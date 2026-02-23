// Package mapper manages the bidirectional mapping between git object SHAs
// and Metanet node identifiers, stored as git notes in refs/notes/bitfs.
package mapper

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// notesRef is the git notes reference used for bitfs mappings.
const notesRef = "bitfs"

// NoteData represents the mapping data stored in git notes for each git object.
// For blob/tree objects, the common fields are populated. For commit objects,
// the anchor fields are also populated.
type NoteData struct {
	// Common fields (blob/tree/commit)
	PNode      string `json:"pnode,omitempty"`       // hex-encoded P_node public key (66 chars)
	TxID       string `json:"txid,omitempty"`        // hex-encoded transaction ID (64 chars)
	KeyHash    string `json:"key_hash,omitempty"`    // hex-encoded SHA256(SHA256(plaintext))
	Access     int    `json:"access,omitempty"`      // 0=private, 1=free, 2=paid
	ChildIndex int    `json:"child_index,omitempty"` // index within parent directory
	NodeType   string `json:"node_type,omitempty"`   // "blob", "tree", "commit"

	// Anchor fields (commit only)
	AnchorTxID string `json:"anchor_txid,omitempty"` // hex-encoded anchor TX ID
	TreePNode  string `json:"tree_pnode,omitempty"`  // root tree's P_node
	TreeTxID   string `json:"tree_txid,omitempty"`   // root tree's TX ID
	Ref        string `json:"ref,omitempty"`         // git ref (e.g. "refs/heads/main")
}

// NotesStore reads and writes git notes in refs/notes/bitfs.
type NotesStore struct {
	gitDir string // path to .git directory (for running git commands)
}

// NewNotesStore creates a new NotesStore that operates on the given git directory.
func NewNotesStore(gitDir string) *NotesStore {
	return &NotesStore{gitDir: gitDir}
}

// GetNote reads the note for a git object SHA.
// Returns nil, nil if no note exists.
func (s *NotesStore) GetNote(objectSHA string) (*NoteData, error) {
	stdout, err := s.runGit("notes", "--ref="+notesRef, "show", objectSHA)
	if err != nil {
		// "no note found" is not an error — return nil.
		if isNoNoteError(err, stdout) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading note for %s: %w", objectSHA, err)
	}

	var data NoteData
	if err := json.Unmarshal([]byte(stdout), &data); err != nil {
		return nil, fmt.Errorf("decoding note for %s: %w", objectSHA, err)
	}
	return &data, nil
}

// SetNote writes or overwrites a note for a git object SHA.
func (s *NotesStore) SetNote(objectSHA string, data *NoteData) error {
	encoded, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("encoding note for %s: %w", objectSHA, err)
	}

	_, err = s.runGit("notes", "--ref="+notesRef, "add", "-f", "-m", string(encoded), objectSHA)
	if err != nil {
		return fmt.Errorf("writing note for %s: %w", objectSHA, err)
	}
	return nil
}

// RemoveNote removes the note for a git object.
// Returns nil if no note exists (idempotent).
func (s *NotesStore) RemoveNote(objectSHA string) error {
	stdout, err := s.runGit("notes", "--ref="+notesRef, "remove", objectSHA)
	if err != nil {
		// Removing a non-existent note is not an error.
		if isNoNoteError(err, stdout) {
			return nil
		}
		return fmt.Errorf("removing note for %s: %w", objectSHA, err)
	}
	return nil
}

// ListNotes returns all object SHAs that have notes in refs/notes/bitfs.
func (s *NotesStore) ListNotes() ([]string, error) {
	stdout, err := s.runGit("notes", "--ref="+notesRef, "list")
	if err != nil {
		// If the notes ref doesn't exist yet, list returns an error.
		// Treat as empty list.
		if strings.Contains(stdout, "Unexpected end of output") ||
			strings.Contains(stdout, "does not exist") {
			return nil, nil
		}
		return nil, fmt.Errorf("listing notes: %w", err)
	}

	stdout = strings.TrimSpace(stdout)
	if stdout == "" {
		return nil, nil
	}

	// Each line is "<note-sha> <object-sha>".
	var shas []string
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			shas = append(shas, parts[1])
		}
	}
	return shas, nil
}

// runGit executes a git command with GIT_DIR set and returns stdout.
// On error, both stdout content and the error are returned so callers
// can inspect output for expected conditions (e.g., "no note found").
func (s *NotesStore) runGit(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Env = append(cmd.Environ(), "GIT_DIR="+s.gitDir)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		// Combine stderr into the returned string for error inspection.
		combined := strings.TrimSpace(stdout.String() + "\n" + stderr.String())
		return combined, fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), combined, err)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// isNoNoteError checks whether a git notes error indicates "no note found"
// for the given object, which is an expected condition, not a real error.
func isNoNoteError(err error, output string) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(output)
	return strings.Contains(lower, "no note found") ||
		strings.Contains(lower, "could not find") ||
		strings.Contains(lower, "has no note")
}
