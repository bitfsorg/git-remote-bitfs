// Package config handles configuration for git-remote-bitfs including
// URL parsing, wallet loading, and .bitfsattributes processing.
package config

import (
	"encoding/hex"
	"fmt"
	"strings"
)

// Known network names.
var knownNetworks = map[string]bool{
	"mainnet": true,
	"testnet": true,
	"regtest": true,
}

// RemoteURL represents a parsed bitfs:// remote URL.
type RemoteURL struct {
	Address string // paymail address or hex pubkey
	Network string // "mainnet", "testnet", "regtest" (default: "mainnet")
}

// ParseURL parses a bitfs:// URL string.
// Formats:
//
//	bitfs://alice@bitfs.org          -> Address="alice@bitfs.org", Network="mainnet"
//	bitfs://alice@bitfs.org@regtest  -> Address="alice@bitfs.org", Network="regtest"
//	bitfs://02abc123...              -> Address="02abc123...", Network="mainnet"
//	bitfs://02abc123...@testnet      -> Address="02abc123...", Network="testnet"
func ParseURL(rawURL string) (*RemoteURL, error) {
	const scheme = "bitfs://"
	if !strings.HasPrefix(rawURL, scheme) {
		return nil, fmt.Errorf("invalid bitfs URL: missing scheme %q", scheme)
	}

	rest := rawURL[len(scheme):]
	if rest == "" {
		return nil, fmt.Errorf("invalid bitfs URL: empty address")
	}

	// Split on the last '@' to check for a network suffix.
	// The address part may itself contain '@' (paymail).
	address := rest
	network := "mainnet"

	if idx := strings.LastIndex(rest, "@"); idx > 0 {
		candidate := rest[idx+1:]
		if knownNetworks[candidate] {
			address = rest[:idx]
			network = candidate
		}
		// Otherwise the whole thing is the address (e.g. alice@bitfs.org
		// where bitfs.org is NOT a network name).
	}

	if address == "" {
		return nil, fmt.Errorf("invalid bitfs URL: empty address")
	}

	return &RemoteURL{
		Address: address,
		Network: network,
	}, nil
}

// IsPaymail returns true if the address looks like a paymail (contains @).
func (u *RemoteURL) IsPaymail() bool {
	return strings.Contains(u.Address, "@")
}

// IsHexPubKey returns true if the address looks like a hex-encoded
// compressed public key (66 hex chars starting with 02 or 03).
func (u *RemoteURL) IsHexPubKey() bool {
	if len(u.Address) != 66 {
		return false
	}
	if !strings.HasPrefix(u.Address, "02") && !strings.HasPrefix(u.Address, "03") {
		return false
	}
	_, err := hex.DecodeString(u.Address)
	return err == nil
}
