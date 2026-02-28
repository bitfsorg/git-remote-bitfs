// Package chain handles reading from and writing to the Metanet DAG on BSV.
// It encrypts content with Method 42, builds transactions, and manages anchors.
package chain

import (
	"context"
	"encoding/hex"
	"fmt"

	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"

	"github.com/bitfsorg/libbitfs-go/method42"
	"github.com/bitfsorg/libbitfs-go/network"
	"github.com/bitfsorg/libbitfs-go/tx"
)

// NodeOp represents a fully prepared node operation ready for TX building.
type NodeOp struct {
	PubKey     *ec.PublicKey  // Node's compressed public key (P_node)
	PrivKey    *ec.PrivateKey // Node's private key (D_node), for signing input
	ParentTxID []byte         // Parent's TxID for OP_RETURN field (0 or 32 bytes)
	Payload    []byte         // Serialized TLV payload
	InputUTXO  *tx.UTXO       // Existing UTXO to spend (nil for creates)
	Purpose    string         // "file", "dir", "anchor" (for logging)
}

// PushParams holds all parameters for a push operation.
type PushParams struct {
	Ops        []NodeOp   // Node operations to include in the batch TX
	FeeUTXOs   []*tx.UTXO // Fee UTXOs for transaction fees
	ChangeAddr []byte     // 20-byte P2PKH hash for change output
	FeeRate    uint64     // Fee rate in sat/KB (0 = default)
}

// PushResult holds the results from building and broadcasting a batch TX.
type PushResult struct {
	TxID       []byte              // Transaction ID (32 bytes)
	RawTx      []byte              // Serialized signed transaction
	NodeUTXOs  map[string]*tx.UTXO // pnode hex -> new UTXO for each op
	ChangeUTXO *tx.UTXO            // Change output (nil if dust)
}

// Writer handles encrypting and pushing git objects to the Metanet DAG.
type Writer struct {
	blockchain network.BlockchainService
}

// NewWriter creates a new chain writer.
func NewWriter(bc network.BlockchainService) *Writer {
	return &Writer{
		blockchain: bc,
	}
}

// Push builds a MutationBatch from the prepared ops, signs, and broadcasts.
//
// Steps:
//  1. Validate parameters
//  2. Build MutationBatch with all ops
//  3. Sign the transaction
//  4. Broadcast via blockchain service
//  5. Return result with new UTXOs
func (w *Writer) Push(ctx context.Context, params *PushParams) (*PushResult, error) {
	if params == nil {
		return nil, fmt.Errorf("chain: push params is nil")
	}
	if len(params.Ops) == 0 {
		return nil, fmt.Errorf("chain: no operations to push")
	}
	if len(params.FeeUTXOs) == 0 {
		return nil, fmt.Errorf("chain: no fee UTXOs provided")
	}

	// Build the MutationBatch.
	batch := tx.NewMutationBatch()

	for _, op := range params.Ops {
		batchOp := tx.BatchNodeOp{
			PubKey:     op.PubKey,
			ParentTxID: op.ParentTxID,
			Payload:    op.Payload,
			InputUTXO:  op.InputUTXO,
			PrivateKey: op.PrivKey,
		}

		// Determine batch op type based on whether we have an input UTXO.
		if op.InputUTXO != nil {
			batchOp.Type = tx.BatchOpNodeUpdate
		} else {
			batchOp.Type = tx.BatchOpChildCreate
		}

		batch.AddNodeOp(batchOp)
	}

	for _, feeUTXO := range params.FeeUTXOs {
		batch.AddFeeInput(feeUTXO)
	}

	if len(params.ChangeAddr) == 20 {
		batch.SetChange(params.ChangeAddr)
	}

	if params.FeeRate > 0 {
		batch.SetFeeRate(params.FeeRate)
	}

	// Build the unsigned transaction.
	batchResult, err := batch.Build()
	if err != nil {
		return nil, fmt.Errorf("chain: building batch TX: %w", err)
	}

	// Sign the transaction.
	// Collect UTXOs for signing in the same order as inputs:
	// first node inputs (ops with InputUTXO), then fee inputs.
	var signingUTXOs []*tx.UTXO
	for _, op := range params.Ops {
		if op.InputUTXO == nil {
			continue
		}
		signingUTXO := &tx.UTXO{
			TxID:         op.InputUTXO.TxID,
			Vout:         op.InputUTXO.Vout,
			Amount:       op.InputUTXO.Amount,
			ScriptPubKey: op.InputUTXO.ScriptPubKey,
			PrivateKey:   op.PrivKey,
		}
		signingUTXOs = append(signingUTXOs, signingUTXO)
	}
	for _, feeUTXO := range params.FeeUTXOs {
		signingUTXOs = append(signingUTXOs, feeUTXO)
	}

	mtx := &tx.MetanetTx{
		RawTx: batchResult.RawTx,
	}

	signedHex, err := tx.SignMetanetTx(mtx, signingUTXOs)
	if err != nil {
		return nil, fmt.Errorf("chain: signing batch TX: %w", err)
	}

	// Broadcast.
	txidHex, err := w.blockchain.BroadcastTx(ctx, signedHex)
	if err != nil {
		return nil, fmt.Errorf("chain: broadcasting TX: %w", err)
	}

	// Parse the returned txid.
	txidBytes, err := hex.DecodeString(txidHex)
	if err != nil {
		return nil, fmt.Errorf("chain: invalid broadcast txid: %w", err)
	}

	// Build result with new node UTXOs.
	nodeUTXOs := make(map[string]*tx.UTXO)
	for i, op := range params.Ops {
		pnodeHex := hex.EncodeToString(op.PubKey.Compressed())
		nodeResult := batchResult.NodeOps[i]
		nodeUTXO := nodeResult.NodeUTXO
		nodeUTXO.TxID = txidBytes
		nodeUTXOs[pnodeHex] = nodeUTXO
	}

	result := &PushResult{
		TxID:       txidBytes,
		RawTx:      mtx.RawTx,
		NodeUTXOs:  nodeUTXOs,
		ChangeUTXO: batchResult.ChangeUTXO,
	}

	if result.ChangeUTXO != nil {
		result.ChangeUTXO.TxID = txidBytes
	}

	return result, nil
}

// EncryptFile encrypts file content using Method 42.
//
// For access="free": uses FreePrivateKey (scalar 1), anyone can decrypt.
// For access="private": uses the node's private key, only owner can decrypt.
func EncryptFile(content []byte, privKey *ec.PrivateKey, pubKey *ec.PublicKey, access string) (*method42.EncryptResult, error) {
	accessMode, err := parseAccess(access)
	if err != nil {
		return nil, err
	}
	return method42.Encrypt(content, privKey, pubKey, accessMode)
}

// DecryptFile decrypts file content using Method 42.
func DecryptFile(ciphertext []byte, privKey *ec.PrivateKey, pubKey *ec.PublicKey, keyHash []byte, access string) ([]byte, error) {
	accessMode, err := parseAccess(access)
	if err != nil {
		return nil, err
	}
	result, err := method42.Decrypt(ciphertext, privKey, pubKey, keyHash, accessMode)
	if err != nil {
		return nil, err
	}
	return result.Plaintext, nil
}

// parseAccess converts a string access mode to method42.Access.
func parseAccess(access string) (method42.Access, error) {
	switch access {
	case "free":
		return method42.AccessFree, nil
	case "private":
		return method42.AccessPrivate, nil
	case "paid":
		return method42.AccessPaid, nil
	default:
		return 0, fmt.Errorf("chain: unknown access mode %q (expected free/private/paid)", access)
	}
}
