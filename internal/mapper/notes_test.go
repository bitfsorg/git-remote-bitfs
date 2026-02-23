package mapper

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initTestRepo creates a temporary git repo with one empty commit and returns
// the path to the .git directory. The temporary directory is cleaned up by t.Cleanup.
func initTestRepo(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	gitDir := filepath.Join(dir, ".git")

	// git init
	run(t, dir, "git", "init")
	// configure user for commits
	run(t, dir, "git", "config", "user.email", "test@test.com")
	run(t, dir, "git", "config", "user.name", "Test")
	// create an initial commit so we have an object to attach notes to
	run(t, dir, "git", "commit", "--allow-empty", "-m", "init")

	return gitDir
}

// headSHA returns the HEAD commit SHA in the given repo.
func headSHA(t *testing.T, gitDir string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Env = append(os.Environ(), "GIT_DIR="+gitDir)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v", err)
	}
	return string(out[:len(out)-1]) // trim newline
}

// run executes a command in the given directory, failing the test on error.
func run(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@test.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v failed: %s: %v", name, args, out, err)
	}
}

func TestSetAndGetNote(t *testing.T) {
	gitDir := initTestRepo(t)
	sha := headSHA(t, gitDir)
	store := NewNotesStore(gitDir)

	note := &NoteData{
		PNode:      "02" + "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
		TxID:       "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
		KeyHash:    "1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
		Access:     1,
		ChildIndex: 3,
		NodeType:   "blob",
	}

	if err := store.SetNote(sha, note); err != nil {
		t.Fatalf("SetNote: %v", err)
	}

	got, err := store.GetNote(sha)
	if err != nil {
		t.Fatalf("GetNote: %v", err)
	}
	if got == nil {
		t.Fatal("GetNote returned nil")
	}

	if got.PNode != note.PNode {
		t.Errorf("PNode = %q, want %q", got.PNode, note.PNode)
	}
	if got.TxID != note.TxID {
		t.Errorf("TxID = %q, want %q", got.TxID, note.TxID)
	}
	if got.KeyHash != note.KeyHash {
		t.Errorf("KeyHash = %q, want %q", got.KeyHash, note.KeyHash)
	}
	if got.Access != note.Access {
		t.Errorf("Access = %d, want %d", got.Access, note.Access)
	}
	if got.ChildIndex != note.ChildIndex {
		t.Errorf("ChildIndex = %d, want %d", got.ChildIndex, note.ChildIndex)
	}
	if got.NodeType != note.NodeType {
		t.Errorf("NodeType = %q, want %q", got.NodeType, note.NodeType)
	}
}

func TestGetNoteNotFound(t *testing.T) {
	gitDir := initTestRepo(t)
	sha := headSHA(t, gitDir)
	store := NewNotesStore(gitDir)

	got, err := store.GetNote(sha)
	if err != nil {
		t.Fatalf("GetNote: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for non-existent note, got %+v", got)
	}
}

func TestOverwriteNote(t *testing.T) {
	gitDir := initTestRepo(t)
	sha := headSHA(t, gitDir)
	store := NewNotesStore(gitDir)

	first := &NoteData{
		PNode:    "02" + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		TxID:     "1111111111111111111111111111111111111111111111111111111111111111",
		NodeType: "blob",
		Access:   0,
	}
	if err := store.SetNote(sha, first); err != nil {
		t.Fatalf("SetNote (first): %v", err)
	}

	second := &NoteData{
		PNode:    "03" + "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		TxID:     "2222222222222222222222222222222222222222222222222222222222222222",
		NodeType: "tree",
		Access:   2,
	}
	if err := store.SetNote(sha, second); err != nil {
		t.Fatalf("SetNote (second): %v", err)
	}

	got, err := store.GetNote(sha)
	if err != nil {
		t.Fatalf("GetNote: %v", err)
	}
	if got == nil {
		t.Fatal("GetNote returned nil after overwrite")
	}
	if got.PNode != second.PNode {
		t.Errorf("PNode = %q, want %q (should be overwritten)", got.PNode, second.PNode)
	}
	if got.TxID != second.TxID {
		t.Errorf("TxID = %q, want %q", got.TxID, second.TxID)
	}
	if got.NodeType != second.NodeType {
		t.Errorf("NodeType = %q, want %q", got.NodeType, second.NodeType)
	}
}

func TestRemoveNote(t *testing.T) {
	gitDir := initTestRepo(t)
	sha := headSHA(t, gitDir)
	store := NewNotesStore(gitDir)

	note := &NoteData{
		PNode:    "02" + "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		TxID:     "3333333333333333333333333333333333333333333333333333333333333333",
		NodeType: "blob",
	}
	if err := store.SetNote(sha, note); err != nil {
		t.Fatalf("SetNote: %v", err)
	}

	// Verify it exists.
	got, err := store.GetNote(sha)
	if err != nil {
		t.Fatalf("GetNote before remove: %v", err)
	}
	if got == nil {
		t.Fatal("expected note to exist before remove")
	}

	// Remove it.
	if err := store.RemoveNote(sha); err != nil {
		t.Fatalf("RemoveNote: %v", err)
	}

	// Verify it's gone.
	got, err = store.GetNote(sha)
	if err != nil {
		t.Fatalf("GetNote after remove: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil after remove, got %+v", got)
	}
}

func TestRemoveNoteIdempotent(t *testing.T) {
	gitDir := initTestRepo(t)
	sha := headSHA(t, gitDir)
	store := NewNotesStore(gitDir)

	// Removing a note that doesn't exist should not error.
	if err := store.RemoveNote(sha); err != nil {
		t.Fatalf("RemoveNote on non-existent: %v", err)
	}
}

func TestListNotes(t *testing.T) {
	gitDir := initTestRepo(t)
	store := NewNotesStore(gitDir)

	// Create multiple commits for multiple objects.
	dir := filepath.Dir(gitDir)
	run(t, dir, "git", "commit", "--allow-empty", "-m", "second")
	run(t, dir, "git", "commit", "--allow-empty", "-m", "third")

	// Get all commit SHAs.
	cmd := exec.Command("git", "log", "--format=%H")
	cmd.Env = append(os.Environ(), "GIT_DIR="+gitDir)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git log: %v", err)
	}

	lines := splitNonEmpty(string(out))
	if len(lines) < 3 {
		t.Fatalf("expected >= 3 commits, got %d", len(lines))
	}

	// Add notes to first two commits.
	for i, sha := range lines[:2] {
		note := &NoteData{
			PNode:      "02" + "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"[:64],
			NodeType:   "commit",
			ChildIndex: i,
		}
		if err := store.SetNote(sha, note); err != nil {
			t.Fatalf("SetNote(%s): %v", sha, err)
		}
	}

	listed, err := store.ListNotes()
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}

	if len(listed) != 2 {
		t.Fatalf("ListNotes returned %d entries, want 2", len(listed))
	}

	// Both noted SHAs should be in the list.
	listedSet := make(map[string]bool)
	for _, s := range listed {
		listedSet[s] = true
	}
	for _, sha := range lines[:2] {
		if !listedSet[sha] {
			t.Errorf("ListNotes missing SHA %s", sha)
		}
	}
}

func TestListNotesEmpty(t *testing.T) {
	gitDir := initTestRepo(t)
	store := NewNotesStore(gitDir)

	listed, err := store.ListNotes()
	if err != nil {
		t.Fatalf("ListNotes: %v", err)
	}
	if len(listed) != 0 {
		t.Fatalf("expected empty list, got %d entries", len(listed))
	}
}

func TestNoteDataJSON(t *testing.T) {
	// Verify JSON roundtrip preserves all fields.
	original := &NoteData{
		PNode:      "02abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
		TxID:       "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210",
		KeyHash:    "deadbeef" + "deadbeef" + "deadbeef" + "deadbeef" + "deadbeef" + "deadbeef" + "deadbeef" + "deadbeef",
		Access:     2,
		ChildIndex: 42,
		NodeType:   "blob",
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded NoteData
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.PNode != original.PNode {
		t.Errorf("PNode mismatch")
	}
	if decoded.TxID != original.TxID {
		t.Errorf("TxID mismatch")
	}
	if decoded.KeyHash != original.KeyHash {
		t.Errorf("KeyHash mismatch")
	}
	if decoded.Access != original.Access {
		t.Errorf("Access mismatch")
	}
	if decoded.ChildIndex != original.ChildIndex {
		t.Errorf("ChildIndex mismatch")
	}
	if decoded.NodeType != original.NodeType {
		t.Errorf("NodeType mismatch")
	}
}

func TestNoteDataJSONOmitEmpty(t *testing.T) {
	// Empty NoteData should produce minimal JSON.
	empty := &NoteData{}
	data, err := json.Marshal(empty)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	// Should be just "{}".
	if string(data) != "{}" {
		t.Errorf("empty NoteData JSON = %s, want {}", string(data))
	}
}

func TestNoteDataJSONAnchorFields(t *testing.T) {
	// Verify anchor fields roundtrip correctly.
	anchor := &NoteData{
		NodeType:   "commit",
		AnchorTxID: "aaaa" + "bbbb" + "cccc" + "dddd" + "eeee" + "ffff" + "0000" + "1111" + "2222" + "3333" + "4444" + "5555" + "6666" + "7777" + "8888" + "9999",
		TreePNode:  "03" + "1111111111111111111111111111111111111111111111111111111111111111",
		TreeTxID:   "ffff" + "0000" + "1111" + "2222" + "3333" + "4444" + "5555" + "6666" + "7777" + "8888" + "9999" + "aaaa" + "bbbb" + "cccc" + "dddd" + "eeee",
	}

	data, err := json.Marshal(anchor)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded NoteData
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.AnchorTxID != anchor.AnchorTxID {
		t.Errorf("AnchorTxID mismatch")
	}
	if decoded.TreePNode != anchor.TreePNode {
		t.Errorf("TreePNode mismatch")
	}
	if decoded.TreeTxID != anchor.TreeTxID {
		t.Errorf("TreeTxID mismatch")
	}
}

func TestSetNoteWithAnchorFields(t *testing.T) {
	gitDir := initTestRepo(t)
	sha := headSHA(t, gitDir)
	store := NewNotesStore(gitDir)

	note := &NoteData{
		PNode:      "02" + "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2",
		TxID:       "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
		NodeType:   "commit",
		AnchorTxID: "1111111111111111111111111111111111111111111111111111111111111111",
		TreePNode:  "03" + "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		TreeTxID:   "2222222222222222222222222222222222222222222222222222222222222222",
	}

	if err := store.SetNote(sha, note); err != nil {
		t.Fatalf("SetNote: %v", err)
	}

	got, err := store.GetNote(sha)
	if err != nil {
		t.Fatalf("GetNote: %v", err)
	}
	if got == nil {
		t.Fatal("GetNote returned nil")
	}

	if got.AnchorTxID != note.AnchorTxID {
		t.Errorf("AnchorTxID = %q, want %q", got.AnchorTxID, note.AnchorTxID)
	}
	if got.TreePNode != note.TreePNode {
		t.Errorf("TreePNode = %q, want %q", got.TreePNode, note.TreePNode)
	}
	if got.TreeTxID != note.TreeTxID {
		t.Errorf("TreeTxID = %q, want %q", got.TreeTxID, note.TreeTxID)
	}
}

// splitNonEmpty splits a string by newlines and returns non-empty trimmed lines.
func splitNonEmpty(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var result []string
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			result = append(result, line)
		}
	}
	return result
}
