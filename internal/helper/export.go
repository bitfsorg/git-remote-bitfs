package helper

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"

	"github.com/tongxiaofeng/git-remote-bitfs/internal/chain"
	"github.com/tongxiaofeng/git-remote-bitfs/internal/config"
	"github.com/tongxiaofeng/git-remote-bitfs/internal/mapper"
	"github.com/tongxiaofeng/git-remote-bitfs/internal/stream"
	"github.com/tongxiaofeng/git-remote-bitfs/internal/utxo"
	"github.com/tongxiaofeng/libbitfs-go/metanet"
	"github.com/tongxiaofeng/libbitfs-go/network"
	libtx "github.com/tongxiaofeng/libbitfs-go/tx"
	"github.com/tongxiaofeng/libbitfs-go/wallet"
)

// ChainPusher abstracts the chain write operations for testability.
// In production, this wraps chain.Writer + chain.BuildAnchor.
// In tests, it can be replaced with a mock.
type ChainPusher interface {
	// PushNodes builds, signs, and broadcasts a batch TX with the given ops.
	PushNodes(ctx context.Context, params *chain.PushParams) (*chain.PushResult, error)

	// PushAnchor builds, signs, and broadcasts an anchor TX.
	PushAnchor(ctx context.Context, params *chain.AnchorParams) (*chain.AnchorResult, error)
}

// defaultChainPusher wraps the real chain.Writer and chain.BuildAnchor.
type defaultChainPusher struct {
	writer *chain.Writer
	bc     network.BlockchainService
}

func (d *defaultChainPusher) PushNodes(ctx context.Context, params *chain.PushParams) (*chain.PushResult, error) {
	return d.writer.Push(ctx, params)
}

func (d *defaultChainPusher) PushAnchor(ctx context.Context, params *chain.AnchorParams) (*chain.AnchorResult, error) {
	result, err := chain.BuildAnchor(params)
	if err != nil {
		return nil, err
	}
	result, err = chain.SignAndBroadcastAnchor(ctx, d.bc, result, params)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// exportHandler encapsulates the export logic, testable independently.
type exportHandler struct {
	helper  *Helper
	parser  *stream.Parser
	pusher  ChainPusher        // optional; nil = create default from config
	blobs   map[string][]byte  // mark -> content
	commits []*stream.Commit
	resets  []*stream.Reset
	tags    []*stream.Tag
}

func newExportHandler(h *Helper) *exportHandler {
	return &exportHandler{
		helper: h,
		parser: stream.NewParser(h.stdin),
		blobs:  make(map[string][]byte),
	}
}

// handleExport handles the "export" command from git.
// It reads a fast-export stream from stdin, processes changes,
// optionally builds and broadcasts transactions, then reports results.
func (h *Helper) handleExport() error {
	eh := newExportHandler(h)

	// Phase 1: Parse the entire fast-export stream.
	if err := eh.parse(); err != nil {
		return fmt.Errorf("parsing fast-export stream: %w", err)
	}

	// Phase 2: Process commits and push to chain (if chain is configured).
	refResults := make(map[string]exportRefResult)
	if h.config.Blockchain != nil && h.config.Wallet != nil {
		if err := eh.pushToChain(refResults); err != nil {
			// On chain error, report failure for all refs.
			for _, c := range eh.commits {
				if _, exists := refResults[c.Ref]; !exists {
					refResults[c.Ref] = exportRefResult{
						err: err.Error(),
					}
				}
			}
		}
	} else {
		// No chain configured -- protocol-only mode.
		// Mark all refs as OK (useful for testing the protocol plumbing).
		for _, c := range eh.commits {
			refResults[c.Ref] = exportRefResult{ok: true}
		}
		for _, r := range eh.resets {
			refResults[r.Ref] = exportRefResult{ok: true}
		}
	}

	// Phase 3: Report results per ref.
	for ref, result := range refResults {
		if result.ok {
			if err := h.writef("ok %s\n", ref); err != nil {
				return err
			}
		} else {
			if err := h.writef("error %s %s\n", ref, result.err); err != nil {
				return err
			}
		}
	}

	// Terminate with empty line.
	if err := h.writef("\n"); err != nil {
		return err
	}
	return nil
}

// exportRefResult tracks the push result for a single ref.
type exportRefResult struct {
	ok  bool
	err string
}

// parse reads the entire fast-export stream, collecting blobs and commits.
func (eh *exportHandler) parse() error {
	for {
		cmd, err := eh.parser.Next()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("reading command: %w", err)
		}

		switch cmd.Type {
		case stream.CmdBlob:
			if cmd.Blob.Mark != "" {
				eh.blobs[cmd.Blob.Mark] = cmd.Blob.Data
			}
		case stream.CmdCommit:
			eh.commits = append(eh.commits, cmd.Commit)
		case stream.CmdReset:
			eh.resets = append(eh.resets, cmd.Reset)
		case stream.CmdTag:
			eh.tags = append(eh.tags, cmd.Tag)
		}
	}
}

// pushToChain processes parsed commits and pushes file/dir nodes + anchor
// to the chain. Updates refResults with per-ref success/failure.
func (eh *exportHandler) pushToChain(refResults map[string]exportRefResult) error {
	h := eh.helper
	ctx := context.Background()

	// Group commits by ref for anchor construction.
	commitsByRef := make(map[string][]*stream.Commit)
	for _, c := range eh.commits {
		commitsByRef[c.Ref] = append(commitsByRef[c.Ref], c)
	}

	// Process each ref.
	for ref, commits := range commitsByRef {
		if err := eh.pushRef(ctx, ref, commits); err != nil {
			refResults[ref] = exportRefResult{err: err.Error()}
			h.debugf("error pushing %s: %v", ref, err)
		} else {
			refResults[ref] = exportRefResult{ok: true}
		}
	}
	return nil
}

// pushRef processes all commits for a single ref and pushes them to chain.
func (eh *exportHandler) pushRef(ctx context.Context, ref string, commits []*stream.Commit) error {
	h := eh.helper

	// Load access policy from .bitfsattributes (default: all private).
	policy := config.DefaultPolicy()

	// Try to load .bitfsattributes from the commit's file tree.
	for _, c := range commits {
		for _, op := range c.FileOps {
			if op.Path == ".bitfsattributes" && op.Op == stream.FileModify {
				var data []byte
				if op.DataRef == "inline" {
					data = op.Data
				} else if d, ok := eh.blobs[op.DataRef]; ok {
					data = d
				}
				if data != nil {
					if p, err := config.ParseAttributes(data); err == nil {
						policy = p
					}
				}
			}
		}
	}

	store := h.initUTXOStore()
	if store == nil {
		return fmt.Errorf("no UTXO store available")
	}

	// Derive branch key for the anchor.
	// Deterministic from wallet + SHA256(ref), or random if wallet is nil.
	branchPriv, branchPub, err := deriveBranchKey(h.config.Wallet, h.config.VaultIndex, ref)
	if err != nil {
		return fmt.Errorf("deriving branch key: %w", err)
	}

	// Track the latest commit for the anchor.
	var lastCommit *stream.Commit

	// fileEntry tracks a file NodeOp alongside its path for directory tree building.
	type fileEntry struct {
		op   chain.NodeOp
		path string
	}
	var fileEntries []fileEntry
	nodeIndex := uint32(0)

	for _, c := range commits {
		lastCommit = c

		for _, op := range c.FileOps {
			switch op.Op {
			case stream.FileModify:
				nodeOp, err := eh.buildFileNodeOp(op, policy, &nodeIndex)
				if err != nil {
					return fmt.Errorf("building node op for %s: %w", op.Path, err)
				}
				if nodeOp != nil {
					fileEntries = append(fileEntries, fileEntry{op: *nodeOp, path: op.Path})
				}

			case stream.FileDelete:
				// For MVP, deletes are tracked but not pushed as chain ops.
				h.debugf("delete: %s (tracked, not pushed to chain in MVP)", op.Path)
			}
		}
	}

	// Build directory tree from file entries and collect all node ops.
	var allOps []chain.NodeOp
	var rootDirPubKey *ec.PublicKey

	if len(fileEntries) > 0 {
		// Extract file ops and paths for directory tree building.
		var fileOps []chain.NodeOp
		var filePaths []string
		for _, fe := range fileEntries {
			fileOps = append(fileOps, fe.op)
			filePaths = append(filePaths, fe.path)
		}

		dirOps, rootPub, err := eh.buildDirTree(fileOps, filePaths)
		if err != nil {
			return fmt.Errorf("building directory tree: %w", err)
		}

		// All ops: files first, then directories (leaf dirs to root).
		allOps = append(allOps, fileOps...)
		allOps = append(allOps, dirOps...)
		rootDirPubKey = rootPub
	}

	// Push all node ops (files + directories) in a batch TX.
	var pushResult *chain.PushResult
	if len(allOps) > 0 {
		feeUTXO := store.AllocateFeeUTXO(10000) // 10k sat minimum
		if feeUTXO == nil {
			return fmt.Errorf("no fee UTXOs available")
		}

		txFeeUTXO, err := feeUTXOToLibtx(feeUTXO)
		if err != nil {
			return fmt.Errorf("converting fee UTXO: %w", err)
		}

		pusher := eh.pusher
		if pusher == nil {
			pusher = &defaultChainPusher{
				writer: chain.NewWriter(h.config.Blockchain),
				bc:     h.config.Blockchain,
			}
		}

		pushParams := &chain.PushParams{
			Ops:      allOps,
			FeeUTXOs: []*libtx.UTXO{txFeeUTXO},
		}

		pushResult, err = pusher.PushNodes(ctx, pushParams)
		if err != nil {
			return fmt.Errorf("pushing nodes: %w", err)
		}

		// Save any change UTXO back.
		if pushResult.ChangeUTXO != nil {
			store.AddFeeUTXO(feeUTXOFromLibtx(pushResult.ChangeUTXO))
		}
	}

	// Build and push anchor TX for this ref.
	if lastCommit != nil {
		feeUTXO := store.AllocateFeeUTXO(5000) // 5k sat for anchor
		if feeUTXO == nil {
			return fmt.Errorf("no fee UTXOs available for anchor")
		}

		// Check for existing ref UTXO (subsequent push).
		var refUTXO *libtx.UTXO
		existingRef := store.GetRefUTXO(ref)
		if existingRef != nil {
			refUTXO, err = refUTXOToLibtx(existingRef)
			if err != nil {
				return fmt.Errorf("converting ref UTXO: %w", err)
			}
		}

		anchorFeeUTXO, err := feeUTXOToLibtx(feeUTXO)
		if err != nil {
			return fmt.Errorf("converting anchor fee UTXO: %w", err)
		}

		anchorParams := &chain.AnchorParams{
			BranchRef:     ref,
			BranchPub:     branchPub,
			BranchPriv:    branchPriv,
			Author:        lastCommit.Author,
			CommitMessage: lastCommit.Message,
			Timestamp:     uint64(time.Now().Unix()),
			RefUTXO:       refUTXO,
			FeeUTXO:       anchorFeeUTXO,
		}

		// Set tree root info from the push result.
		if rootDirPubKey != nil && pushResult != nil {
			anchorParams.TreeRootPNode = rootDirPubKey.Compressed()
			rootPNodeHex := hex.EncodeToString(rootDirPubKey.Compressed())
			if rootUTXO, ok := pushResult.NodeUTXOs[rootPNodeHex]; ok {
				anchorParams.TreeRootTxID = rootUTXO.TxID
			}
		}

		// Set parent anchor TxID for chain linking (subsequent pushes).
		if existingRef != nil && existingRef.AnchorTxID != "" {
			parentTxID, pErr := hex.DecodeString(existingRef.AnchorTxID)
			if pErr == nil && len(parentTxID) > 0 {
				anchorParams.ParentAnchorTxIDs = [][]byte{parentTxID}
			}
		}

		anchorResult, err := chain.BuildAnchor(anchorParams)
		if err != nil {
			return fmt.Errorf("building anchor: %w", err)
		}

		anchorResult, err = chain.SignAndBroadcastAnchor(ctx, h.config.Blockchain, anchorResult, anchorParams)
		if err != nil {
			return fmt.Errorf("broadcasting anchor: %w", err)
		}

		// Update ref UTXO in store.
		if anchorResult.RefUTXO != nil {
			store.SetRefUTXO(refUTXOFromAnchor(ref, branchPub, anchorResult))
		}

		// Save change UTXO.
		if anchorResult.ChangeUTXO != nil {
			store.AddFeeUTXO(feeUTXOFromLibtx(anchorResult.ChangeUTXO))
		}

		// Save UTXO store.
		if err := store.Save(); err != nil {
			h.debugf("warning: saving UTXO store: %v", err)
		}

		// Update git notes for the last commit if notes store is available.
		if h.notes != nil && lastCommit.Mark != "" {
			anchorTxIDHex := hex.EncodeToString(anchorResult.TxID)
			note := &mapper.NoteData{
				NodeType:   "commit",
				AnchorTxID: anchorTxIDHex,
				PNode:      hex.EncodeToString(branchPub.Compressed()),
			}
			// Best effort: notes require a real git SHA, not a mark.
			// In practice, git would resolve marks after fast-import.
			// For now, we skip note writing during export (notes are more
			// relevant during import/fetch).
			_ = note
		}
	}

	return nil
}

// buildFileNodeOp creates a NodeOp for a file modify operation.
func (eh *exportHandler) buildFileNodeOp(op stream.FileOp, policy *config.AccessPolicy, index *uint32) (*chain.NodeOp, error) {
	// Resolve blob content.
	var content []byte
	if op.DataRef == "inline" {
		content = op.Data
	} else if data, ok := eh.blobs[op.DataRef]; ok {
		content = data
	} else {
		return nil, fmt.Errorf("blob %s not found", op.DataRef)
	}

	// Determine access policy for this file.
	access, _ := policy.GetAccess(op.Path)

	// Generate a key pair for this file node.
	nodePriv, err := ec.NewPrivateKey()
	if err != nil {
		return nil, fmt.Errorf("generating node key: %w", err)
	}
	nodePub := nodePriv.PubKey()

	// Encrypt the content.
	encResult, err := chain.EncryptFile(content, nodePriv, nodePub, access)
	if err != nil {
		return nil, fmt.Errorf("encrypting %s: %w", op.Path, err)
	}

	// Determine access mode for the metanet node.
	accessMode := metanet.AccessPrivate
	switch access {
	case "free":
		accessMode = metanet.AccessFree
	case "paid":
		accessMode = metanet.AccessPaid
	}

	// Build metanet Node payload with inline encrypted content.
	node := &metanet.Node{
		Version:    1,
		Type:       metanet.NodeTypeFile,
		Op:         metanet.OpCreate,
		MimeType:   guessMimeType(op.Path),
		FileSize:   uint64(len(content)),
		Access:     accessMode,
		KeyHash:    encResult.KeyHash,
		Encrypted:  true,
		EncPayload: encResult.Ciphertext,
		Timestamp:  uint64(time.Now().Unix()),
	}

	payload, err := metanet.SerializePayload(node)
	if err != nil {
		return nil, fmt.Errorf("serializing payload for %s: %w", op.Path, err)
	}

	*index++

	return &chain.NodeOp{
		PubKey:  nodePub,
		PrivKey: nodePriv,
		Payload: payload,
		Purpose: "file:" + op.Path,
	}, nil
}

// buildDirTree constructs directory NodeOps from the file operations.
// It creates a directory node for each unique parent directory, with proper
// ChildEntry lists and MerkleRoot hashes.
// Returns directory ops (leaf dirs first, root last) and the root dir's PubKey.
func (eh *exportHandler) buildDirTree(fileOps []chain.NodeOp, filePaths []string) ([]chain.NodeOp, *ec.PublicKey, error) {
	// dirInfo tracks a directory being built.
	type dirInfo struct {
		path     string
		privKey  *ec.PrivateKey
		pubKey   *ec.PublicKey
		children []metanet.ChildEntry
	}

	// Collect unique directory paths.
	dirs := make(map[string]*dirInfo)

	// Ensure root directory "." exists.
	rootPriv, err := ec.NewPrivateKey()
	if err != nil {
		return nil, nil, fmt.Errorf("generating root dir key: %w", err)
	}
	dirs["."] = &dirInfo{
		path:    ".",
		privKey: rootPriv,
		pubKey:  rootPriv.PubKey(),
	}

	// Create directory entries for all intermediate paths.
	for _, fp := range filePaths {
		dir := path.Dir(fp)
		if dir == "" {
			dir = "."
		}
		// Ensure all ancestors exist.
		for dir != "." {
			if _, exists := dirs[dir]; !exists {
				priv, err := ec.NewPrivateKey()
				if err != nil {
					return nil, nil, fmt.Errorf("generating dir key for %s: %w", dir, err)
				}
				dirs[dir] = &dirInfo{
					path:    dir,
					privKey: priv,
					pubKey:  priv.PubKey(),
				}
			}
			dir = path.Dir(dir)
		}
	}

	// Add file entries to their parent directories.
	for i, fp := range filePaths {
		dir := path.Dir(fp)
		if dir == "" {
			dir = "."
		}
		dirEntry := dirs[dir]
		child := metanet.ChildEntry{
			Index:    uint32(len(dirEntry.children)),
			Name:     path.Base(fp),
			Type:     metanet.NodeTypeFile,
			PubKey:   fileOps[i].PubKey.Compressed(),
			Hardened: true,
		}
		dirEntry.children = append(dirEntry.children, child)
	}

	// Add subdirectory entries to their parent directories.
	// Sort directory names to get deterministic ordering.
	dirNames := make([]string, 0, len(dirs))
	for name := range dirs {
		if name != "." {
			dirNames = append(dirNames, name)
		}
	}
	sort.Strings(dirNames)

	for _, dirName := range dirNames {
		parentDir := path.Dir(dirName)
		if parentDir == "" {
			parentDir = "."
		}
		parentEntry := dirs[parentDir]
		child := metanet.ChildEntry{
			Index:    uint32(len(parentEntry.children)),
			Name:     path.Base(dirName),
			Type:     metanet.NodeTypeDir,
			PubKey:   dirs[dirName].pubKey.Compressed(),
			Hardened: true,
		}
		parentEntry.children = append(parentEntry.children, child)
	}

	// Build directory NodeOps bottom-up (leaf dirs first, root last).
	// Sort by path depth descending so deepest directories come first.
	allDirNames := append(dirNames, ".")
	sort.Slice(allDirNames, func(i, j int) bool {
		di := strings.Count(allDirNames[i], "/")
		dj := strings.Count(allDirNames[j], "/")
		if di != dj {
			return di > dj // deeper directories first
		}
		return allDirNames[i] < allDirNames[j]
	})

	var dirOps []chain.NodeOp
	for _, dirName := range allDirNames {
		di := dirs[dirName]

		// Compute MerkleRoot from children.
		var merkleRoot []byte
		if len(di.children) > 0 {
			merkleRoot = metanet.ComputeDirectoryMerkleRoot(di.children)
		}

		node := &metanet.Node{
			Version:        1,
			Type:           metanet.NodeTypeDir,
			Op:             metanet.OpCreate,
			Children:       di.children,
			NextChildIndex: uint32(len(di.children)),
			MerkleRoot:     merkleRoot,
			Timestamp:      uint64(time.Now().Unix()),
		}

		payload, err := metanet.SerializePayload(node)
		if err != nil {
			return nil, nil, fmt.Errorf("serializing dir payload for %s: %w", dirName, err)
		}

		dirOps = append(dirOps, chain.NodeOp{
			PubKey:  di.pubKey,
			PrivKey: di.privKey,
			Payload: payload,
			Purpose: "dir:" + dirName,
		})
	}

	return dirOps, dirs["."].pubKey, nil
}

// guessMimeType returns a MIME type based on file extension using the
// standard library, with fallbacks for source code types not in the
// system MIME database.
func guessMimeType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))

	// Fallback map for source code extensions that mime.TypeByExtension
	// often doesn't know about (depends on OS MIME database).
	fallbacks := map[string]string{
		".go": "text/x-go",
		".py": "text/x-python",
		".md": "text/markdown",
	}

	// Try the standard library first.
	if mimeType := mime.TypeByExtension(ext); mimeType != "" {
		return mimeType
	}

	// Check our fallbacks for source code.
	if mimeType, ok := fallbacks[ext]; ok {
		return mimeType
	}

	return "application/octet-stream"
}

// deriveBranchKey derives a deterministic branch key for a ref.
// Uses SHA256(ref) mod 2^31 as the BIP32 derivation index from the wallet.
// Falls back to a random key when wallet is nil (testing mode).
func deriveBranchKey(w *wallet.Wallet, vaultIndex uint32, ref string) (*ec.PrivateKey, *ec.PublicKey, error) {
	if w == nil {
		// No wallet: fall back to random key (testing/protocol-only mode).
		priv, err := ec.NewPrivateKey()
		if err != nil {
			return nil, nil, fmt.Errorf("generating random branch key: %w", err)
		}
		return priv, priv.PubKey(), nil
	}

	// Deterministic: derive from SHA256(ref) to get a stable index.
	h := sha256.Sum256([]byte(ref))
	index := binary.BigEndian.Uint32(h[:4]) & 0x7FFFFFFF // ensure non-hardened range

	// Derive: m/44'/236'/(V+1)'/0/0/0/<index>
	// Use index 0 as the "branch keys" namespace, then the ref-specific index.
	kp, err := w.DeriveNodeKey(vaultIndex, []uint32{0, index}, []bool{true, true})
	if err != nil {
		return nil, nil, fmt.Errorf("deriving branch key: %w", err)
	}
	return kp.PrivateKey, kp.PublicKey, nil
}

// --- utility functions ---

// hexToBytes decodes a hex string to bytes, returning an error on failure.
func hexToBytes(s string) ([]byte, error) {
	return hex.DecodeString(s)
}

// feeUTXOToLibtx converts a utxo.FeeUTXO to a libtx.UTXO with error handling.
func feeUTXOToLibtx(u *utxo.FeeUTXO) (*libtx.UTXO, error) {
	txid, err := hex.DecodeString(u.TxID)
	if err != nil {
		return nil, fmt.Errorf("decoding fee UTXO txid: %w", err)
	}
	script, err := hex.DecodeString(u.ScriptPubKey)
	if err != nil {
		return nil, fmt.Errorf("decoding fee UTXO script: %w", err)
	}
	return &libtx.UTXO{
		TxID:         txid,
		Vout:         u.Vout,
		Amount:       u.Amount,
		ScriptPubKey: script,
	}, nil
}

// refUTXOToLibtx converts a utxo.RefUTXO to a libtx.UTXO with error handling.
func refUTXOToLibtx(u *utxo.RefUTXO) (*libtx.UTXO, error) {
	txid, err := hex.DecodeString(u.TxID)
	if err != nil {
		return nil, fmt.Errorf("decoding ref UTXO txid: %w", err)
	}
	script, err := hex.DecodeString(u.ScriptPubKey)
	if err != nil {
		return nil, fmt.Errorf("decoding ref UTXO script: %w", err)
	}
	return &libtx.UTXO{
		TxID:         txid,
		Vout:         u.Vout,
		Amount:       u.Amount,
		ScriptPubKey: script,
	}, nil
}

// feeUTXOFromLibtx converts a libtx.UTXO to a utxo.FeeUTXO for the store.
func feeUTXOFromLibtx(u *libtx.UTXO) utxo.FeeUTXO {
	return utxo.FeeUTXO{
		TxID:         hex.EncodeToString(u.TxID),
		Vout:         u.Vout,
		Amount:       u.Amount,
		ScriptPubKey: hex.EncodeToString(u.ScriptPubKey),
	}
}

// refUTXOFromAnchor builds a RefUTXO from an anchor result.
func refUTXOFromAnchor(ref string, branchPub *ec.PublicKey, result *chain.AnchorResult) utxo.RefUTXO {
	return utxo.RefUTXO{
		Ref:          ref,
		TxID:         hex.EncodeToString(result.RefUTXO.TxID),
		Vout:         result.RefUTXO.Vout,
		Amount:       result.RefUTXO.Amount,
		PNode:        hex.EncodeToString(branchPub.Compressed()),
		ScriptPubKey: hex.EncodeToString(result.RefUTXO.ScriptPubKey),
		AnchorTxID:   hex.EncodeToString(result.TxID),
	}
}
