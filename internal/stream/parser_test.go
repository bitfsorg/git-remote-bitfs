package stream

import (
	"io"
	"strconv"
	"strings"
	"testing"
)

func TestParser_SingleBlob(t *testing.T) {
	input := "blob\nmark :1\ndata 5\nhello\n"
	p := NewParser(strings.NewReader(input))

	cmd, err := p.Next()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd.Type != CmdBlob {
		t.Fatalf("expected CmdBlob, got %d", cmd.Type)
	}
	if cmd.Blob.Mark != ":1" {
		t.Errorf("mark = %q, want %q", cmd.Blob.Mark, ":1")
	}
	if string(cmd.Blob.Data) != "hello" {
		t.Errorf("data = %q, want %q", string(cmd.Blob.Data), "hello")
	}

	// Should be EOF after.
	_, err = p.Next()
	if err != io.EOF {
		t.Errorf("expected io.EOF, got %v", err)
	}
}

func TestParser_BlobWithoutMark(t *testing.T) {
	input := "blob\ndata 3\nabc\n"
	p := NewParser(strings.NewReader(input))

	cmd, err := p.Next()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd.Blob.Mark != "" {
		t.Errorf("expected empty mark, got %q", cmd.Blob.Mark)
	}
	if string(cmd.Blob.Data) != "abc" {
		t.Errorf("data = %q, want %q", string(cmd.Blob.Data), "abc")
	}
}

func TestParser_CommitWithFileOps(t *testing.T) {
	input := strings.Join([]string{
		"blob",
		"mark :1",
		"data 5",
		"hello",
		"commit refs/heads/main",
		"mark :2",
		"author Test User <test@example.com> 1234567890 +0000",
		"committer Test User <test@example.com> 1234567890 +0000",
		"data 12",
		"first commit",
		"M 100644 :1 path/to/file.txt",
		"D deleted/file.txt",
		"",
	}, "\n")

	p := NewParser(strings.NewReader(input))

	// First command: blob.
	cmd, err := p.Next()
	if err != nil {
		t.Fatalf("unexpected error parsing blob: %v", err)
	}
	if cmd.Type != CmdBlob {
		t.Fatalf("expected CmdBlob, got %d", cmd.Type)
	}

	// Second command: commit.
	cmd, err = p.Next()
	if err != nil {
		t.Fatalf("unexpected error parsing commit: %v", err)
	}
	if cmd.Type != CmdCommit {
		t.Fatalf("expected CmdCommit, got %d", cmd.Type)
	}

	c := cmd.Commit
	if c.Mark != ":2" {
		t.Errorf("mark = %q, want %q", c.Mark, ":2")
	}
	if c.Ref != "refs/heads/main" {
		t.Errorf("ref = %q, want %q", c.Ref, "refs/heads/main")
	}
	if c.Author != "Test User <test@example.com> 1234567890 +0000" {
		t.Errorf("author = %q", c.Author)
	}
	if c.Committer != "Test User <test@example.com> 1234567890 +0000" {
		t.Errorf("committer = %q", c.Committer)
	}
	if c.Message != "first commit" {
		t.Errorf("message = %q, want %q", c.Message, "first commit")
	}
	if len(c.FileOps) != 2 {
		t.Fatalf("expected 2 file ops, got %d", len(c.FileOps))
	}

	// M op.
	op0 := c.FileOps[0]
	if op0.Op != FileModify {
		t.Errorf("op[0] type = %d, want FileModify", op0.Op)
	}
	if op0.Mode != 0100644 {
		t.Errorf("op[0] mode = %o, want 100644", op0.Mode)
	}
	if op0.DataRef != ":1" {
		t.Errorf("op[0] dataref = %q, want %q", op0.DataRef, ":1")
	}
	if op0.Path != "path/to/file.txt" {
		t.Errorf("op[0] path = %q, want %q", op0.Path, "path/to/file.txt")
	}

	// D op.
	op1 := c.FileOps[1]
	if op1.Op != FileDelete {
		t.Errorf("op[1] type = %d, want FileDelete", op1.Op)
	}
	if op1.Path != "deleted/file.txt" {
		t.Errorf("op[1] path = %q, want %q", op1.Path, "deleted/file.txt")
	}
}

func TestParser_MultipleBlobsAndCommit(t *testing.T) {
	input := strings.Join([]string{
		"blob",
		"mark :1",
		"data 3",
		"foo",
		"blob",
		"mark :2",
		"data 3",
		"bar",
		"commit refs/heads/main",
		"mark :3",
		"committer A <a@b.com> 1000 +0000",
		"data 4",
		"init",
		"M 100644 :1 a.txt",
		"M 100644 :2 b.txt",
		"",
	}, "\n")

	p := NewParser(strings.NewReader(input))

	// Blob 1.
	cmd, err := p.Next()
	if err != nil {
		t.Fatalf("blob 1 error: %v", err)
	}
	if cmd.Type != CmdBlob || cmd.Blob.Mark != ":1" {
		t.Fatalf("expected blob :1")
	}
	if string(cmd.Blob.Data) != "foo" {
		t.Errorf("blob 1 data = %q", string(cmd.Blob.Data))
	}

	// Blob 2.
	cmd, err = p.Next()
	if err != nil {
		t.Fatalf("blob 2 error: %v", err)
	}
	if cmd.Type != CmdBlob || cmd.Blob.Mark != ":2" {
		t.Fatalf("expected blob :2")
	}

	// Commit.
	cmd, err = p.Next()
	if err != nil {
		t.Fatalf("commit error: %v", err)
	}
	if cmd.Type != CmdCommit {
		t.Fatalf("expected commit")
	}
	if len(cmd.Commit.FileOps) != 2 {
		t.Errorf("expected 2 file ops, got %d", len(cmd.Commit.FileOps))
	}
}

func TestParser_CommitWithMergeParents(t *testing.T) {
	input := strings.Join([]string{
		"commit refs/heads/main",
		"mark :5",
		"committer Merger <m@x.com> 2000 +0000",
		"data 12",
		"merge commit",
		"from :3",
		"merge :4",
		"M 100644 :1 merged.txt",
		"",
	}, "\n")

	p := NewParser(strings.NewReader(input))
	cmd, err := p.Next()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	c := cmd.Commit
	if c.From != ":3" {
		t.Errorf("from = %q, want %q", c.From, ":3")
	}
	if len(c.Merges) != 1 || c.Merges[0] != ":4" {
		t.Errorf("merges = %v, want [:4]", c.Merges)
	}
}

func TestParser_Reset(t *testing.T) {
	input := "reset refs/heads/main\nfrom :2\n\n"
	p := NewParser(strings.NewReader(input))

	cmd, err := p.Next()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd.Type != CmdReset {
		t.Fatalf("expected CmdReset, got %d", cmd.Type)
	}
	if cmd.Reset.Ref != "refs/heads/main" {
		t.Errorf("ref = %q", cmd.Reset.Ref)
	}
	if cmd.Reset.From != ":2" {
		t.Errorf("from = %q", cmd.Reset.From)
	}
}

func TestParser_ResetWithoutFrom(t *testing.T) {
	input := "reset refs/heads/dev\n\n"
	p := NewParser(strings.NewReader(input))

	cmd, err := p.Next()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd.Type != CmdReset {
		t.Fatalf("expected CmdReset")
	}
	if cmd.Reset.From != "" {
		t.Errorf("expected empty from, got %q", cmd.Reset.From)
	}
}

func TestParser_Tag(t *testing.T) {
	input := strings.Join([]string{
		"tag v1.0",
		"from :2",
		"tagger Release Bot <bot@example.com> 1234567890 +0000",
		"data 7",
		"v1.0 rc",
		"",
	}, "\n")

	p := NewParser(strings.NewReader(input))
	cmd, err := p.Next()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd.Type != CmdTag {
		t.Fatalf("expected CmdTag, got %d", cmd.Type)
	}

	tag := cmd.Tag
	if tag.Name != "v1.0" {
		t.Errorf("name = %q", tag.Name)
	}
	if tag.From != ":2" {
		t.Errorf("from = %q", tag.From)
	}
	if tag.Tagger != "Release Bot <bot@example.com> 1234567890 +0000" {
		t.Errorf("tagger = %q", tag.Tagger)
	}
	if tag.Message != "v1.0 rc" {
		t.Errorf("message = %q, want %q", tag.Message, "v1.0 rc")
	}
}

func TestParser_FileModes(t *testing.T) {
	tests := []struct {
		name     string
		modeLine string
		wantMode uint32
	}{
		{"regular", "M 100644 :1 file.txt", 0100644},
		{"executable", "M 100755 :1 script.sh", 0100755},
		{"symlink", "M 120000 :1 link", 0120000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := "commit refs/heads/main\ncommitter A <a@b.com> 1 +0\ndata 1\nx\n" +
				tt.modeLine + "\n\n"

			p := NewParser(strings.NewReader(input))
			cmd, err := p.Next()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(cmd.Commit.FileOps) != 1 {
				t.Fatalf("expected 1 file op, got %d", len(cmd.Commit.FileOps))
			}
			if cmd.Commit.FileOps[0].Mode != tt.wantMode {
				t.Errorf("mode = %o, want %o", cmd.Commit.FileOps[0].Mode, tt.wantMode)
			}
		})
	}
}

func TestParser_BinaryDataInBlob(t *testing.T) {
	// Binary data: contains newlines, null bytes, high bytes.
	data := []byte{0x00, 0x01, '\n', 0xff, 0xfe, '\n', 0x00, 0x42}
	input := "blob\nmark :1\ndata 8\n" + string(data) + "\n"

	p := NewParser(strings.NewReader(input))
	cmd, err := p.Next()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd.Type != CmdBlob {
		t.Fatalf("expected CmdBlob")
	}
	if len(cmd.Blob.Data) != 8 {
		t.Fatalf("data length = %d, want 8", len(cmd.Blob.Data))
	}
	for i, b := range data {
		if cmd.Blob.Data[i] != b {
			t.Errorf("data[%d] = 0x%02x, want 0x%02x", i, cmd.Blob.Data[i], b)
		}
	}
}

func TestParser_EmptyCommit(t *testing.T) {
	input := strings.Join([]string{
		"commit refs/heads/main",
		"mark :1",
		"committer A <a@b.com> 1 +0",
		"data 5",
		"empty",
		"",
	}, "\n")

	p := NewParser(strings.NewReader(input))
	cmd, err := p.Next()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd.Type != CmdCommit {
		t.Fatalf("expected CmdCommit")
	}
	if len(cmd.Commit.FileOps) != 0 {
		t.Errorf("expected 0 file ops, got %d", len(cmd.Commit.FileOps))
	}
	if cmd.Commit.Message != "empty" {
		t.Errorf("message = %q, want %q", cmd.Commit.Message, "empty")
	}
}

func TestParser_SubmoduleSkipped(t *testing.T) {
	input := strings.Join([]string{
		"commit refs/heads/main",
		"committer A <a@b.com> 1 +0",
		"data 4",
		"test",
		"M 100644 :1 normal.txt",
		"M 160000 abc123 submod",
		"M 100755 :2 script.sh",
		"",
	}, "\n")

	p := NewParser(strings.NewReader(input))
	cmd, err := p.Next()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Submodule should be skipped, so only 2 file ops.
	if len(cmd.Commit.FileOps) != 2 {
		t.Fatalf("expected 2 file ops (submodule skipped), got %d", len(cmd.Commit.FileOps))
	}
	if cmd.Commit.FileOps[0].Path != "normal.txt" {
		t.Errorf("op[0] path = %q", cmd.Commit.FileOps[0].Path)
	}
	if cmd.Commit.FileOps[1].Path != "script.sh" {
		t.Errorf("op[1] path = %q", cmd.Commit.FileOps[1].Path)
	}
}

func TestParser_LargeDataBlock(t *testing.T) {
	// 100KB of data.
	size := 100 * 1024
	data := make([]byte, size)
	for i := range data {
		data[i] = byte(i % 256)
	}
	input := "blob\nmark :1\ndata " + strconv.Itoa(size) + "\n" + string(data) + "\n"

	p := NewParser(strings.NewReader(input))
	cmd, err := p.Next()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cmd.Blob.Data) != size {
		t.Errorf("data length = %d, want %d", len(cmd.Blob.Data), size)
	}
	// Spot check some bytes.
	for _, i := range []int{0, 1, 255, 256, 1000, size - 1} {
		want := byte(i % 256)
		if cmd.Blob.Data[i] != want {
			t.Errorf("data[%d] = 0x%02x, want 0x%02x", i, cmd.Blob.Data[i], want)
		}
	}
}

func TestParser_DoneCommand(t *testing.T) {
	input := "blob\nmark :1\ndata 2\nhi\ndone\n"
	p := NewParser(strings.NewReader(input))

	cmd, err := p.Next()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd.Type != CmdBlob {
		t.Fatalf("expected blob")
	}

	// "done" should cause EOF.
	_, err = p.Next()
	if err != io.EOF {
		t.Errorf("expected io.EOF after done, got %v", err)
	}
}

func TestParser_CommitWithFrom(t *testing.T) {
	input := strings.Join([]string{
		"commit refs/heads/main",
		"mark :3",
		"committer A <a@b.com> 2 +0",
		"data 6",
		"second",
		"from :2",
		"M 100644 :1 updated.txt",
		"",
	}, "\n")

	p := NewParser(strings.NewReader(input))
	cmd, err := p.Next()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd.Commit.From != ":2" {
		t.Errorf("from = %q, want %q", cmd.Commit.From, ":2")
	}
}

func TestParser_QuotedPath(t *testing.T) {
	// Git fast-export quotes paths with special characters.
	input := "commit refs/heads/main\ncommitter A <a@b.com> 1 +0\ndata 1\nx\n" +
		"M 100644 :1 \"path with\\nnewline.txt\"\n\n"

	p := NewParser(strings.NewReader(input))
	cmd, err := p.Next()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cmd.Commit.FileOps) != 1 {
		t.Fatalf("expected 1 file op")
	}
	if cmd.Commit.FileOps[0].Path != "path with\nnewline.txt" {
		t.Errorf("path = %q, want %q", cmd.Commit.FileOps[0].Path, "path with\nnewline.txt")
	}
}

func TestParser_InlineData(t *testing.T) {
	input := strings.Join([]string{
		"commit refs/heads/main",
		"committer A <a@b.com> 1 +0",
		"data 1",
		"x",
		"M 100644 inline readme.md",
		"data 11",
		"hello world",
		"",
	}, "\n")

	p := NewParser(strings.NewReader(input))
	cmd, err := p.Next()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cmd.Commit.FileOps) != 1 {
		t.Fatalf("expected 1 file op, got %d", len(cmd.Commit.FileOps))
	}
	op := cmd.Commit.FileOps[0]
	if op.DataRef != "inline" {
		t.Errorf("dataref = %q, want %q", op.DataRef, "inline")
	}
	if string(op.Data) != "hello world" {
		t.Errorf("data = %q, want %q", string(op.Data), "hello world")
	}
}

func TestParser_MultipleCommits(t *testing.T) {
	input := strings.Join([]string{
		"blob",
		"mark :1",
		"data 1",
		"a",
		"commit refs/heads/main",
		"mark :2",
		"committer A <a@b.com> 1 +0",
		"data 5",
		"first",
		"M 100644 :1 a.txt",
		"",
		"blob",
		"mark :3",
		"data 1",
		"b",
		"commit refs/heads/main",
		"mark :4",
		"committer A <a@b.com> 2 +0",
		"data 6",
		"second",
		"from :2",
		"M 100644 :3 b.txt",
		"",
	}, "\n")

	p := NewParser(strings.NewReader(input))

	types := []CommandType{CmdBlob, CmdCommit, CmdBlob, CmdCommit}
	for i, wantType := range types {
		cmd, err := p.Next()
		if err != nil {
			t.Fatalf("command %d: unexpected error: %v", i, err)
		}
		if cmd.Type != wantType {
			t.Errorf("command %d: type = %d, want %d", i, cmd.Type, wantType)
		}
	}

	_, err := p.Next()
	if err != io.EOF {
		t.Errorf("expected EOF, got %v", err)
	}
}

func TestParser_CommitMultipleMerges(t *testing.T) {
	input := strings.Join([]string{
		"commit refs/heads/main",
		"mark :10",
		"committer A <a@b.com> 5 +0",
		"data 13",
		"octopus merge",
		"from :3",
		"merge :5",
		"merge :7",
		"merge :9",
		"",
	}, "\n")

	p := NewParser(strings.NewReader(input))
	cmd, err := p.Next()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	c := cmd.Commit
	if c.From != ":3" {
		t.Errorf("from = %q", c.From)
	}
	if len(c.Merges) != 3 {
		t.Fatalf("expected 3 merges, got %d", len(c.Merges))
	}
	want := []string{":5", ":7", ":9"}
	for i, m := range c.Merges {
		if m != want[i] {
			t.Errorf("merge[%d] = %q, want %q", i, m, want[i])
		}
	}
}

func TestParser_EmptyData(t *testing.T) {
	input := "blob\nmark :1\ndata 0\n\n"
	p := NewParser(strings.NewReader(input))

	cmd, err := p.Next()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cmd.Blob.Data) != 0 {
		t.Errorf("expected empty data, got %d bytes", len(cmd.Blob.Data))
	}
}

func TestParser_CommitWithDeleteOnly(t *testing.T) {
	input := strings.Join([]string{
		"commit refs/heads/main",
		"mark :3",
		"committer A <a@b.com> 3 +0",
		"data 10",
		"delete all",
		"from :2",
		"D old/file.txt",
		"D another.txt",
		"",
	}, "\n")

	p := NewParser(strings.NewReader(input))
	cmd, err := p.Next()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cmd.Commit.FileOps) != 2 {
		t.Fatalf("expected 2 file ops, got %d", len(cmd.Commit.FileOps))
	}
	for _, op := range cmd.Commit.FileOps {
		if op.Op != FileDelete {
			t.Errorf("expected FileDelete, got %d", op.Op)
		}
	}
}

func TestParser_CommentsIgnored(t *testing.T) {
	input := "# this is a comment\nblob\nmark :1\ndata 2\nhi\n"
	p := NewParser(strings.NewReader(input))

	cmd, err := p.Next()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd.Type != CmdBlob {
		t.Fatalf("expected blob, got %d", cmd.Type)
	}
}

func TestParser_ResetFollowedByCommit(t *testing.T) {
	input := strings.Join([]string{
		"reset refs/heads/main",
		"from :2",
		"",
		"commit refs/heads/main",
		"mark :3",
		"committer A <a@b.com> 1 +0",
		"data 1",
		"x",
		"",
	}, "\n")

	p := NewParser(strings.NewReader(input))

	cmd, err := p.Next()
	if err != nil {
		t.Fatalf("reset error: %v", err)
	}
	if cmd.Type != CmdReset {
		t.Fatalf("expected CmdReset")
	}

	cmd, err = p.Next()
	if err != nil {
		t.Fatalf("commit error: %v", err)
	}
	if cmd.Type != CmdCommit {
		t.Fatalf("expected CmdCommit")
	}
}

func TestParser_MultilineCommitMessage(t *testing.T) {
	msg := "line one\nline two\nline three"
	input := "commit refs/heads/main\ncommitter A <a@b.com> 1 +0\ndata 28\n" + msg + "\n\n"

	p := NewParser(strings.NewReader(input))
	cmd, err := p.Next()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cmd.Commit.Message != msg {
		t.Errorf("message = %q, want %q", cmd.Commit.Message, msg)
	}
}

func TestParser_TagFollowedByReset(t *testing.T) {
	input := strings.Join([]string{
		"tag v1.0",
		"from :2",
		"tagger T <t@t.com> 100 +0",
		"data 3",
		"tag",
		"reset refs/heads/main",
		"from :2",
		"",
	}, "\n")

	p := NewParser(strings.NewReader(input))

	cmd, err := p.Next()
	if err != nil {
		t.Fatalf("tag error: %v", err)
	}
	if cmd.Type != CmdTag {
		t.Fatalf("expected CmdTag, got %d", cmd.Type)
	}

	cmd, err = p.Next()
	if err != nil {
		t.Fatalf("reset error: %v", err)
	}
	if cmd.Type != CmdReset {
		t.Fatalf("expected CmdReset, got %d", cmd.Type)
	}
}

func TestParser_CommitWithAuthorOnly(t *testing.T) {
	input := strings.Join([]string{
		"commit refs/heads/main",
		"author Author Name <author@example.com> 100 +0100",
		"committer Committer Name <committer@example.com> 200 -0500",
		"data 4",
		"test",
		"",
	}, "\n")

	p := NewParser(strings.NewReader(input))
	cmd, err := p.Next()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	c := cmd.Commit
	if c.Author != "Author Name <author@example.com> 100 +0100" {
		t.Errorf("author = %q", c.Author)
	}
	if c.Committer != "Committer Name <committer@example.com> 200 -0500" {
		t.Errorf("committer = %q", c.Committer)
	}
}

func TestParser_DeleteQuotedPath(t *testing.T) {
	input := "commit refs/heads/main\ncommitter A <a@b.com> 1 +0\ndata 1\nx\n" +
		"D \"path\\twith\\ttabs.txt\"\n\n"

	p := NewParser(strings.NewReader(input))
	cmd, err := p.Next()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cmd.Commit.FileOps) != 1 {
		t.Fatalf("expected 1 file op")
	}
	if cmd.Commit.FileOps[0].Path != "path\twith\ttabs.txt" {
		t.Errorf("path = %q, want %q", cmd.Commit.FileOps[0].Path, "path\twith\ttabs.txt")
	}
}
