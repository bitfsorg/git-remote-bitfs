package utxo

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestNewStore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	s := NewStore(path)
	if err := s.Load(); err != nil {
		t.Fatalf("Load() on new store: %v", err)
	}

	st := s.State()
	if len(st.FeeUTXOs) != 0 {
		t.Errorf("expected empty FeeUTXOs, got %d", len(st.FeeUTXOs))
	}
	if len(st.NodeUTXOs) != 0 {
		t.Errorf("expected empty NodeUTXOs, got %d", len(st.NodeUTXOs))
	}
	if len(st.RefUTXOs) != 0 {
		t.Errorf("expected empty RefUTXOs, got %d", len(st.RefUTXOs))
	}
}

func TestSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	// Create and populate store.
	s1 := NewStore(path)
	s1.AddFeeUTXO(FeeUTXO{
		TxID:   "aaaa",
		Vout:   0,
		Amount: 1000,
	})
	s1.SetNodeUTXO(NodeUTXO{
		TxID:  "bbbb",
		Vout:  1,
		PNode: "02abcdef",
	})
	s1.SetRefUTXO(RefUTXO{
		TxID: "cccc",
		Vout: 0,
		Ref:  "refs/heads/main",
	})

	if err := s1.Save(); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	// Load into a new store.
	s2 := NewStore(path)
	if err := s2.Load(); err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	st := s2.State()
	if len(st.FeeUTXOs) != 1 {
		t.Fatalf("expected 1 FeeUTXO, got %d", len(st.FeeUTXOs))
	}
	if st.FeeUTXOs[0].TxID != "aaaa" {
		t.Errorf("FeeUTXO TxID = %q, want %q", st.FeeUTXOs[0].TxID, "aaaa")
	}
	if st.FeeUTXOs[0].Amount != 1000 {
		t.Errorf("FeeUTXO Amount = %d, want 1000", st.FeeUTXOs[0].Amount)
	}

	if len(st.NodeUTXOs) != 1 {
		t.Fatalf("expected 1 NodeUTXO, got %d", len(st.NodeUTXOs))
	}
	if st.NodeUTXOs[0].PNode != "02abcdef" {
		t.Errorf("NodeUTXO PNode = %q, want %q", st.NodeUTXOs[0].PNode, "02abcdef")
	}

	if len(st.RefUTXOs) != 1 {
		t.Fatalf("expected 1 RefUTXO, got %d", len(st.RefUTXOs))
	}
	if st.RefUTXOs[0].Ref != "refs/heads/main" {
		t.Errorf("RefUTXO Ref = %q, want %q", st.RefUTXOs[0].Ref, "refs/heads/main")
	}
}

func TestAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	s := NewStore(path)
	s.AddFeeUTXO(FeeUTXO{TxID: "tx1", Amount: 500})

	if err := s.Save(); err != nil {
		t.Fatalf("Save() error: %v", err)
	}

	// Verify the file exists and is valid JSON.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading saved file: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("saved file is empty")
	}

	// No temp files should remain.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

func TestAllocateFeeUTXO(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "state.json"))

	s.AddFeeUTXO(FeeUTXO{TxID: "small", Amount: 100})
	s.AddFeeUTXO(FeeUTXO{TxID: "medium", Amount: 500})
	s.AddFeeUTXO(FeeUTXO{TxID: "large", Amount: 1000})

	// Allocate with min 400 — should pick "medium" (smallest sufficient).
	got := s.AllocateFeeUTXO(400)
	if got == nil {
		t.Fatal("AllocateFeeUTXO(400) returned nil")
	}
	if got.TxID != "medium" {
		t.Errorf("allocated TxID = %q, want %q", got.TxID, "medium")
	}

	// After allocation, should have 2 remaining.
	st := s.State()
	if len(st.FeeUTXOs) != 2 {
		t.Errorf("remaining FeeUTXOs = %d, want 2", len(st.FeeUTXOs))
	}

	// Allocate with min 2000 — nothing sufficient.
	got = s.AllocateFeeUTXO(2000)
	if got != nil {
		t.Errorf("AllocateFeeUTXO(2000) expected nil, got %+v", got)
	}
}

func TestAllocateFeeUTXOEmpty(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "state.json"))

	got := s.AllocateFeeUTXO(100)
	if got != nil {
		t.Errorf("AllocateFeeUTXO on empty store expected nil, got %+v", got)
	}
}

func TestSetAndGetNodeUTXO(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "state.json"))

	node := NodeUTXO{
		TxID:         "tx1",
		Vout:         0,
		Amount:       546,
		PNode:        "02aabbccdd",
		ScriptPubKey: "76a914...",
	}
	s.SetNodeUTXO(node)

	got := s.GetNodeUTXO("02aabbccdd")
	if got == nil {
		t.Fatal("GetNodeUTXO returned nil")
	}
	if got.TxID != "tx1" {
		t.Errorf("TxID = %q, want %q", got.TxID, "tx1")
	}
	if got.Amount != 546 {
		t.Errorf("Amount = %d, want 546", got.Amount)
	}

	// Non-existent pnode.
	if s.GetNodeUTXO("nonexistent") != nil {
		t.Error("expected nil for nonexistent pnode")
	}
}

func TestNodeUTXOOverwrite(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "state.json"))

	s.SetNodeUTXO(NodeUTXO{TxID: "tx1", PNode: "02aabb", Amount: 546})
	s.SetNodeUTXO(NodeUTXO{TxID: "tx2", PNode: "02aabb", Amount: 1000})

	got := s.GetNodeUTXO("02aabb")
	if got == nil {
		t.Fatal("GetNodeUTXO returned nil")
	}
	if got.TxID != "tx2" {
		t.Errorf("TxID = %q, want %q (overwrite)", got.TxID, "tx2")
	}
	if got.Amount != 1000 {
		t.Errorf("Amount = %d, want 1000", got.Amount)
	}

	// Should still be only 1 entry.
	st := s.State()
	if len(st.NodeUTXOs) != 1 {
		t.Errorf("NodeUTXOs count = %d, want 1", len(st.NodeUTXOs))
	}
}

func TestRemoveNodeUTXO(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "state.json"))

	s.SetNodeUTXO(NodeUTXO{TxID: "tx1", PNode: "02aabb"})
	s.RemoveNodeUTXO("02aabb")

	if got := s.GetNodeUTXO("02aabb"); got != nil {
		t.Errorf("expected nil after remove, got %+v", got)
	}

	// Remove non-existent — should not panic.
	s.RemoveNodeUTXO("nonexistent")
}

func TestSetAndGetRefUTXO(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "state.json"))

	ref := RefUTXO{
		TxID:       "tx1",
		Vout:       0,
		Amount:     546,
		Ref:        "refs/heads/main",
		PNode:      "02aabb",
		AnchorTxID: "anchor1",
	}
	s.SetRefUTXO(ref)

	got := s.GetRefUTXO("refs/heads/main")
	if got == nil {
		t.Fatal("GetRefUTXO returned nil")
	}
	if got.TxID != "tx1" {
		t.Errorf("TxID = %q, want %q", got.TxID, "tx1")
	}
	if got.AnchorTxID != "anchor1" {
		t.Errorf("AnchorTxID = %q, want %q", got.AnchorTxID, "anchor1")
	}

	// Non-existent ref.
	if s.GetRefUTXO("refs/heads/nonexistent") != nil {
		t.Error("expected nil for nonexistent ref")
	}
}

func TestRefUTXOOverwrite(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "state.json"))

	s.SetRefUTXO(RefUTXO{TxID: "tx1", Ref: "refs/heads/main", AnchorTxID: "anchor1"})
	s.SetRefUTXO(RefUTXO{TxID: "tx2", Ref: "refs/heads/main", AnchorTxID: "anchor2"})

	got := s.GetRefUTXO("refs/heads/main")
	if got == nil {
		t.Fatal("GetRefUTXO returned nil")
	}
	if got.TxID != "tx2" {
		t.Errorf("TxID = %q, want %q", got.TxID, "tx2")
	}
	if got.AnchorTxID != "anchor2" {
		t.Errorf("AnchorTxID = %q, want %q", got.AnchorTxID, "anchor2")
	}

	st := s.State()
	if len(st.RefUTXOs) != 1 {
		t.Errorf("RefUTXOs count = %d, want 1", len(st.RefUTXOs))
	}
}

func TestRemoveRefUTXO(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "state.json"))

	s.SetRefUTXO(RefUTXO{TxID: "tx1", Ref: "refs/heads/main"})
	s.RemoveRefUTXO("refs/heads/main")

	if got := s.GetRefUTXO("refs/heads/main"); got != nil {
		t.Errorf("expected nil after remove, got %+v", got)
	}

	// Remove non-existent — should not panic.
	s.RemoveRefUTXO("refs/heads/nonexistent")
}

func TestConcurrentAccess(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "state.json"))

	var wg sync.WaitGroup
	const n = 100

	// Concurrent fee UTXO additions.
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s.AddFeeUTXO(FeeUTXO{
				TxID:   "tx" + string(rune('A'+i%26)),
				Amount: uint64(i * 100),
			})
		}(i)
	}

	// Concurrent node UTXO sets.
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s.SetNodeUTXO(NodeUTXO{
				TxID:  "ntx",
				PNode: "pnode" + string(rune('A'+i%26)),
			})
		}(i)
	}

	// Concurrent ref UTXO sets.
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s.SetRefUTXO(RefUTXO{
				TxID: "rtx",
				Ref:  "refs/heads/" + string(rune('A'+i%26)),
			})
		}(i)
	}

	wg.Wait()

	// Just verify no panics or data races occurred.
	st := s.State()
	if len(st.FeeUTXOs) == 0 {
		t.Error("expected some FeeUTXOs after concurrent adds")
	}
	if len(st.NodeUTXOs) == 0 {
		t.Error("expected some NodeUTXOs after concurrent sets")
	}
	if len(st.RefUTXOs) == 0 {
		t.Error("expected some RefUTXOs after concurrent sets")
	}
}

func TestStateCopyIsolation(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(filepath.Join(dir, "state.json"))

	s.AddFeeUTXO(FeeUTXO{TxID: "tx1", Amount: 100})

	st := s.State()
	if len(st.FeeUTXOs) != 1 {
		t.Fatalf("expected 1 FeeUTXO, got %d", len(st.FeeUTXOs))
	}

	// Mutate the copy.
	st.FeeUTXOs[0].Amount = 999

	// Original should be unchanged.
	st2 := s.State()
	if st2.FeeUTXOs[0].Amount != 100 {
		t.Errorf("state mutation leaked: got Amount=%d, want 100", st2.FeeUTXOs[0].Amount)
	}
}

func TestLoadInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")

	// Write invalid JSON.
	if err := os.WriteFile(path, []byte("{invalid"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := NewStore(path)
	err := s.Load()
	if err == nil {
		t.Fatal("Load() with invalid JSON should return error")
	}
}

func TestSaveCreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "dir", "state.json")

	s := NewStore(path)
	s.AddFeeUTXO(FeeUTXO{TxID: "tx1", Amount: 100})

	if err := s.Save(); err != nil {
		t.Fatalf("Save() with missing parent dirs: %v", err)
	}

	// Verify file was created.
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("expected state file to exist after Save()")
	}
}
