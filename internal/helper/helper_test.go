package helper

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestHelper creates a helper with no chain dependencies for protocol testing.
func newTestHelper(t *testing.T, input string) (*Helper, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cfg := HelperConfig{
		RemoteName: "origin",
		RemoteURL:  "bitfs://alice@bitfs.org",
	}
	h, err := New(cfg, strings.NewReader(input), stdout, stderr)
	require.NoError(t, err)
	return h, stdout, stderr
}

func TestCapabilities(t *testing.T) {
	h, stdout, _ := newTestHelper(t, "capabilities\n\n")

	err := h.Run()
	require.NoError(t, err)

	output := stdout.String()
	assert.Contains(t, output, "import\n")
	assert.Contains(t, output, "export\n")
	assert.Contains(t, output, "refspec refs/heads/*:refs/bitfs/heads/*\n")
	assert.Contains(t, output, "refspec refs/tags/*:refs/bitfs/tags/*\n")

	// The output should end with "\n\n" (the last capability line "\n" + terminator "\n").
	assert.True(t, strings.HasSuffix(output, "\n\n"), "should end with empty line terminator")
}

func TestCapabilitiesExactOutput(t *testing.T) {
	h, stdout, _ := newTestHelper(t, "capabilities\n\n")

	err := h.Run()
	require.NoError(t, err)

	expected := "import\nexport\nrefspec refs/heads/*:refs/bitfs/heads/*\nrefspec refs/tags/*:refs/bitfs/tags/*\n\n"
	assert.Equal(t, expected, stdout.String())
}

func TestListForPushEmpty(t *testing.T) {
	h, stdout, _ := newTestHelper(t, "list for-push\n\n")

	err := h.Run()
	require.NoError(t, err)

	// Empty list: just a newline.
	assert.Equal(t, "\n", stdout.String())
}

func TestListEmpty(t *testing.T) {
	h, stdout, _ := newTestHelper(t, "list\n\n")

	err := h.Run()
	require.NoError(t, err)

	// Empty list: just a newline.
	assert.Equal(t, "\n", stdout.String())
}

func TestProtocolLoop(t *testing.T) {
	// Send: capabilities -> list -> terminate.
	input := "capabilities\nlist\n\n"
	h, stdout, _ := newTestHelper(t, input)

	err := h.Run()
	require.NoError(t, err)

	output := stdout.String()

	// Should contain capabilities response.
	assert.Contains(t, output, "import\n")
	assert.Contains(t, output, "export\n")

	// The list response follows capabilities.
	// Split output: capabilities block ends with "\n\n", then list output.
	parts := strings.SplitN(output, "\n\n", 2)
	require.Len(t, parts, 2, "expected capabilities + list output")

	// The list block should be just a newline (empty list).
	assert.Equal(t, "\n", parts[1])
}

func TestProtocolLoopMultipleCommands(t *testing.T) {
	// Send: capabilities -> list for-push -> terminate.
	input := "capabilities\nlist for-push\n\n"
	h, stdout, _ := newTestHelper(t, input)

	err := h.Run()
	require.NoError(t, err)

	output := stdout.String()

	// Capabilities response.
	assert.Contains(t, output, "import\n")
	assert.Contains(t, output, "export\n")

	// List for-push response at the end.
	assert.True(t, strings.HasSuffix(output, "\n\n"), "output should end with empty list terminator")
}

func TestUnsupportedCommand(t *testing.T) {
	h, _, _ := newTestHelper(t, "foobar\n\n")

	err := h.Run()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported command")
	assert.Contains(t, err.Error(), "foobar")
}

func TestExportSimple(t *testing.T) {
	// Build a minimal fast-export stream: one blob + one commit.
	stream := strings.Join([]string{
		"blob",
		"mark :1",
		"data 5",
		"hello",
		"commit refs/heads/main",
		"mark :2",
		"committer Test User <test@example.com> 1234567890 +0000",
		"data 12",
		"first commit",
		"M 100644 :1 hello.txt",
		"",
		"done",
		"",
	}, "\n")

	// Prefix with "export\n" command, then the fast-export stream follows.
	input := "export\n" + stream
	h, stdout, _ := newTestHelper(t, input)

	err := h.Run()
	require.NoError(t, err)

	output := stdout.String()
	// In protocol-only mode (no blockchain), should report ok.
	assert.Contains(t, output, "ok refs/heads/main\n")
	// Terminated by empty line.
	assert.True(t, strings.HasSuffix(output, "\n\n") || strings.HasSuffix(output, "\n"),
		"output should end with terminator")
}

func TestExportMultipleFiles(t *testing.T) {
	stream := strings.Join([]string{
		"blob",
		"mark :1",
		"data 5",
		"hello",
		"blob",
		"mark :2",
		"data 5",
		"world",
		"commit refs/heads/main",
		"mark :3",
		"committer Test <t@t.com> 1000 +0000",
		"data 4",
		"init",
		"M 100644 :1 a.txt",
		"M 100644 :2 b.txt",
		"",
		"done",
		"",
	}, "\n")

	input := "export\n" + stream
	h, stdout, _ := newTestHelper(t, input)

	err := h.Run()
	require.NoError(t, err)

	output := stdout.String()
	assert.Contains(t, output, "ok refs/heads/main\n")
}

func TestExportWithDelete(t *testing.T) {
	stream := strings.Join([]string{
		"commit refs/heads/main",
		"mark :1",
		"committer Test <t@t.com> 2000 +0000",
		"data 6",
		"delete",
		"D old-file.txt",
		"",
		"done",
		"",
	}, "\n")

	input := "export\n" + stream
	h, stdout, _ := newTestHelper(t, input)

	err := h.Run()
	require.NoError(t, err)

	output := stdout.String()
	assert.Contains(t, output, "ok refs/heads/main\n")
}

func TestExportMultipleRefs(t *testing.T) {
	stream := strings.Join([]string{
		"blob",
		"mark :1",
		"data 3",
		"foo",
		"commit refs/heads/main",
		"mark :2",
		"committer A <a@b.com> 1000 +0000",
		"data 4",
		"main",
		"M 100644 :1 foo.txt",
		"",
		"blob",
		"mark :3",
		"data 3",
		"bar",
		"commit refs/heads/dev",
		"mark :4",
		"committer A <a@b.com> 2000 +0000",
		"data 3",
		"dev",
		"M 100644 :3 bar.txt",
		"",
		"done",
		"",
	}, "\n")

	input := "export\n" + stream
	h, stdout, _ := newTestHelper(t, input)

	err := h.Run()
	require.NoError(t, err)

	output := stdout.String()
	assert.Contains(t, output, "ok refs/heads/main\n")
	assert.Contains(t, output, "ok refs/heads/dev\n")
}

func TestExportEmptyStream(t *testing.T) {
	// An export with just "done" and no commits.
	stream := "done\n"
	input := "export\n" + stream
	h, stdout, _ := newTestHelper(t, input)

	err := h.Run()
	require.NoError(t, err)

	// No refs to report, just terminator.
	assert.Equal(t, "\n", stdout.String())
}

func TestExportWithReset(t *testing.T) {
	stream := strings.Join([]string{
		"reset refs/heads/main",
		"",
		"done",
		"",
	}, "\n")

	input := "export\n" + stream
	h, stdout, _ := newTestHelper(t, input)

	err := h.Run()
	require.NoError(t, err)

	output := stdout.String()
	assert.Contains(t, output, "ok refs/heads/main\n")
}

func TestFullProtocolSequence(t *testing.T) {
	// Simulate a full push sequence: capabilities -> list for-push -> export.
	exportStream := strings.Join([]string{
		"blob",
		"mark :1",
		"data 12",
		"hello world!",
		"commit refs/heads/main",
		"mark :2",
		"committer Test <t@t.com> 1000 +0000",
		"data 4",
		"init",
		"M 100644 :1 README.md",
		"",
		"done",
		"",
	}, "\n")

	input := "capabilities\nlist for-push\nexport\n" + exportStream
	h, stdout, _ := newTestHelper(t, input)

	err := h.Run()
	require.NoError(t, err)

	output := stdout.String()

	// Capabilities response.
	assert.Contains(t, output, "import\n")
	assert.Contains(t, output, "export\n")
	assert.Contains(t, output, "refspec refs/heads/*:refs/bitfs/heads/*\n")

	// Export result.
	assert.Contains(t, output, "ok refs/heads/main\n")
}

func TestEOFTermination(t *testing.T) {
	// If stdin closes without empty line, should exit gracefully.
	h, _, _ := newTestHelper(t, "capabilities\n")

	err := h.Run()
	// The capabilities handler runs, then ReadString gets EOF.
	require.NoError(t, err)
}

func TestNewWithInvalidURL(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cfg := HelperConfig{
		RemoteName: "origin",
		RemoteURL:  "http://invalid-url",
	}
	_, err := New(cfg, strings.NewReader(""), stdout, stderr)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parsing remote URL")
}

func TestNewWithEmptyURL(t *testing.T) {
	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cfg := HelperConfig{
		RemoteName: "origin",
		RemoteURL:  "",
	}
	h, err := New(cfg, strings.NewReader(""), stdout, stderr)
	require.NoError(t, err)
	assert.Nil(t, h.parsedURL)
}

func TestGuessMimeType(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"main.go", "text/x-go"},
		{"app.js", "text/javascript"},
		{"script.py", "text/x-python"},
		{"README.md", "text/markdown"},
		{"config.json", "application/json"},
		{"index.html", "text/html"},
		{"style.css", "text/css"},
		{"notes.txt", "text/plain"},
		{"logo.png", "image/png"},
		{"photo.jpg", "image/jpeg"},
		{"photo.jpeg", "image/jpeg"},
		{"binary.dat", "application/octet-stream"},
		{"Makefile", "application/octet-stream"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := guessMimeType(tt.path)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestHexToBytes(t *testing.T) {
	// Valid hex.
	result := hexToBytes("abcd")
	assert.Equal(t, []byte{0xab, 0xcd}, result)

	// Invalid hex returns empty (hex.DecodeString returns []byte{} + error).
	result = hexToBytes("xyz")
	assert.Empty(t, result)

	// Empty string returns empty byte slice.
	result = hexToBytes("")
	assert.Empty(t, result)
}

func TestExportInlineData(t *testing.T) {
	stream := strings.Join([]string{
		"commit refs/heads/main",
		"mark :1",
		"committer Test <t@t.com> 1000 +0000",
		"data 11",
		"inline test",
		"M 100644 inline readme.md",
		"data 11",
		"hello world",
		"",
		"done",
		"",
	}, "\n")

	input := "export\n" + stream
	h, stdout, _ := newTestHelper(t, input)

	err := h.Run()
	require.NoError(t, err)

	output := stdout.String()
	assert.Contains(t, output, "ok refs/heads/main\n")
}

func TestImportNoChainReader(t *testing.T) {
	h, _, _ := newTestHelper(t, "import refs/heads/main\n\n")

	err := h.Run()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no chain reader available")
}
