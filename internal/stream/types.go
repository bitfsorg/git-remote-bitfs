// Package stream implements parsing of git fast-export streams and
// generation of git fast-import streams.
//
// The fast-export format is produced by `git fast-export` and represents
// a git repository's history as a sequential stream of blob, commit,
// reset, and tag commands. The fast-import format is the inverse,
// consumed by `git fast-import` to recreate a repository.
//
// This package provides a streaming parser (Parser) that reads fast-export
// output one command at a time, and a generator (Generator) that writes
// fast-import format.
package stream

// CommandType identifies the type of a fast-export/fast-import command.
type CommandType int

const (
	// CmdBlob represents a blob (file content) command.
	CmdBlob CommandType = iota
	// CmdCommit represents a commit command with tree modifications.
	CmdCommit
	// CmdReset represents a ref reset command.
	CmdReset
	// CmdTag represents an annotated tag command.
	CmdTag
)

// FileOpType identifies the type of a file operation within a commit.
type FileOpType int

const (
	// FileModify represents an M (modify) file operation.
	FileModify FileOpType = iota
	// FileDelete represents a D (delete) file operation.
	FileDelete
)

// FileOp represents a single file operation within a commit command.
type FileOp struct {
	// Op is the operation type (modify or delete).
	Op FileOpType
	// Mode is the file mode (100644, 100755, 120000 for symlink, 160000 for submodule).
	// Only meaningful for FileModify operations.
	Mode uint32
	// DataRef is the mark reference (e.g. ":1") or "inline" for inline data.
	// Only meaningful for FileModify operations.
	DataRef string
	// Path is the file path relative to the repository root.
	Path string
	// Data holds inline data when DataRef == "inline".
	Data []byte
}

// Blob represents a blob command containing raw file data.
type Blob struct {
	// Mark is the mark identifier (e.g. ":1") assigned to this blob.
	Mark string
	// Data is the raw blob content.
	Data []byte
}

// Commit represents a commit command with metadata and file operations.
type Commit struct {
	// Mark is the mark identifier (e.g. ":2") assigned to this commit.
	Mark string
	// Ref is the target reference (e.g. "refs/heads/main").
	Ref string
	// Author is the author identity in "Name <email> timestamp timezone" format.
	Author string
	// Committer is the committer identity in the same format as Author.
	Committer string
	// Message is the commit message.
	Message string
	// From is the parent commit mark or SHA (first parent).
	From string
	// Merges contains additional parent marks/SHAs for merge commits.
	Merges []string
	// FileOps lists all file operations in this commit.
	FileOps []FileOp
}

// Reset represents a ref reset command.
type Reset struct {
	// Ref is the reference to reset (e.g. "refs/heads/main").
	Ref string
	// From is the mark or SHA to reset to.
	From string
}

// Tag represents an annotated tag command.
type Tag struct {
	// Name is the tag name (e.g. "v1.0").
	Name string
	// From is the mark or SHA the tag points to.
	From string
	// Tagger is the tagger identity in "Name <email> timestamp timezone" format.
	Tagger string
	// Message is the tag message.
	Message string
}

// Command is a tagged union representing any fast-export/fast-import command.
// Exactly one of Blob, Commit, Reset, or Tag will be non-nil, corresponding
// to the Type field.
type Command struct {
	// Type identifies which command variant this is.
	Type CommandType
	// Blob is set when Type == CmdBlob.
	Blob *Blob
	// Commit is set when Type == CmdCommit.
	Commit *Commit
	// Reset is set when Type == CmdReset.
	Reset *Reset
	// Tag is set when Type == CmdTag.
	Tag *Tag
}
