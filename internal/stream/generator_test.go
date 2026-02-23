package stream

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestGenerator_WriteBlob(t *testing.T) {
	var buf bytes.Buffer
	g := NewGenerator(&buf)

	err := g.WriteBlob(":1", []byte("hello"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "blob\nmark :1\ndata 5\nhello\n"
	if buf.String() != want {
		t.Errorf("output = %q, want %q", buf.String(), want)
	}
}

func TestGenerator_WriteBlobWithoutMark(t *testing.T) {
	var buf bytes.Buffer
	g := NewGenerator(&buf)

	err := g.WriteBlob("", []byte("abc"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "blob\ndata 3\nabc\n"
	if buf.String() != want {
		t.Errorf("output = %q, want %q", buf.String(), want)
	}
}

func TestGenerator_WriteCommit(t *testing.T) {
	var buf bytes.Buffer
	g := NewGenerator(&buf)

	c := &Commit{
		Ref:       "refs/heads/main",
		Mark:      ":2",
		Author:    "Test User <test@example.com> 1234567890 +0000",
		Committer: "Test User <test@example.com> 1234567890 +0000",
		Message:   "first commit",
		FileOps: []FileOp{
			{Op: FileModify, Mode: 0100644, DataRef: ":1", Path: "file.txt"},
			{Op: FileDelete, Path: "old.txt"},
		},
	}

	err := g.WriteCommit(c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()

	// Verify the output contains expected lines.
	expectedLines := []string{
		"commit refs/heads/main",
		"mark :2",
		"author Test User <test@example.com> 1234567890 +0000",
		"committer Test User <test@example.com> 1234567890 +0000",
		"data 12",
		"first commit",
		"M 100644 :1 file.txt",
		"D old.txt",
	}

	for _, line := range expectedLines {
		if !strings.Contains(output, line) {
			t.Errorf("output missing line %q\nfull output:\n%s", line, output)
		}
	}
}

func TestGenerator_WriteCommitWithMerge(t *testing.T) {
	var buf bytes.Buffer
	g := NewGenerator(&buf)

	c := &Commit{
		Ref:       "refs/heads/main",
		Mark:      ":5",
		Committer: "A <a@b.com> 100 +0",
		Message:   "merge",
		From:      ":3",
		Merges:    []string{":4"},
	}

	err := g.WriteCommit(c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "from :3\n") {
		t.Errorf("missing from line")
	}
	if !strings.Contains(output, "merge :4\n") {
		t.Errorf("missing merge line")
	}
}

func TestGenerator_WriteReset(t *testing.T) {
	var buf bytes.Buffer
	g := NewGenerator(&buf)

	err := g.WriteReset("refs/heads/main", ":2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "reset refs/heads/main\nfrom :2\n\n"
	if buf.String() != want {
		t.Errorf("output = %q, want %q", buf.String(), want)
	}
}

func TestGenerator_WriteResetNoFrom(t *testing.T) {
	var buf bytes.Buffer
	g := NewGenerator(&buf)

	err := g.WriteReset("refs/heads/dev", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "reset refs/heads/dev\n\n"
	if buf.String() != want {
		t.Errorf("output = %q, want %q", buf.String(), want)
	}
}

func TestGenerator_WriteDone(t *testing.T) {
	var buf bytes.Buffer
	g := NewGenerator(&buf)

	err := g.WriteDone()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if buf.String() != "done\n" {
		t.Errorf("output = %q, want %q", buf.String(), "done\n")
	}
}

func TestGenerator_WriteInlineData(t *testing.T) {
	var buf bytes.Buffer
	g := NewGenerator(&buf)

	c := &Commit{
		Ref:       "refs/heads/main",
		Mark:      ":1",
		Committer: "A <a@b.com> 1 +0",
		Message:   "inline",
		FileOps: []FileOp{
			{
				Op:      FileModify,
				Mode:    0100644,
				DataRef: "inline",
				Path:    "readme.md",
				Data:    []byte("hello world"),
			},
		},
	}

	err := g.WriteCommit(c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "M 100644 inline readme.md\n") {
		t.Errorf("missing inline M line in output:\n%s", output)
	}
	if !strings.Contains(output, "data 11\nhello world\n") {
		t.Errorf("missing inline data in output:\n%s", output)
	}
}

func TestGenerator_QuotedPath(t *testing.T) {
	var buf bytes.Buffer
	g := NewGenerator(&buf)

	c := &Commit{
		Ref:       "refs/heads/main",
		Committer: "A <a@b.com> 1 +0",
		Message:   "x",
		FileOps: []FileOp{
			{Op: FileModify, Mode: 0100644, DataRef: ":1", Path: "path\nwith\nnewlines.txt"},
		},
	}

	err := g.WriteCommit(c)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, `"path\nwith\nnewlines.txt"`) {
		t.Errorf("path not properly quoted in output:\n%s", output)
	}
}

// TestRoundtrip_BlobParseGenerate generates a blob, then parses it back
// and verifies the data survives the roundtrip.
func TestRoundtrip_BlobParseGenerate(t *testing.T) {
	// Generate.
	var buf bytes.Buffer
	g := NewGenerator(&buf)
	original := []byte("hello world\x00binary\ndata")
	if err := g.WriteBlob(":1", original); err != nil {
		t.Fatalf("generate error: %v", err)
	}

	// Parse back.
	p := NewParser(&buf)
	cmd, err := p.Next()
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if cmd.Type != CmdBlob {
		t.Fatalf("expected CmdBlob")
	}
	if cmd.Blob.Mark != ":1" {
		t.Errorf("mark = %q", cmd.Blob.Mark)
	}
	if !bytes.Equal(cmd.Blob.Data, original) {
		t.Errorf("data mismatch:\n  got  %q\n  want %q", cmd.Blob.Data, original)
	}
}

// TestRoundtrip_CommitParseGenerate generates a commit, parses it back.
func TestRoundtrip_CommitParseGenerate(t *testing.T) {
	original := &Commit{
		Ref:       "refs/heads/main",
		Mark:      ":5",
		Author:    "Alice <alice@example.com> 1000 +0000",
		Committer: "Bob <bob@example.com> 2000 +0100",
		Message:   "initial commit\nwith multiple lines",
		From:      ":3",
		Merges:    []string{":4"},
		FileOps: []FileOp{
			{Op: FileModify, Mode: 0100644, DataRef: ":1", Path: "src/main.go"},
			{Op: FileModify, Mode: 0100755, DataRef: ":2", Path: "build.sh"},
			{Op: FileDelete, Path: "old/readme.txt"},
		},
	}

	// Generate.
	var buf bytes.Buffer
	g := NewGenerator(&buf)
	if err := g.WriteCommit(original); err != nil {
		t.Fatalf("generate error: %v", err)
	}

	// Parse back.
	p := NewParser(&buf)
	cmd, err := p.Next()
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if cmd.Type != CmdCommit {
		t.Fatalf("expected CmdCommit")
	}

	c := cmd.Commit
	if c.Ref != original.Ref {
		t.Errorf("ref = %q, want %q", c.Ref, original.Ref)
	}
	if c.Mark != original.Mark {
		t.Errorf("mark = %q, want %q", c.Mark, original.Mark)
	}
	if c.Author != original.Author {
		t.Errorf("author = %q, want %q", c.Author, original.Author)
	}
	if c.Committer != original.Committer {
		t.Errorf("committer = %q, want %q", c.Committer, original.Committer)
	}
	if c.Message != original.Message {
		t.Errorf("message = %q, want %q", c.Message, original.Message)
	}
	if c.From != original.From {
		t.Errorf("from = %q, want %q", c.From, original.From)
	}
	if len(c.Merges) != len(original.Merges) {
		t.Fatalf("merges count = %d, want %d", len(c.Merges), len(original.Merges))
	}
	for i := range c.Merges {
		if c.Merges[i] != original.Merges[i] {
			t.Errorf("merge[%d] = %q, want %q", i, c.Merges[i], original.Merges[i])
		}
	}
	if len(c.FileOps) != len(original.FileOps) {
		t.Fatalf("fileops count = %d, want %d", len(c.FileOps), len(original.FileOps))
	}
	for i := range c.FileOps {
		if c.FileOps[i].Op != original.FileOps[i].Op {
			t.Errorf("fileop[%d] op = %d, want %d", i, c.FileOps[i].Op, original.FileOps[i].Op)
		}
		if c.FileOps[i].Path != original.FileOps[i].Path {
			t.Errorf("fileop[%d] path = %q, want %q", i, c.FileOps[i].Path, original.FileOps[i].Path)
		}
		if c.FileOps[i].Op == FileModify {
			if c.FileOps[i].Mode != original.FileOps[i].Mode {
				t.Errorf("fileop[%d] mode = %o, want %o", i, c.FileOps[i].Mode, original.FileOps[i].Mode)
			}
			if c.FileOps[i].DataRef != original.FileOps[i].DataRef {
				t.Errorf("fileop[%d] dataref = %q, want %q", i, c.FileOps[i].DataRef, original.FileOps[i].DataRef)
			}
		}
	}
}

// TestRoundtrip_ResetParseGenerate generates a reset, parses it back.
func TestRoundtrip_ResetParseGenerate(t *testing.T) {
	var buf bytes.Buffer
	g := NewGenerator(&buf)
	if err := g.WriteReset("refs/heads/main", ":10"); err != nil {
		t.Fatalf("generate error: %v", err)
	}

	p := NewParser(&buf)
	cmd, err := p.Next()
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if cmd.Type != CmdReset {
		t.Fatalf("expected CmdReset")
	}
	if cmd.Reset.Ref != "refs/heads/main" {
		t.Errorf("ref = %q", cmd.Reset.Ref)
	}
	if cmd.Reset.From != ":10" {
		t.Errorf("from = %q", cmd.Reset.From)
	}
}

// TestRoundtrip_FullStream generates a full stream (blobs + commit + reset + done),
// then parses it all back.
func TestRoundtrip_FullStream(t *testing.T) {
	var buf bytes.Buffer
	g := NewGenerator(&buf)

	// Write blobs.
	if err := g.WriteBlob(":1", []byte("file content A")); err != nil {
		t.Fatalf("blob 1 error: %v", err)
	}
	if err := g.WriteBlob(":2", []byte("file content B")); err != nil {
		t.Fatalf("blob 2 error: %v", err)
	}

	// Write commit.
	if err := g.WriteCommit(&Commit{
		Ref:       "refs/heads/main",
		Mark:      ":3",
		Committer: "Test <test@test.com> 1000 +0000",
		Message:   "initial",
		FileOps: []FileOp{
			{Op: FileModify, Mode: 0100644, DataRef: ":1", Path: "a.txt"},
			{Op: FileModify, Mode: 0100644, DataRef: ":2", Path: "b.txt"},
		},
	}); err != nil {
		t.Fatalf("commit error: %v", err)
	}

	// Write reset.
	if err := g.WriteReset("refs/heads/main", ":3"); err != nil {
		t.Fatalf("reset error: %v", err)
	}

	// Write done.
	if err := g.WriteDone(); err != nil {
		t.Fatalf("done error: %v", err)
	}

	// Parse back.
	p := NewParser(&buf)

	// Blob 1.
	cmd, err := p.Next()
	if err != nil {
		t.Fatalf("parse blob 1: %v", err)
	}
	if cmd.Type != CmdBlob || string(cmd.Blob.Data) != "file content A" {
		t.Errorf("blob 1 mismatch: type=%d data=%q", cmd.Type, string(cmd.Blob.Data))
	}

	// Blob 2.
	cmd, err = p.Next()
	if err != nil {
		t.Fatalf("parse blob 2: %v", err)
	}
	if cmd.Type != CmdBlob || string(cmd.Blob.Data) != "file content B" {
		t.Errorf("blob 2 mismatch")
	}

	// Commit.
	cmd, err = p.Next()
	if err != nil {
		t.Fatalf("parse commit: %v", err)
	}
	if cmd.Type != CmdCommit {
		t.Fatalf("expected commit")
	}
	if len(cmd.Commit.FileOps) != 2 {
		t.Errorf("expected 2 file ops, got %d", len(cmd.Commit.FileOps))
	}

	// Reset.
	cmd, err = p.Next()
	if err != nil {
		t.Fatalf("parse reset: %v", err)
	}
	if cmd.Type != CmdReset {
		t.Fatalf("expected reset")
	}

	// Done -> EOF.
	_, err = p.Next()
	if err != io.EOF {
		t.Errorf("expected EOF after done, got %v", err)
	}
}

func TestGenerator_FileModeFormatting(t *testing.T) {
	tests := []struct {
		name     string
		mode     uint32
		wantMode string
	}{
		{"regular", 0100644, "100644"},
		{"executable", 0100755, "100755"},
		{"symlink", 0120000, "120000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			g := NewGenerator(&buf)
			c := &Commit{
				Ref:       "refs/heads/main",
				Committer: "A <a@b.com> 1 +0",
				Message:   "x",
				FileOps: []FileOp{
					{Op: FileModify, Mode: tt.mode, DataRef: ":1", Path: "f"},
				},
			}
			if err := g.WriteCommit(c); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(buf.String(), "M "+tt.wantMode+" ") {
				t.Errorf("output missing mode %s:\n%s", tt.wantMode, buf.String())
			}
		})
	}
}

func TestGenerator_EmptyBlobData(t *testing.T) {
	var buf bytes.Buffer
	g := NewGenerator(&buf)

	if err := g.WriteBlob(":1", []byte{}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "blob\nmark :1\ndata 0\n\n"
	if buf.String() != want {
		t.Errorf("output = %q, want %q", buf.String(), want)
	}
}
