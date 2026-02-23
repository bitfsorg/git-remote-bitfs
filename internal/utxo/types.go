// Package utxo manages UTXO state for git-remote-bitfs with atomic
// persistence using temp file + rename for crash safety.
package utxo

// FeeUTXO represents a UTXO used for transaction fees.
type FeeUTXO struct {
	TxID         string `json:"txid"`          // hex-encoded, 64 chars
	Vout         uint32 `json:"vout"`
	Amount       uint64 `json:"amount"`        // satoshis
	ScriptPubKey string `json:"script_pubkey"` // hex-encoded
	KeyPath      string `json:"key_path"`      // BIP44 derivation path
}

// NodeUTXO represents a P_node UTXO (identity UTXO for a Metanet node).
type NodeUTXO struct {
	TxID         string `json:"txid"`
	Vout         uint32 `json:"vout"`
	Amount       uint64 `json:"amount"`
	PNode        string `json:"pnode"`         // hex-encoded compressed pubkey
	ScriptPubKey string `json:"script_pubkey"`
}

// RefUTXO represents a branch head reference UTXO (for CAS consistency).
type RefUTXO struct {
	TxID         string `json:"txid"`
	Vout         uint32 `json:"vout"`
	Amount       uint64 `json:"amount"`
	Ref          string `json:"ref"`           // e.g. "refs/heads/main"
	PNode        string `json:"pnode"`         // branch P_node
	ScriptPubKey string `json:"script_pubkey"`
	AnchorTxID   string `json:"anchor_txid"`   // latest anchor TX for this ref
}

// State holds all UTXO state for a repository.
type State struct {
	FeeUTXOs  []FeeUTXO  `json:"fee_utxos"`
	NodeUTXOs []NodeUTXO `json:"node_utxos"`
	RefUTXOs  []RefUTXO  `json:"ref_utxos"`
}
