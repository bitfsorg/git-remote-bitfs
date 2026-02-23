package stream

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"log"
	"strconv"
	"strings"
)

// Parser reads a git fast-export stream and yields one Command at a time.
type Parser struct {
	r    *bufio.Reader
	// peeked holds a line that was read but not yet consumed.
	// This happens when we need to look ahead to determine if the
	// current command has ended.
	peeked   string
	hasPeked bool
	done     bool
}

// NewParser creates a new Parser that reads from r.
func NewParser(r io.Reader) *Parser {
	return &Parser{
		r: bufio.NewReaderSize(r, 64*1024),
	}
}

// Next returns the next command from the stream. Returns nil, io.EOF
// when the stream is exhausted. Commands are returned in the order
// they appear in the stream.
func (p *Parser) Next() (*Command, error) {
	if p.done {
		return nil, io.EOF
	}

	for {
		line, err := p.readLine()
		if err != nil {
			if err == io.EOF {
				p.done = true
				return nil, io.EOF
			}
			return nil, fmt.Errorf("reading stream: %w", err)
		}

		// Skip empty lines and comments between commands.
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		switch {
		case line == "blob":
			return p.parseBlob()
		case strings.HasPrefix(line, "commit "):
			ref := line[len("commit "):]
			return p.parseCommit(ref)
		case strings.HasPrefix(line, "reset "):
			ref := line[len("reset "):]
			return p.parseReset(ref)
		case strings.HasPrefix(line, "tag "):
			name := line[len("tag "):]
			return p.parseTag(name)
		case line == "done":
			p.done = true
			return nil, io.EOF
		default:
			// Skip unknown commands (progress, checkpoint, etc.)
			continue
		}
	}
}

// readLine reads the next line from the stream, stripping the trailing newline.
// If a line was peeked, it returns that instead.
func (p *Parser) readLine() (string, error) {
	if p.hasPeked {
		p.hasPeked = false
		return p.peeked, nil
	}

	line, err := p.r.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	// If we got data, strip the trailing newline.
	if len(line) > 0 {
		line = strings.TrimRight(line, "\n")
		return line, nil
	}
	// No data at all means EOF.
	return "", io.EOF
}

// peekLine reads the next line without consuming it.
func (p *Parser) peekLine() (string, error) {
	if p.hasPeked {
		return p.peeked, nil
	}
	line, err := p.readLine()
	if err != nil {
		return "", err
	}
	p.peeked = line
	p.hasPeked = true
	return line, nil
}

// unreadLine pushes a line back so the next readLine returns it.
func (p *Parser) unreadLine(line string) {
	p.peeked = line
	p.hasPeked = true
}

// readData reads a "data <count>\n" directive followed by exactly count bytes.
func (p *Parser) readData() ([]byte, error) {
	line, err := p.readLine()
	if err != nil {
		return nil, fmt.Errorf("expected data line: %w", err)
	}

	if !strings.HasPrefix(line, "data ") {
		return nil, fmt.Errorf("expected 'data <count>', got %q", line)
	}

	countStr := line[len("data "):]
	count, err := strconv.Atoi(countStr)
	if err != nil {
		return nil, fmt.Errorf("invalid data count %q: %w", countStr, err)
	}

	// Read exactly count bytes (binary safe).
	data := make([]byte, count)
	_, err = io.ReadFull(p.r, data)
	if err != nil {
		return nil, fmt.Errorf("reading %d data bytes: %w", count, err)
	}

	// After the data bytes, there is a trailing LF. Consume it.
	// The LF is not part of the data.
	b, err := p.r.ReadByte()
	if err == nil && b != '\n' {
		// Not a newline — push it back.
		if err2 := p.r.UnreadByte(); err2 != nil {
			return nil, fmt.Errorf("unreading byte: %w", err2)
		}
	}

	return data, nil
}

// parseBlob parses a blob command. The "blob" keyword has already been consumed.
func (p *Parser) parseBlob() (*Command, error) {
	blob := &Blob{}

	// Optional mark line.
	line, err := p.readLine()
	if err != nil {
		return nil, fmt.Errorf("reading blob mark: %w", err)
	}
	if strings.HasPrefix(line, "mark ") {
		blob.Mark = line[len("mark "):]
	} else {
		// Not a mark, must be the data line — push back.
		p.unreadLine(line)
	}

	data, err := p.readData()
	if err != nil {
		return nil, fmt.Errorf("reading blob data: %w", err)
	}
	blob.Data = data

	return &Command{Type: CmdBlob, Blob: blob}, nil
}

// parseCommit parses a commit command. The "commit <ref>" line has been consumed.
func (p *Parser) parseCommit(ref string) (*Command, error) {
	c := &Commit{Ref: ref}

	// Parse header lines until we hit file ops or the next command.
	for {
		line, err := p.peekLine()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}

		switch {
		case strings.HasPrefix(line, "mark "):
			p.readLine() //nolint:errcheck
			c.Mark = line[len("mark "):]

		case strings.HasPrefix(line, "author "):
			p.readLine() //nolint:errcheck
			c.Author = line[len("author "):]

		case strings.HasPrefix(line, "committer "):
			p.readLine() //nolint:errcheck
			c.Committer = line[len("committer "):]

		case strings.HasPrefix(line, "data "):
			// Commit message data block.
			data, err := p.readData()
			if err != nil {
				return nil, fmt.Errorf("reading commit message: %w", err)
			}
			c.Message = string(data)

		case strings.HasPrefix(line, "from "):
			p.readLine() //nolint:errcheck
			c.From = line[len("from "):]

		case strings.HasPrefix(line, "merge "):
			p.readLine() //nolint:errcheck
			c.Merges = append(c.Merges, line[len("merge "):])

		case strings.HasPrefix(line, "M "):
			p.readLine() //nolint:errcheck
			op, err := p.parseFileModify(line)
			if err != nil {
				return nil, err
			}
			if op != nil {
				c.FileOps = append(c.FileOps, *op)
			}

		case strings.HasPrefix(line, "D "):
			p.readLine() //nolint:errcheck
			path := line[2:]
			path = unquotePath(path)
			c.FileOps = append(c.FileOps, FileOp{
				Op:   FileDelete,
				Path: path,
			})

		case line == "":
			// Empty line between sections — consume and continue.
			p.readLine() //nolint:errcheck

		default:
			// Any other line means the commit is done and this line
			// belongs to the next command. Leave it peeked.
			goto done
		}
	}
done:
	return &Command{Type: CmdCommit, Commit: c}, nil
}

// parseFileModify parses an "M <mode> <dataref> <path>" line.
// Returns nil (no error) for submodule entries (mode 160000) which are skipped.
func (p *Parser) parseFileModify(line string) (*FileOp, error) {
	// Format: M <mode> <dataref> <path>
	// The path may contain spaces and could be quoted.
	parts := strings.SplitN(line, " ", 4)
	if len(parts) < 4 {
		return nil, fmt.Errorf("invalid M line: %q", line)
	}

	modeStr := parts[1]
	dataRef := parts[2]
	path := parts[3]

	mode64, err := strconv.ParseUint(modeStr, 8, 32)
	if err != nil {
		return nil, fmt.Errorf("invalid file mode %q: %w", modeStr, err)
	}
	mode := uint32(mode64)

	// Skip submodules with a warning.
	if mode == 0160000 {
		log.Printf("warning: skipping submodule entry: %s", path)
		return nil, nil
	}

	path = unquotePath(path)

	op := &FileOp{
		Op:      FileModify,
		Mode:    mode,
		DataRef: dataRef,
		Path:    path,
	}

	// If data is inline, read the data block immediately.
	if dataRef == "inline" {
		data, err := p.readData()
		if err != nil {
			return nil, fmt.Errorf("reading inline data for %s: %w", path, err)
		}
		op.Data = data
	}

	return op, nil
}

// parseReset parses a reset command. The "reset <ref>" line has been consumed.
func (p *Parser) parseReset(ref string) (*Command, error) {
	r := &Reset{Ref: ref}

	// Optional "from" line.
	line, err := p.peekLine()
	if err != nil {
		if err == io.EOF {
			return &Command{Type: CmdReset, Reset: r}, nil
		}
		return nil, err
	}

	if strings.HasPrefix(line, "from ") {
		p.readLine() //nolint:errcheck
		r.From = line[len("from "):]
	}

	return &Command{Type: CmdReset, Reset: r}, nil
}

// parseTag parses a tag command. The "tag <name>" line has been consumed.
func (p *Parser) parseTag(name string) (*Command, error) {
	t := &Tag{Name: name}

	for {
		line, err := p.peekLine()
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}

		switch {
		case strings.HasPrefix(line, "from "):
			p.readLine() //nolint:errcheck
			t.From = line[len("from "):]

		case strings.HasPrefix(line, "tagger "):
			p.readLine() //nolint:errcheck
			t.Tagger = line[len("tagger "):]

		case strings.HasPrefix(line, "data "):
			data, err := p.readData()
			if err != nil {
				return nil, fmt.Errorf("reading tag message: %w", err)
			}
			t.Message = string(data)

		case line == "":
			p.readLine() //nolint:errcheck

		default:
			goto done
		}
	}
done:
	return &Command{Type: CmdTag, Tag: t}, nil
}

// unquotePath removes C-style quoting from paths if present.
// Git fast-export quotes paths that contain special characters.
func unquotePath(p string) string {
	if len(p) >= 2 && p[0] == '"' && p[len(p)-1] == '"' {
		// Attempt to unquote as a Go string literal (which handles
		// the same escape sequences as C strings).
		unquoted, err := strconv.Unquote(p)
		if err == nil {
			return unquoted
		}
		// Fall back: strip quotes, unescape common sequences.
		inner := p[1 : len(p)-1]
		var buf bytes.Buffer
		for i := 0; i < len(inner); i++ {
			if inner[i] == '\\' && i+1 < len(inner) {
				switch inner[i+1] {
				case '\\':
					buf.WriteByte('\\')
				case '"':
					buf.WriteByte('"')
				case 'n':
					buf.WriteByte('\n')
				case 't':
					buf.WriteByte('\t')
				default:
					buf.WriteByte(inner[i+1])
				}
				i++
			} else {
				buf.WriteByte(inner[i])
			}
		}
		return buf.String()
	}
	return p
}
