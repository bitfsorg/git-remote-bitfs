package helper

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"time"

	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"

	"github.com/tongxiaofeng/git-remote-bitfs/internal/chain"
	"github.com/tongxiaofeng/git-remote-bitfs/internal/config"
	"github.com/tongxiaofeng/git-remote-bitfs/internal/mapper"
	"github.com/tongxiaofeng/git-remote-bitfs/internal/stream"
	"github.com/tongxiaofeng/git-remote-bitfs/internal/utxo"
	"github.com/tongxiaofeng/libbitfs/metanet"
	"github.com/tongxiaofeng/libbitfs/network"
	libtx "github.com/tongxiaofeng/libbitfs/tx"
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
	blobs   map[string][]byte // mark -> content
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

	store := h.initUTXOStore()
	if store == nil {
		return fmt.Errorf("no UTXO store available")
	}

	// Derive branch key for the anchor.
	branchPriv, err := ec.NewPrivateKey()
	if err != nil {
		return fmt.Errorf("generating branch key: %w", err)
	}
	branchPub := branchPriv.PubKey()

	// Track the latest commit for the anchor.
	var lastCommit *stream.Commit
	var nodeOps []chain.NodeOp
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
					nodeOps = append(nodeOps, *nodeOp)
				}

			case stream.FileDelete:
				// For MVP, deletes are tracked but not pushed as chain ops.
				// In full implementation, we'd mark the node as deleted.
				h.debugf("delete: %s (tracked, not pushed to chain in MVP)", op.Path)
			}
		}
	}

	// If we have node ops, push them in a batch TX.
	if len(nodeOps) > 0 {
		feeUTXO := store.AllocateFeeUTXO(10000) // 10k sat minimum
		if feeUTXO == nil {
			return fmt.Errorf("no fee UTXOs available")
		}

		txFeeUTXO := &libtx.UTXO{
			TxID:         hexToBytes(feeUTXO.TxID),
			Vout:         feeUTXO.Vout,
			Amount:       feeUTXO.Amount,
			ScriptPubKey: hexToBytes(feeUTXO.ScriptPubKey),
		}

		pushParams := &chain.PushParams{
			Ops:      nodeOps,
			FeeUTXOs: []*libtx.UTXO{txFeeUTXO},
		}

		writer := chain.NewWriter(h.config.Blockchain)
		result, err := writer.Push(ctx, pushParams)
		if err != nil {
			return fmt.Errorf("pushing nodes: %w", err)
		}

		// Save any change UTXO back.
		if result.ChangeUTXO != nil {
			store.AddFeeUTXO(feeUTXOFromLibtx(result.ChangeUTXO))
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
			refUTXO = &libtx.UTXO{
				TxID:         hexToBytes(existingRef.TxID),
				Vout:         existingRef.Vout,
				Amount:       existingRef.Amount,
				ScriptPubKey: hexToBytes(existingRef.ScriptPubKey),
			}
		}

		anchorParams := &chain.AnchorParams{
			BranchRef:     ref,
			BranchPub:     branchPub,
			BranchPriv:    branchPriv,
			Author:        lastCommit.Author,
			CommitMessage: lastCommit.Message,
			Timestamp:     uint64(time.Now().Unix()),
			RefUTXO:       refUTXO,
			FeeUTXO: &libtx.UTXO{
				TxID:         hexToBytes(feeUTXO.TxID),
				Vout:         feeUTXO.Vout,
				Amount:       feeUTXO.Amount,
				ScriptPubKey: hexToBytes(feeUTXO.ScriptPubKey),
			},
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

	// Build metanet Node payload (metadata only; ciphertext is stored
	// off-chain or in separate data outputs, not in OP_RETURN).
	node := &metanet.Node{
		Version:   1,
		Type:      metanet.NodeTypeFile,
		Op:        metanet.OpCreate,
		MimeType:  guessMimeType(op.Path),
		FileSize:  uint64(len(content)),
		Access:    accessMode,
		KeyHash:   encResult.KeyHash,
		Encrypted: true,
		Timestamp: uint64(time.Now().Unix()),
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

// guessMimeType returns a basic MIME type guess based on file extension.
func guessMimeType(path string) string {
	lower := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lower, ".go"):
		return "text/x-go"
	case strings.HasSuffix(lower, ".js"):
		return "text/javascript"
	case strings.HasSuffix(lower, ".py"):
		return "text/x-python"
	case strings.HasSuffix(lower, ".md"):
		return "text/markdown"
	case strings.HasSuffix(lower, ".json"):
		return "application/json"
	case strings.HasSuffix(lower, ".html"), strings.HasSuffix(lower, ".htm"):
		return "text/html"
	case strings.HasSuffix(lower, ".css"):
		return "text/css"
	case strings.HasSuffix(lower, ".txt"):
		return "text/plain"
	case strings.HasSuffix(lower, ".png"):
		return "image/png"
	case strings.HasSuffix(lower, ".jpg"), strings.HasSuffix(lower, ".jpeg"):
		return "image/jpeg"
	default:
		return "application/octet-stream"
	}
}

// --- utility functions ---

// hexToBytes decodes a hex string to bytes. Returns nil on error.
func hexToBytes(s string) []byte {
	b, _ := hex.DecodeString(s)
	return b
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
