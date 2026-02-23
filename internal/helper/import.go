package helper

import (
	"context"
	"encoding/hex"
	"fmt"
	"strings"

	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"

	"github.com/tongxiaofeng/git-remote-bitfs/internal/chain"
	"github.com/tongxiaofeng/git-remote-bitfs/internal/stream"
)

// importHandler encapsulates the import logic for fetching from chain.
type importHandler struct {
	helper      *Helper
	generator   *stream.Generator
	reader      chain.ChainReader
	markCounter int // Auto-incrementing mark counter
	privKey     *ec.PrivateKey
}

// newImportHandler creates a new import handler.
// The reader parameter allows injection of a mock for testing.
func newImportHandler(h *Helper, reader chain.ChainReader) *importHandler {
	return &importHandler{
		helper:    h,
		generator: stream.NewGenerator(h.stdout),
		reader:    reader,
	}
}

// nextMark returns the next auto-incrementing mark string (":1", ":2", ...).
func (ih *importHandler) nextMark() string {
	ih.markCounter++
	return fmt.Sprintf(":%d", ih.markCounter)
}

// handleImport handles the "import <ref>" command from git.
//
// Git sends one or more "import <ref>" lines followed by a blank line.
// The handler reads all requested refs, fetches objects from chain, and
// writes a fast-import stream to stdout, terminated by "done\n".
func (h *Helper) handleImport(firstRef string) error {
	// Collect all import refs. Git may send multiple "import <ref>" lines.
	refs := []string{strings.TrimSpace(firstRef)}

	for {
		line, err := h.stdin.ReadString('\n')
		if err != nil {
			break
		}
		line = strings.TrimRight(line, "\n\r")
		if line == "" {
			break
		}
		cmd, args, _ := strings.Cut(line, " ")
		if cmd == "import" {
			refs = append(refs, strings.TrimSpace(args))
		}
	}

	// Determine the chain reader to use.
	var reader chain.ChainReader
	if h.config.ChainReader != nil {
		reader = h.config.ChainReader
	} else {
		return fmt.Errorf("import: no chain reader available")
	}

	ih := newImportHandler(h, reader)

	// Derive private key for decryption if wallet is available.
	if h.config.Wallet != nil {
		kp, err := h.config.Wallet.DeriveVaultRootKey(h.config.VaultIndex)
		if err != nil {
			h.debugf("warning: could not derive vault root key: %v", err)
		} else {
			ih.privKey = kp.PrivateKey
		}
	}

	return ih.process(refs)
}

// process handles all import ref requests and writes fast-import stream.
func (ih *importHandler) process(refs []string) error {
	ctx := context.Background()

	for _, ref := range refs {
		if err := ih.importRef(ctx, ref); err != nil {
			return fmt.Errorf("importing %s: %w", ref, err)
		}
	}

	// Terminate the fast-import stream.
	if err := ih.generator.WriteDone(); err != nil {
		return fmt.Errorf("writing done: %w", err)
	}

	return nil
}

// importRef imports a single ref from chain.
func (ih *importHandler) importRef(ctx context.Context, ref string) error {
	h := ih.helper

	// Find the anchor chain for this ref.
	store := h.initUTXOStore()
	if store == nil {
		h.debugf("no UTXO store, cannot discover ref %s", ref)
		return nil
	}

	refUTXO := store.GetRefUTXO(ref)
	if refUTXO == nil {
		h.debugf("no ref UTXO for %s, skipping", ref)
		return nil
	}

	latestAnchorTxID, err := hex.DecodeString(refUTXO.AnchorTxID)
	if err != nil || len(latestAnchorTxID) == 0 {
		h.debugf("invalid anchor txid for %s: %s", ref, refUTXO.AnchorTxID)
		return nil
	}

	// Check for incremental fetch: look for the last imported anchor in notes.
	var stopTxID []byte
	stopTxID = ih.findLastImportedAnchor(ref)

	// Walk the anchor chain from newest to oldest (or to the stop point).
	reader := chain.NewReader(ih.reader)
	anchors, err := reader.WalkAnchorChain(ctx, latestAnchorTxID, stopTxID)
	if err != nil {
		return fmt.Errorf("walking anchor chain for %s: %w", ref, err)
	}

	if len(anchors) == 0 {
		h.debugf("no new anchors for %s", ref)
		return nil
	}

	// Set the BranchRef on each anchor for context.
	for _, a := range anchors {
		a.BranchRef = ref
	}

	// Import anchors in chronological order (oldest first).
	// The walk returns newest first, so reverse.
	var prevCommitMark string
	for i := len(anchors) - 1; i >= 0; i-- {
		commitMark, err := ih.importAnchor(ctx, reader, anchors[i], ref, prevCommitMark)
		if err != nil {
			return fmt.Errorf("importing anchor %s: %w",
				hex.EncodeToString(anchors[i].TxID), err)
		}
		prevCommitMark = commitMark
	}

	return nil
}

// importAnchor imports one anchor (one commit) and its tree into the fast-import stream.
// Returns the mark assigned to the commit for use as "from" in the next commit.
func (ih *importHandler) importAnchor(ctx context.Context, reader *chain.Reader,
	anchor *chain.AnchorInfo, ref string, fromMark string) (string, error) {

	// Read the directory tree for this anchor.
	var files []chain.ReadResult
	if len(anchor.TreeRootPNode) > 0 && len(anchor.TreeRootTxID) > 0 {
		var err error
		files, err = reader.ReadTree(ctx, anchor.TreeRootPNode, anchor.TreeRootTxID, ih.privKey)
		if err != nil {
			return "", fmt.Errorf("reading tree: %w", err)
		}
	}

	// Write blobs for each file and collect their marks.
	type blobRef struct {
		mark string
		path string
		mode uint32
	}
	var blobRefs []blobRef

	for _, f := range files {
		mark := ih.nextMark()
		if err := ih.generator.WriteBlob(mark, f.Content); err != nil {
			return "", fmt.Errorf("writing blob for %s: %w", f.Path, err)
		}
		blobRefs = append(blobRefs, blobRef{
			mark: mark,
			path: f.Path,
			mode: f.Mode,
		})
	}

	// Build the commit.
	commitMark := ih.nextMark()

	// Construct the author/committer line in git format.
	author := anchor.Author
	if author == "" {
		author = "BitFS <bitfs@bitfs.org>"
	}

	// Build timestamp string for git fast-import format.
	// Format: "Name <email> timestamp timezone"
	committer := author
	if anchor.Timestamp > 0 {
		// Check if author already has a timestamp.
		if !strings.Contains(author, ">") {
			author = fmt.Sprintf("%s %d +0000", author, anchor.Timestamp)
		}
		if !strings.Contains(committer, ">") {
			committer = fmt.Sprintf("%s %d +0000", committer, anchor.Timestamp)
		}
	}

	// Build file ops.
	var fileOps []stream.FileOp

	// If this is not incremental (no fromMark), use deleteall first
	// to ensure the tree is exactly what the anchor describes.
	if fromMark == "" && len(blobRefs) > 0 {
		// For the first commit, we can use M ops directly (implied empty tree).
	}

	for _, b := range blobRefs {
		fileOps = append(fileOps, stream.FileOp{
			Op:      stream.FileModify,
			Mode:    b.mode,
			DataRef: b.mark,
			Path:    b.path,
		})
	}

	commit := &stream.Commit{
		Ref:       ref,
		Mark:      commitMark,
		Author:    author,
		Committer: committer,
		Message:   anchor.CommitMessage,
		From:      fromMark,
		FileOps:   fileOps,
	}

	if err := ih.generator.WriteCommit(commit); err != nil {
		return "", fmt.Errorf("writing commit: %w", err)
	}

	return commitMark, nil
}

// findLastImportedAnchor checks git notes to find the last imported anchor TxID
// for a given ref. Returns nil if no previous import found.
func (ih *importHandler) findLastImportedAnchor(ref string) []byte {
	h := ih.helper
	if h.notes == nil {
		return nil
	}

	// Look through notes to find a commit note with matching anchor data.
	// For incremental fetch, we stored the last anchor TxID in notes.
	// This is a best-effort lookup.
	shas, err := h.notes.ListNotes()
	if err != nil {
		return nil
	}

	for _, sha := range shas {
		note, err := h.notes.GetNote(sha)
		if err != nil || note == nil {
			continue
		}
		if note.NodeType == "commit" && note.AnchorTxID != "" {
			// Only match notes for the same ref.
			if note.Ref != "" && note.Ref != ref {
				continue
			}
			// Found a commit note -- decode the anchor TxID.
			txID, err := hex.DecodeString(note.AnchorTxID)
			if err == nil && len(txID) > 0 {
				return txID
			}
		}
	}

	return nil
}
