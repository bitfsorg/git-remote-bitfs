package mapper

import (
	"path"
	"sort"
	"strings"
)

// PathEntry holds the mapping from a file path to its Metanet position.
type PathEntry struct {
	ChildIndex int    // index within parent directory
	PNode      string // hex-encoded P_node public key
	TxID       string // hex-encoded transaction ID
}

// PathIndex maps file paths to their child index and P_node.
// It provides an in-memory index used during push/fetch to track
// which files map to which child positions in the Metanet DAG.
type PathIndex struct {
	entries map[string]*PathEntry
}

// NewPathIndex creates an empty PathIndex.
func NewPathIndex() *PathIndex {
	return &PathIndex{
		entries: make(map[string]*PathEntry),
	}
}

// Set adds or updates the entry for a given path.
func (idx *PathIndex) Set(p string, entry *PathEntry) {
	idx.entries[cleanPath(p)] = entry
}

// Get retrieves the entry for a given path.
// Returns the entry and true if found, nil and false otherwise.
func (idx *PathIndex) Get(p string) (*PathEntry, bool) {
	e, ok := idx.entries[cleanPath(p)]
	return e, ok
}

// Delete removes the entry for a given path.
func (idx *PathIndex) Delete(p string) {
	delete(idx.entries, cleanPath(p))
}

// ListDir returns all entries whose path is a direct child of dirPath.
// Results are sorted by ChildIndex for deterministic ordering.
func (idx *PathIndex) ListDir(dirPath string) []*DirEntry {
	prefix := cleanPath(dirPath)
	if prefix != "" {
		prefix += "/"
	}

	var results []*DirEntry
	for p, entry := range idx.entries {
		if !strings.HasPrefix(p, prefix) {
			continue
		}
		// Only direct children: no further "/" after the prefix.
		rest := p[len(prefix):]
		if rest == "" || strings.Contains(rest, "/") {
			continue
		}
		results = append(results, &DirEntry{
			Name:  rest,
			Path:  p,
			Entry: entry,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Entry.ChildIndex < results[j].Entry.ChildIndex
	})
	return results
}

// Len returns the number of entries in the index.
func (idx *PathIndex) Len() int {
	return len(idx.entries)
}

// DirEntry is a directory listing entry returned by ListDir.
type DirEntry struct {
	Name  string     // file/directory name (basename)
	Path  string     // full path
	Entry *PathEntry // mapping data
}

// cleanPath normalizes a path by removing trailing slashes and cleaning it.
func cleanPath(p string) string {
	p = strings.TrimRight(p, "/")
	if p == "." || p == "" {
		return ""
	}
	return path.Clean(p)
}
