package config

import (
	"testing"
)

func TestParseURL(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    *RemoteURL
		wantErr bool
	}{
		{
			name: "simple paymail",
			raw:  "bitfs://alice@bitfs.org",
			want: &RemoteURL{Address: "alice@bitfs.org", Network: "mainnet"},
		},
		{
			name: "paymail with network",
			raw:  "bitfs://alice@bitfs.org@regtest",
			want: &RemoteURL{Address: "alice@bitfs.org", Network: "regtest"},
		},
		{
			name: "paymail with testnet",
			raw:  "bitfs://bob@example.com@testnet",
			want: &RemoteURL{Address: "bob@example.com", Network: "testnet"},
		},
		{
			name: "hex pubkey compressed 02",
			raw:  "bitfs://02a1633cafcc01ebfb6d78e39f687a1f0995c62fc95f51ead10a02ee0be551b5dc",
			want: &RemoteURL{
				Address: "02a1633cafcc01ebfb6d78e39f687a1f0995c62fc95f51ead10a02ee0be551b5dc",
				Network: "mainnet",
			},
		},
		{
			name: "hex pubkey compressed 03",
			raw:  "bitfs://03a1633cafcc01ebfb6d78e39f687a1f0995c62fc95f51ead10a02ee0be551b5dc",
			want: &RemoteURL{
				Address: "03a1633cafcc01ebfb6d78e39f687a1f0995c62fc95f51ead10a02ee0be551b5dc",
				Network: "mainnet",
			},
		},
		{
			name: "hex pubkey with network",
			raw:  "bitfs://02a1633cafcc01ebfb6d78e39f687a1f0995c62fc95f51ead10a02ee0be551b5dc@testnet",
			want: &RemoteURL{
				Address: "02a1633cafcc01ebfb6d78e39f687a1f0995c62fc95f51ead10a02ee0be551b5dc",
				Network: "testnet",
			},
		},
		{
			name: "paymail domain not a network",
			raw:  "bitfs://user@mainnet.example.com",
			want: &RemoteURL{Address: "user@mainnet.example.com", Network: "mainnet"},
		},
		{
			name:    "missing scheme",
			raw:     "alice@bitfs.org",
			wantErr: true,
		},
		{
			name:    "wrong scheme",
			raw:     "https://alice@bitfs.org",
			wantErr: true,
		},
		{
			name:    "empty address",
			raw:     "bitfs://",
			wantErr: true,
		},
		{
			name: "paymail with mainnet explicit",
			raw:  "bitfs://alice@bitfs.org@mainnet",
			want: &RemoteURL{Address: "alice@bitfs.org", Network: "mainnet"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseURL(tt.raw)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseURL(%q) expected error, got nil", tt.raw)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseURL(%q) unexpected error: %v", tt.raw, err)
			}
			if got.Address != tt.want.Address {
				t.Errorf("Address = %q, want %q", got.Address, tt.want.Address)
			}
			if got.Network != tt.want.Network {
				t.Errorf("Network = %q, want %q", got.Network, tt.want.Network)
			}
		})
	}
}

func TestRemoteURL_IsPaymail(t *testing.T) {
	tests := []struct {
		address string
		want    bool
	}{
		{"alice@bitfs.org", true},
		{"user@example.com", true},
		{"02a1633cafcc01ebfb6d78e39f687a1f0995c62fc95f51ead10a02ee0be551b5dc", false},
	}

	for _, tt := range tests {
		u := &RemoteURL{Address: tt.address}
		if got := u.IsPaymail(); got != tt.want {
			t.Errorf("IsPaymail(%q) = %v, want %v", tt.address, got, tt.want)
		}
	}
}

func TestRemoteURL_IsHexPubKey(t *testing.T) {
	tests := []struct {
		address string
		want    bool
	}{
		{"02a1633cafcc01ebfb6d78e39f687a1f0995c62fc95f51ead10a02ee0be551b5dc", true},
		{"03a1633cafcc01ebfb6d78e39f687a1f0995c62fc95f51ead10a02ee0be551b5dc", true},
		{"alice@bitfs.org", false},
		{"04a1633cafcc01ebfb6d78e39f687a1f0995c62fc95f51ead10a02ee0be551b5dc", false}, // starts with 04
		{"02a1633c", false},  // too short
		{"02zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz", false}, // invalid hex
	}

	for _, tt := range tests {
		u := &RemoteURL{Address: tt.address}
		if got := u.IsHexPubKey(); got != tt.want {
			t.Errorf("IsHexPubKey(%q) = %v, want %v", tt.address, got, tt.want)
		}
	}
}
