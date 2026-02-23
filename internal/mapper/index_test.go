package mapper

import (
	"testing"
)

func TestPathIndexSetAndGet(t *testing.T) {
	idx := NewPathIndex()

	entry := &PathEntry{
		ChildIndex: 0,
		PNode:      "02abcdef",
		TxID:       "aabbccdd",
	}
	idx.Set("src/main.go", entry)

	got, ok := idx.Get("src/main.go")
	if !ok {
		t.Fatal("Get returned false for existing entry")
	}
	if got.ChildIndex != 0 {
		t.Errorf("ChildIndex = %d, want 0", got.ChildIndex)
	}
	if got.PNode != "02abcdef" {
		t.Errorf("PNode = %q, want %q", got.PNode, "02abcdef")
	}
	if got.TxID != "aabbccdd" {
		t.Errorf("TxID = %q, want %q", got.TxID, "aabbccdd")
	}
}

func TestPathIndexGetNotFound(t *testing.T) {
	idx := NewPathIndex()

	_, ok := idx.Get("nonexistent")
	if ok {
		t.Fatal("Get returned true for non-existent entry")
	}
}

func TestPathIndexOverwrite(t *testing.T) {
	idx := NewPathIndex()

	first := &PathEntry{ChildIndex: 0, PNode: "02aaa"}
	idx.Set("file.txt", first)

	second := &PathEntry{ChildIndex: 5, PNode: "03bbb"}
	idx.Set("file.txt", second)

	got, ok := idx.Get("file.txt")
	if !ok {
		t.Fatal("Get returned false")
	}
	if got.ChildIndex != 5 {
		t.Errorf("ChildIndex = %d, want 5 after overwrite", got.ChildIndex)
	}
	if got.PNode != "03bbb" {
		t.Errorf("PNode = %q, want %q", got.PNode, "03bbb")
	}
}

func TestPathIndexDelete(t *testing.T) {
	idx := NewPathIndex()

	idx.Set("file.txt", &PathEntry{ChildIndex: 0})
	idx.Delete("file.txt")

	_, ok := idx.Get("file.txt")
	if ok {
		t.Fatal("Get returned true after Delete")
	}
}

func TestPathIndexDeleteNonExistent(t *testing.T) {
	idx := NewPathIndex()
	// Deleting a non-existent entry should not panic.
	idx.Delete("nonexistent")
}

func TestPathIndexLen(t *testing.T) {
	idx := NewPathIndex()

	if idx.Len() != 0 {
		t.Fatalf("Len = %d, want 0", idx.Len())
	}

	idx.Set("a", &PathEntry{ChildIndex: 0})
	idx.Set("b", &PathEntry{ChildIndex: 1})
	idx.Set("c", &PathEntry{ChildIndex: 2})

	if idx.Len() != 3 {
		t.Fatalf("Len = %d, want 3", idx.Len())
	}

	idx.Delete("b")
	if idx.Len() != 2 {
		t.Fatalf("Len = %d, want 2 after delete", idx.Len())
	}
}

func TestPathIndexListDir(t *testing.T) {
	idx := NewPathIndex()

	// Set up a directory structure:
	// src/
	//   main.go (index 0)
	//   util.go (index 1)
	// src/sub/
	//   deep.go (index 0)
	// README.md (index 0)
	idx.Set("src/main.go", &PathEntry{ChildIndex: 1, PNode: "02aaa"})
	idx.Set("src/util.go", &PathEntry{ChildIndex: 0, PNode: "02bbb"})
	idx.Set("src/sub/deep.go", &PathEntry{ChildIndex: 0, PNode: "02ccc"})
	idx.Set("README.md", &PathEntry{ChildIndex: 0, PNode: "02ddd"})

	// List "src" directory — should return main.go and util.go (direct children only).
	entries := idx.ListDir("src")
	if len(entries) != 2 {
		t.Fatalf("ListDir(src) returned %d entries, want 2", len(entries))
	}

	// Should be sorted by ChildIndex.
	if entries[0].Name != "util.go" {
		t.Errorf("entries[0].Name = %q, want %q (index 0)", entries[0].Name, "util.go")
	}
	if entries[1].Name != "main.go" {
		t.Errorf("entries[1].Name = %q, want %q (index 1)", entries[1].Name, "main.go")
	}
}

func TestPathIndexListDirRoot(t *testing.T) {
	idx := NewPathIndex()

	idx.Set("README.md", &PathEntry{ChildIndex: 0})
	idx.Set("go.mod", &PathEntry{ChildIndex: 1})
	idx.Set("src/main.go", &PathEntry{ChildIndex: 0})

	// List root directory.
	entries := idx.ListDir("")
	if len(entries) != 2 {
		t.Fatalf("ListDir('') returned %d entries, want 2", len(entries))
	}
	if entries[0].Name != "README.md" {
		t.Errorf("entries[0].Name = %q, want %q", entries[0].Name, "README.md")
	}
	if entries[1].Name != "go.mod" {
		t.Errorf("entries[1].Name = %q, want %q", entries[1].Name, "go.mod")
	}
}

func TestPathIndexListDirEmpty(t *testing.T) {
	idx := NewPathIndex()

	entries := idx.ListDir("nonexistent")
	if len(entries) != 0 {
		t.Fatalf("ListDir(nonexistent) returned %d entries, want 0", len(entries))
	}
}

func TestPathIndexListDirSubdirectory(t *testing.T) {
	idx := NewPathIndex()

	idx.Set("a/b/c.txt", &PathEntry{ChildIndex: 0, PNode: "02aaa"})
	idx.Set("a/b/d.txt", &PathEntry{ChildIndex: 1, PNode: "02bbb"})
	idx.Set("a/e.txt", &PathEntry{ChildIndex: 0, PNode: "02ccc"})

	entries := idx.ListDir("a/b")
	if len(entries) != 2 {
		t.Fatalf("ListDir(a/b) returned %d entries, want 2", len(entries))
	}
	if entries[0].Name != "c.txt" {
		t.Errorf("entries[0].Name = %q, want %q", entries[0].Name, "c.txt")
	}
	if entries[1].Name != "d.txt" {
		t.Errorf("entries[1].Name = %q, want %q", entries[1].Name, "d.txt")
	}
}

func TestPathIndexTrailingSlash(t *testing.T) {
	idx := NewPathIndex()

	// Trailing slashes should be normalized.
	idx.Set("src/main.go/", &PathEntry{ChildIndex: 0, PNode: "02aaa"})

	got, ok := idx.Get("src/main.go")
	if !ok {
		t.Fatal("Get without trailing slash returned false")
	}
	if got.PNode != "02aaa" {
		t.Errorf("PNode = %q, want %q", got.PNode, "02aaa")
	}

	// Also via trailing slash.
	got, ok = idx.Get("src/main.go/")
	if !ok {
		t.Fatal("Get with trailing slash returned false")
	}
	if got.PNode != "02aaa" {
		t.Errorf("PNode = %q, want %q", got.PNode, "02aaa")
	}
}

func TestPathIndexDirEntryPath(t *testing.T) {
	idx := NewPathIndex()

	idx.Set("src/main.go", &PathEntry{ChildIndex: 0, PNode: "02aaa"})

	entries := idx.ListDir("src")
	if len(entries) != 1 {
		t.Fatalf("ListDir(src) returned %d entries, want 1", len(entries))
	}
	if entries[0].Path != "src/main.go" {
		t.Errorf("Path = %q, want %q", entries[0].Path, "src/main.go")
	}
}
