package stream

import (
	"fmt"
	"io"
	"strings"
)

// Generator writes git fast-import format to an io.Writer.
type Generator struct {
	w io.Writer
}

// NewGenerator creates a new Generator that writes to w.
func NewGenerator(w io.Writer) *Generator {
	return &Generator{w: w}
}

// WriteBlob writes a blob command with the given mark and data.
func (g *Generator) WriteBlob(mark string, data []byte) error {
	if _, err := fmt.Fprintf(g.w, "blob\n"); err != nil {
		return err
	}
	if mark != "" {
		if _, err := fmt.Fprintf(g.w, "mark %s\n", mark); err != nil {
			return err
		}
	}
	if err := g.writeData(data); err != nil {
		return err
	}
	return nil
}

// WriteCommit writes a commit command with all its metadata and file operations.
func (g *Generator) WriteCommit(c *Commit) error {
	if _, err := fmt.Fprintf(g.w, "commit %s\n", c.Ref); err != nil {
		return err
	}
	if c.Mark != "" {
		if _, err := fmt.Fprintf(g.w, "mark %s\n", c.Mark); err != nil {
			return err
		}
	}
	if c.Author != "" {
		if _, err := fmt.Fprintf(g.w, "author %s\n", c.Author); err != nil {
			return err
		}
	}
	if c.Committer != "" {
		if _, err := fmt.Fprintf(g.w, "committer %s\n", c.Committer); err != nil {
			return err
		}
	}
	if err := g.writeData([]byte(c.Message)); err != nil {
		return err
	}
	if c.From != "" {
		if _, err := fmt.Fprintf(g.w, "from %s\n", c.From); err != nil {
			return err
		}
	}
	for _, merge := range c.Merges {
		if _, err := fmt.Fprintf(g.w, "merge %s\n", merge); err != nil {
			return err
		}
	}
	for _, op := range c.FileOps {
		if err := g.writeFileOp(&op); err != nil {
			return err
		}
	}
	// Trailing empty line to separate from the next command.
	if _, err := fmt.Fprintf(g.w, "\n"); err != nil {
		return err
	}
	return nil
}

// WriteReset writes a reset command.
func (g *Generator) WriteReset(ref, from string) error {
	if _, err := fmt.Fprintf(g.w, "reset %s\n", ref); err != nil {
		return err
	}
	if from != "" {
		if _, err := fmt.Fprintf(g.w, "from %s\n", from); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(g.w, "\n"); err != nil {
		return err
	}
	return nil
}

// WriteDone writes the "done" command that terminates the fast-import stream.
func (g *Generator) WriteDone() error {
	_, err := fmt.Fprintf(g.w, "done\n")
	return err
}

// writeData writes a "data <count>\n<bytes>\n" block.
func (g *Generator) writeData(data []byte) error {
	if _, err := fmt.Fprintf(g.w, "data %d\n", len(data)); err != nil {
		return err
	}
	if _, err := g.w.Write(data); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(g.w, "\n"); err != nil {
		return err
	}
	return nil
}

// writeFileOp writes a single file operation line.
func (g *Generator) writeFileOp(op *FileOp) error {
	switch op.Op {
	case FileModify:
		path := quotePath(op.Path)
		if op.DataRef == "inline" {
			if _, err := fmt.Fprintf(g.w, "M %06o inline %s\n", op.Mode, path); err != nil {
				return err
			}
			if err := g.writeData(op.Data); err != nil {
				return err
			}
		} else {
			if _, err := fmt.Fprintf(g.w, "M %06o %s %s\n", op.Mode, op.DataRef, path); err != nil {
				return err
			}
		}
	case FileDelete:
		path := quotePath(op.Path)
		if _, err := fmt.Fprintf(g.w, "D %s\n", path); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown file op type: %d", op.Op)
	}
	return nil
}

// quotePath applies C-style quoting to a path if it contains special characters.
// Git fast-import requires quoting for paths with spaces, quotes, backslashes, or newlines.
func quotePath(p string) string {
	if needsQuoting(p) {
		var b strings.Builder
		b.WriteByte('"')
		for i := 0; i < len(p); i++ {
			switch p[i] {
			case '\\':
				b.WriteString(`\\`)
			case '"':
				b.WriteString(`\"`)
			case '\n':
				b.WriteString(`\n`)
			case '\t':
				b.WriteString(`\t`)
			default:
				b.WriteByte(p[i])
			}
		}
		b.WriteByte('"')
		return b.String()
	}
	return p
}

// needsQuoting returns true if a path contains characters that require
// C-style quoting in the fast-import format.
func needsQuoting(p string) bool {
	for i := 0; i < len(p); i++ {
		switch p[i] {
		case '"', '\\', '\n', '\t':
			return true
		}
	}
	return false
}
