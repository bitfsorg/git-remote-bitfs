// Package main implements the git-remote-bitfs binary.
// Git invokes this as: git-remote-bitfs <remote-name> <url>
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/tongxiaofeng/git-remote-bitfs/internal/helper"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "Usage: git-remote-bitfs <remote-name> <url>\n")
		os.Exit(1)
	}

	remoteName := os.Args[1]
	remoteURL := os.Args[2]

	// Determine git directory from environment or default.
	gitDir := os.Getenv("GIT_DIR")
	if gitDir == "" {
		// Default to .git in current directory.
		gitDir = filepath.Join(".", ".git")
	}

	cfg := helper.HelperConfig{
		RemoteName: remoteName,
		RemoteURL:  remoteURL,
		GitDir:     gitDir,
		// Blockchain and Wallet are left nil for now.
		// They will be initialized from wallet config when chain operations
		// are needed (e.g., BITFS_SEED env var or git config).
	}

	h, err := helper.New(cfg, os.Stdin, os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}

	if err := h.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}
