package utxo

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Store manages UTXO state with atomic file persistence.
// Uses temp file + rename pattern for crash safety.
type Store struct {
	path  string // file path (e.g. ~/.bitfs/utxos.json or .git/bitfs/state.json)
	state *State
	mu    sync.Mutex
}

// NewStore creates a new Store for the given file path.
func NewStore(path string) *Store {
	return &Store{
		path:  path,
		state: &State{},
	}
}

// Load reads state from disk. Returns empty state if file doesn't exist.
func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			s.state = &State{}
			return nil
		}
		return fmt.Errorf("reading utxo state: %w", err)
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("parsing utxo state: %w", err)
	}

	s.state = &state
	return nil
}

// Save atomically writes state to disk (write temp -> rename).
func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.MarshalIndent(s.state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling utxo state: %w", err)
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating state directory: %w", err)
	}

	// Write to temp file first for atomicity.
	tmp, err := os.CreateTemp(dir, ".utxo-state-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("closing temp file: %w", err)
	}

	// Atomic rename.
	if err := os.Rename(tmpName, s.path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("renaming temp file: %w", err)
	}

	return nil
}

// State returns a copy of the current state.
func (s *Store) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()

	st := State{}
	if s.state.FeeUTXOs != nil {
		st.FeeUTXOs = make([]FeeUTXO, len(s.state.FeeUTXOs))
		copy(st.FeeUTXOs, s.state.FeeUTXOs)
	}
	if s.state.NodeUTXOs != nil {
		st.NodeUTXOs = make([]NodeUTXO, len(s.state.NodeUTXOs))
		copy(st.NodeUTXOs, s.state.NodeUTXOs)
	}
	if s.state.RefUTXOs != nil {
		st.RefUTXOs = make([]RefUTXO, len(s.state.RefUTXOs))
		copy(st.RefUTXOs, s.state.RefUTXOs)
	}
	return st
}

// --- Fee UTXO operations ---

// AddFeeUTXO adds a fee UTXO to the pool.
func (s *Store) AddFeeUTXO(u FeeUTXO) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.FeeUTXOs = append(s.state.FeeUTXOs, u)
}

// AllocateFeeUTXO finds and removes a fee UTXO with sufficient amount.
// Returns nil if no suitable UTXO found. Prefers the smallest UTXO
// that satisfies the minimum amount to reduce fragmentation.
func (s *Store) AllocateFeeUTXO(minAmount uint64) *FeeUTXO {
	s.mu.Lock()
	defer s.mu.Unlock()

	bestIdx := -1
	var bestAmount uint64

	for i, u := range s.state.FeeUTXOs {
		if u.Amount >= minAmount {
			if bestIdx == -1 || u.Amount < bestAmount {
				bestIdx = i
				bestAmount = u.Amount
			}
		}
	}

	if bestIdx == -1 {
		return nil
	}

	result := s.state.FeeUTXOs[bestIdx]
	// Remove by swapping with last element.
	last := len(s.state.FeeUTXOs) - 1
	s.state.FeeUTXOs[bestIdx] = s.state.FeeUTXOs[last]
	s.state.FeeUTXOs = s.state.FeeUTXOs[:last]

	return &result
}

// --- Node UTXO operations ---

// SetNodeUTXO updates or adds a node UTXO (by PNode).
func (s *Store) SetNodeUTXO(u NodeUTXO) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, existing := range s.state.NodeUTXOs {
		if existing.PNode == u.PNode {
			s.state.NodeUTXOs[i] = u
			return
		}
	}
	s.state.NodeUTXOs = append(s.state.NodeUTXOs, u)
}

// GetNodeUTXO returns the UTXO for a given PNode. Returns nil if not found.
func (s *Store) GetNodeUTXO(pnode string) *NodeUTXO {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, u := range s.state.NodeUTXOs {
		if u.PNode == pnode {
			result := u
			return &result
		}
	}
	return nil
}

// RemoveNodeUTXO removes the UTXO for a given PNode.
func (s *Store) RemoveNodeUTXO(pnode string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, u := range s.state.NodeUTXOs {
		if u.PNode == pnode {
			last := len(s.state.NodeUTXOs) - 1
			s.state.NodeUTXOs[i] = s.state.NodeUTXOs[last]
			s.state.NodeUTXOs = s.state.NodeUTXOs[:last]
			return
		}
	}
}

// --- Ref UTXO operations ---

// SetRefUTXO updates or adds a ref UTXO (by Ref name).
func (s *Store) SetRefUTXO(u RefUTXO) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, existing := range s.state.RefUTXOs {
		if existing.Ref == u.Ref {
			s.state.RefUTXOs[i] = u
			return
		}
	}
	s.state.RefUTXOs = append(s.state.RefUTXOs, u)
}

// GetRefUTXO returns the UTXO for a given ref. Returns nil if not found.
func (s *Store) GetRefUTXO(ref string) *RefUTXO {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, u := range s.state.RefUTXOs {
		if u.Ref == ref {
			result := u
			return &result
		}
	}
	return nil
}

// RemoveRefUTXO removes the UTXO for a given ref.
func (s *Store) RemoveRefUTXO(ref string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, u := range s.state.RefUTXOs {
		if u.Ref == ref {
			last := len(s.state.RefUTXOs) - 1
			s.state.RefUTXOs[i] = s.state.RefUTXOs[last]
			s.state.RefUTXOs = s.state.RefUTXOs[:last]
			return
		}
	}
}
