package helper

import (
	"fmt"
)

// handleList handles the "list" and "list for-push" commands.
//
// For "list for-push":
//   - Read local UTXO store to determine what refs we've previously pushed.
//   - Report: "<sha1> refs/heads/main\n\n"
//   - If no refs are known yet, output just "\n" (empty list).
//
// For "list" (fetch):
//   - Read ref UTXOs from the UTXO store to find known refs.
//   - Report refs with their commit SHAs.
//   - For MVP: read from UTXO store's RefUTXOs.
//
// Output format:
//
//	<sha1> refs/heads/main
//	<sha1> refs/tags/v1.0
//	@refs/heads/main HEAD
//	<empty line>
func (h *Helper) handleList(args string) error {
	store := h.initUTXOStore()

	// Collect known refs from the UTXO store.
	type refInfo struct {
		sha string
		ref string
	}
	var refs []refInfo

	if store != nil {
		state := store.State()
		for _, r := range state.RefUTXOs {
			if r.Ref != "" && r.AnchorTxID != "" {
				// For now, we use AnchorTxID as a placeholder for the commit SHA.
				// In a full implementation, git notes would map anchor TxIDs back
				// to commit SHAs. When we have notes available, we'd look up the
				// actual git commit SHA.
				sha := lookupCommitSHA(h, r.Ref, r.AnchorTxID)
				if sha != "" {
					refs = append(refs, refInfo{sha: sha, ref: r.Ref})
				}
			}
		}
	}

	// Output refs.
	if len(refs) > 0 {
		for _, r := range refs {
			if err := h.writef("%s %s\n", r.sha, r.ref); err != nil {
				return fmt.Errorf("writing ref: %w", err)
			}
		}
		// Report HEAD as a symref pointing to the first branch we find.
		for _, r := range refs {
			if r.ref == "refs/heads/main" || r.ref == "refs/heads/master" {
				if err := h.writef("@%s HEAD\n", r.ref); err != nil {
					return fmt.Errorf("writing HEAD: %w", err)
				}
				break
			}
		}
	}

	// Terminate with empty line.
	if err := h.writef("\n"); err != nil {
		return fmt.Errorf("writing list terminator: %w", err)
	}
	return nil
}

// lookupCommitSHA attempts to find the git commit SHA associated with an
// anchor TxID by scanning git notes. Returns empty string if not found.
//
// For the MVP, this walks the notes store looking for a commit note whose
// anchor_txid matches. In a production implementation, we'd maintain a
// reverse index.
func lookupCommitSHA(h *Helper, ref, anchorTxID string) string {
	if h.notes == nil {
		return ""
	}

	shas, err := h.notes.ListNotes()
	if err != nil {
		h.debugf("warning: listing notes: %v", err)
		return ""
	}

	for _, sha := range shas {
		note, err := h.notes.GetNote(sha)
		if err != nil {
			continue
		}
		if note != nil && note.NodeType == "commit" && note.AnchorTxID == anchorTxID {
			return sha
		}
	}
	return ""
}
