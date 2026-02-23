package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// WalletConfig holds wallet loading configuration.
type WalletConfig struct {
	SeedHex    string // hex-encoded seed (from env or gitconfig)
	WalletPath string // path to wallet.enc file
	Passphrase string // for encrypted wallet
	Network    string // mainnet/testnet/regtest
}

// LoadWalletConfig loads wallet configuration from (in priority order):
//  1. Environment variables: BITFS_SEED, BITFS_WALLET_PATH, BITFS_PASSPHRASE, BITFS_NETWORK
//  2. Git config: bitfs.seed, bitfs.walletPath, bitfs.passphrase, bitfs.network
//  3. Default paths: ~/.bitfs/wallet.enc, network=mainnet
func LoadWalletConfig() (*WalletConfig, error) {
	cfg := &WalletConfig{}

	// 1. Environment variables (highest priority).
	cfg.SeedHex = os.Getenv("BITFS_SEED")
	cfg.WalletPath = os.Getenv("BITFS_WALLET_PATH")
	cfg.Passphrase = os.Getenv("BITFS_PASSPHRASE")
	cfg.Network = os.Getenv("BITFS_NETWORK")

	// 2. Git config (fill in blanks).
	if cfg.SeedHex == "" {
		cfg.SeedHex = gitConfigGet("bitfs.seed")
	}
	if cfg.WalletPath == "" {
		cfg.WalletPath = gitConfigGet("bitfs.walletPath")
	}
	if cfg.Passphrase == "" {
		cfg.Passphrase = gitConfigGet("bitfs.passphrase")
	}
	if cfg.Network == "" {
		cfg.Network = gitConfigGet("bitfs.network")
	}

	// 3. Defaults.
	if cfg.WalletPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		cfg.WalletPath = filepath.Join(home, ".bitfs", "wallet.enc")
	}
	if cfg.Network == "" {
		cfg.Network = "mainnet"
	}

	return cfg, nil
}

// gitConfigGet runs `git config --get <key>` and returns the value.
// Returns empty string if the key is not set or git is not available.
func gitConfigGet(key string) string {
	cmd := exec.Command("git", "config", "--get", key)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
