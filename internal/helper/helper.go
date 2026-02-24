// Package helper implements the git remote helper protocol for bitfs.
// It handles stdin/stdout communication with git, dispatching to
// capabilities, list, import, and export handlers.
//
// Git invokes the helper via stdin/stdout. The protocol is line-based:
// git sends a command, the helper responds, terminated by an empty line.
//
// Supported commands:
//   - capabilities: report supported features
//   - list [for-push]: list refs on remote
//   - export: push objects to chain (reads fast-export stream)
//   - import <ref>: fetch objects from chain (reads Metanet DAG, writes fast-import stream)
package helper

import (
	"bufio"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/tongxiaofeng/git-remote-bitfs/internal/chain"
	"github.com/tongxiaofeng/git-remote-bitfs/internal/config"
	"github.com/tongxiaofeng/git-remote-bitfs/internal/mapper"
	"github.com/tongxiaofeng/git-remote-bitfs/internal/utxo"
	"github.com/tongxiaofeng/libbitfs-go/network"
	"github.com/tongxiaofeng/libbitfs-go/wallet"
)

// HelperConfig holds configuration for the remote helper.
type HelperConfig struct {
	RemoteName string // Git remote name (e.g. "origin")
	RemoteURL  string // Full bitfs:// URL
	GitDir     string // Path to .git directory

	// Optional dependencies (nil is OK for protocol-only testing).
	Blockchain     network.BlockchainService
	Wallet         *wallet.Wallet
	VaultIndex     uint32
	ChainReader    chain.ChainReader // For fetch/import (nil = import not available)
	ChainRefLister ChainRefLister    // For discovering remote refs during clone (nil = disabled)
}

// Helper implements the git remote helper protocol.
type Helper struct {
	config HelperConfig
	stdin  *bufio.Reader
	stdout io.Writer
	stderr io.Writer

	// Lazy-initialized components.
	notes     *mapper.NotesStore
	utxoStore *utxo.Store
	parsedURL *config.RemoteURL
}

// New creates a new Helper instance. The helper reads commands from stdin
// and writes responses to stdout. Diagnostic output goes to stderr.
//
// For unit testing, blockchain and wallet may be nil in the config.
func New(cfg HelperConfig, stdin io.Reader, stdout, stderr io.Writer) (*Helper, error) {
	h := &Helper{
		config: cfg,
		stdin:  bufio.NewReader(stdin),
		stdout: stdout,
		stderr: stderr,
	}

	// Parse the remote URL if provided.
	if cfg.RemoteURL != "" {
		parsed, err := config.ParseURL(cfg.RemoteURL)
		if err != nil {
			return nil, fmt.Errorf("parsing remote URL: %w", err)
		}
		h.parsedURL = parsed
	}

	// Initialize notes store if git dir is available.
	if cfg.GitDir != "" {
		h.notes = mapper.NewNotesStore(cfg.GitDir)
	}

	return h, nil
}

// Run starts the remote helper protocol loop. It reads commands from
// stdin one at a time, dispatches to the appropriate handler, and
// returns when stdin is closed (EOF) or an empty line is received
// outside a command context.
func (h *Helper) Run() error {
	for {
		line, err := h.stdin.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("reading stdin: %w", err)
		}
		line = strings.TrimRight(line, "\n\r")

		if line == "" {
			// An empty line outside a command context signals "done".
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

// handleCapabilities reports the helper's supported features to git.
// Output is terminated by an empty line.
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
			return fmt.Errorf("writing capability: %w", err)
		}
	}
	return nil
}

// writef is a helper to write formatted output to stdout.
func (h *Helper) writef(format string, args ...any) error {
	_, err := fmt.Fprintf(h.stdout, format, args...)
	return err
}

// debugf writes diagnostic output to stderr.
func (h *Helper) debugf(format string, args ...any) {
	fmt.Fprintf(h.stderr, "git-remote-bitfs: "+format+"\n", args...)
}

// initUTXOStore lazily initializes the UTXO store.
func (h *Helper) initUTXOStore() *utxo.Store {
	if h.utxoStore == nil && h.config.GitDir != "" {
		storePath := filepath.Join(h.config.GitDir, "bitfs", "state.json")
		h.utxoStore = utxo.NewStore(storePath)
		if err := h.utxoStore.Load(); err != nil {
			h.debugf("warning: loading UTXO store: %v", err)
		}
	}
	return h.utxoStore
}
