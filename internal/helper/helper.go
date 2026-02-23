// Package helper implements the git remote helper protocol for bitfs.
// It handles stdin/stdout communication with git, dispatching to
// capabilities, list, import, and export handlers.
package helper

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// Helper implements the git remote helper protocol.
type Helper struct {
	remoteName string
	remoteURL  string
	stdin      *bufio.Reader
	stdout     io.Writer
	stderr     io.Writer
}

// New creates a new Helper instance.
func New(remoteName, remoteURL string, stdin io.Reader, stdout, stderr io.Writer) (*Helper, error) {
	return &Helper{
		remoteName: remoteName,
		remoteURL:  remoteURL,
		stdin:      bufio.NewReader(stdin),
		stdout:     stdout,
		stderr:     stderr,
	}, nil
}

// Run starts the remote helper protocol loop.
func (h *Helper) Run() error {
	for {
		line, err := h.stdin.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("reading stdin: %w", err)
		}
		line = strings.TrimRight(line, "\n")

		if line == "" {
			return nil
		}

		cmd, args, _ := strings.Cut(line, " ")
		switch cmd {
		case "capabilities":
			if err := h.handleCapabilities(); err != nil {
				return err
			}
		case "list":
			if err := h.handleList(args); err != nil {
				return err
			}
		case "import":
			if err := h.handleImport(args); err != nil {
				return err
			}
		case "export":
			if err := h.handleExport(); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported command: %s", cmd)
		}
	}
}

func (h *Helper) handleCapabilities() error {
	caps := []string{
		"import",
		"export",
		"refspec refs/heads/*:refs/bitfs/heads/*",
		"refspec refs/tags/*:refs/bitfs/tags/*",
		"",
	}
	for _, c := range caps {
		if _, err := fmt.Fprintf(h.stdout, "%s\n", c); err != nil {
			return err
		}
	}
	return nil
}

func (h *Helper) handleList(args string) error {
	// TODO: implement list refs from chain
	_, _ = fmt.Fprintf(h.stdout, "\n")
	return nil
}

func (h *Helper) handleImport(refs string) error {
	// TODO: implement import (fetch from chain)
	return fmt.Errorf("import not yet implemented")
}

func (h *Helper) handleExport() error {
	// TODO: implement export (push to chain)
	return fmt.Errorf("export not yet implemented")
}
