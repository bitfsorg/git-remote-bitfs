# CLAUDE.md

This file provides guidance to Claude Code when working with code in this repository.

## Project Overview

**git-remote-bitfs** is a Git remote helper that translates between Git's object model and the Metanet DAG on BSV blockchain. Users work with standard git locally; `git push` encrypts and broadcasts to blockchain; `git fetch`/`git clone` reads from chain and decrypts.

URL scheme: `bitfs://<address>[@network]`

## Build & Test

```bash
go test ./...                           # Run all unit tests
go test ./internal/stream/ -v           # Run single package tests
go build ./cmd/git-remote-bitfs         # Build binary
go install ./cmd/git-remote-bitfs       # Install to $GOPATH/bin
```

Module: `github.com/tongxiaofeng/git-remote-bitfs`, Go 1.25.6

## Project Structure

```
git-remote-bitfs/
├── cmd/git-remote-bitfs/    ← Binary entry point
├── internal/
│   ├── helper/              ← Git remote helper protocol (stdin/stdout)
│   ├── stream/              ← Fast-import/fast-export stream parsing
│   ├── mapper/              ← Git SHA ↔ Metanet mapping (git notes)
│   ├── chain/               ← Metanet DAG read/write (encrypt, TX, broadcast)
│   ├── config/              ← URL parsing, .bitfsattributes, wallet loading
│   └── utxo/                ← UTXO state management (atomic JSON)
├── integration/             ← Integration tests (regtest, build tag: e2e)
└── docs/plans/              ← Design and implementation plans
```

## Dependencies

| Package | Purpose |
|---------|---------|
| `github.com/tongxiaofeng/libbitfs-go` | Shared core: method42, wallet, tx, metanet, network |
| `github.com/bsv-blockchain/go-sdk` | BSV primitives (ec, bip32, transaction, script) |
| `github.com/stretchr/testify` | Test assertions |
| `golang.org/x/crypto` | HKDF, Argon2id |

Uses `replace ../libbitfs-go` directive in go.mod.

## Key Design Decisions

- Protocol: import/export (fast-import/fast-export streams)
- Encryption in remote helper (local git stores plaintext)
- Object mapping state in git notes (`refs/notes/bitfs`)
- Branch consistency via ref UTXO pattern (CAS via double-spend)
- MVP: free + private access only (paid deferred to v0.2)
- Per-object mapping (not packfile) for individual file addressability

## Coding Conventions

- Idiomatic Go with table-driven tests
- Error wrapping: `fmt.Errorf("context: %w", err)`
- go-sdk `compat/bip32` needs alias: `compat "github.com/bsv-blockchain/go-sdk/compat/bip32"`
- MetaFlag: `0x6d657461` ("meta" in ASCII)
- Dust limit: 546 satoshis
- License: OpenBSV License Version 5
