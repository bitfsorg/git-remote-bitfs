package chain

import (
	"context"
	"encoding/hex"
	"fmt"

	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"

	"github.com/tongxiaofeng/libbitfs/metanet"
	"github.com/tongxiaofeng/libbitfs/network"
	"github.com/tongxiaofeng/libbitfs/tx"
)

// AnchorParams holds parameters for building an anchor TX.
type AnchorParams struct {
	BranchRef   string         // e.g. "refs/heads/main"
	BranchPub   *ec.PublicKey  // P_branch
	BranchPriv  *ec.PrivateKey // D_branch for signing

	TreeRootPNode []byte // Root directory's P_node (33 bytes compressed)
	TreeRootTxID  []byte // Root directory's latest TxID (32 bytes)

	ParentAnchorTxIDs [][]byte // Previous anchor TxIDs (merge commits have multiple)

	Author        string // Git author string
	CommitMessage string // Git commit message
	Timestamp     uint64 // Unix timestamp
	GitCommitSHA  []byte // 20 bytes

	RefUTXO  *tx.UTXO // Current branch head UTXO (nil for first push)
	FeeUTXO  *tx.UTXO // Fee UTXO
	ChangeAddr []byte  // 20-byte P2PKH hash for change
	FeeRate    uint64  // sat/KB (0 = default)
	Force      bool    // Force push (ignore existing ref UTXO)
}

// AnchorResult holds the result of building an anchor TX.
type AnchorResult struct {
	RawTx      []byte    // Unsigned serialized TX
	TxID       []byte    // TX hash (set after signing)
	RefUTXO    *tx.UTXO  // New branch head UTXO (P2PKH -> P_branch, 546 sat)
	ChangeUTXO *tx.UTXO  // Change output (nil if dust)
}

// BuildAnchor builds an anchor transaction for a git commit.
//
// Anchor TX structure:
//
//	For first push (no RefUTXO):
//	  Uses CreateRoot pattern: 1 input (fee), 3 outputs
//	For subsequent push (with RefUTXO):
//	  Uses SelfUpdate pattern: 2 inputs (ref + fee), 3 outputs
//
// Output layout:
//
//	0: OP_RETURN [MetaFlag, P_branch, PrevAnchorTxID, AnchorPayload]
//	1: P2PKH -> P_branch (546 sat, new branch head UTXO)
//	2: P2PKH -> Change
//
// AnchorPayload TLV fields:
//
//	Type = NodeTypeAnchor (3)
//	TreeRootPNode, TreeRootTxID, ParentAnchorTxID(s),
//	Author, CommitMessage, Timestamp, GitCommitSHA
func BuildAnchor(params *AnchorParams) (*AnchorResult, error) {
	if err := validateAnchorParams(params); err != nil {
		return nil, err
	}

	// Build the anchor node payload.
	node := &metanet.Node{
		Version:          1,
		Type:             metanet.NodeTypeAnchor,
		Op:               metanet.OpCreate,
		Timestamp:        params.Timestamp,
		TreeRootPNode:    params.TreeRootPNode,
		TreeRootTxID:     params.TreeRootTxID,
		ParentAnchorTxID: params.ParentAnchorTxIDs,
		Author:           params.Author,
		CommitMessage:    params.CommitMessage,
		GitCommitSHA:     params.GitCommitSHA,
	}

	// If updating an existing anchor chain, mark as OpUpdate.
	if params.RefUTXO != nil {
		node.Op = metanet.OpUpdate
	}

	payload, err := metanet.SerializePayload(node)
	if err != nil {
		return nil, fmt.Errorf("chain: serializing anchor payload: %w", err)
	}

	// Use the first parent anchor TxID as the "parent" in the OP_RETURN.
	// For anchors, this links to the previous anchor in the chain.
	var parentTxID []byte
	if len(params.ParentAnchorTxIDs) > 0 {
		parentTxID = params.ParentAnchorTxIDs[0]
	}

	// Build via MutationBatch for consistency with the Push path.
	batch := tx.NewMutationBatch()

	batchOp := tx.BatchNodeOp{
		PubKey:     params.BranchPub,
		ParentTxID: parentTxID,
		Payload:    payload,
		PrivateKey: params.BranchPriv,
	}

	if params.RefUTXO != nil && !params.Force {
		// Subsequent push: spend the existing branch head UTXO.
		batchOp.InputUTXO = params.RefUTXO
		batchOp.Type = tx.BatchOpNodeUpdate
	} else {
		// First push or force push: no existing UTXO to spend.
		batchOp.Type = tx.BatchOpChildCreate
	}

	batch.AddNodeOp(batchOp)
	batch.AddFeeInput(params.FeeUTXO)

	if len(params.ChangeAddr) == 20 {
		batch.SetChange(params.ChangeAddr)
	}

	if params.FeeRate > 0 {
		batch.SetFeeRate(params.FeeRate)
	}

	batchResult, err := batch.Build()
	if err != nil {
		return nil, fmt.Errorf("chain: building anchor TX: %w", err)
	}

	result := &AnchorResult{
		RawTx:      batchResult.RawTx,
		RefUTXO:    batchResult.NodeOps[0].NodeUTXO,
		ChangeUTXO: batchResult.ChangeUTXO,
	}

	return result, nil
}

// ParseAnchor parses OP_RETURN push data from an anchor TX back into a Node.
//
// The pushes should be the 4-element array from the OP_RETURN:
// [MetaFlag, P_branch, PrevAnchorTxID, AnchorPayload].
func ParseAnchor(pushes [][]byte) (*metanet.Node, error) {
	return metanet.ParseNode(pushes)
}

// MultiRefAnchorParams supports pushing multiple refs in one TX.
// Used when pushing branch + tag atomically.
type MultiRefAnchorParams struct {
	Refs       []AnchorParams // Multiple ref updates
	FeeUTXOs   []*tx.UTXO    // Shared fee UTXOs
	ChangeAddr []byte         // 20-byte P2PKH hash for change
	FeeRate    uint64         // sat/KB (0 = default)
}

// MultiRefAnchorResult holds the result for a multi-ref anchor TX.
type MultiRefAnchorResult struct {
	RawTx      []byte              // Unsigned serialized TX
	TxID       []byte              // TX hash (set after signing)
	RefUTXOs   map[string]*tx.UTXO // ref name -> new branch head UTXO
	ChangeUTXO *tx.UTXO            // Change output
}

// BuildMultiRefAnchor builds one TX spending all affected ref UTXOs.
// Each ref gets its own OP_RETURN + P2PKH pair in the batch.
func BuildMultiRefAnchor(params *MultiRefAnchorParams) (*MultiRefAnchorResult, error) {
	if params == nil {
		return nil, fmt.Errorf("chain: multi-ref anchor params is nil")
	}
	if len(params.Refs) == 0 {
		return nil, fmt.Errorf("chain: no refs in multi-ref anchor")
	}
	if len(params.FeeUTXOs) == 0 {
		return nil, fmt.Errorf("chain: no fee UTXOs for multi-ref anchor")
	}

	batch := tx.NewMutationBatch()

	for i, ref := range params.Refs {
		if ref.BranchPub == nil {
			return nil, fmt.Errorf("chain: ref[%d] (%s) missing BranchPub", i, ref.BranchRef)
		}

		// Build anchor payload for this ref.
		node := &metanet.Node{
			Version:          1,
			Type:             metanet.NodeTypeAnchor,
			Op:               metanet.OpCreate,
			Timestamp:        ref.Timestamp,
			TreeRootPNode:    ref.TreeRootPNode,
			TreeRootTxID:     ref.TreeRootTxID,
			ParentAnchorTxID: ref.ParentAnchorTxIDs,
			Author:           ref.Author,
			CommitMessage:    ref.CommitMessage,
			GitCommitSHA:     ref.GitCommitSHA,
		}

		if ref.RefUTXO != nil {
			node.Op = metanet.OpUpdate
		}

		payload, err := metanet.SerializePayload(node)
		if err != nil {
			return nil, fmt.Errorf("chain: serializing anchor payload for ref[%d]: %w", i, err)
		}

		var parentTxID []byte
		if len(ref.ParentAnchorTxIDs) > 0 {
			parentTxID = ref.ParentAnchorTxIDs[0]
		}

		batchOp := tx.BatchNodeOp{
			PubKey:     ref.BranchPub,
			ParentTxID: parentTxID,
			Payload:    payload,
			PrivateKey: ref.BranchPriv,
		}

		if ref.RefUTXO != nil && !ref.Force {
			batchOp.InputUTXO = ref.RefUTXO
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

	batchResult, err := batch.Build()
	if err != nil {
		return nil, fmt.Errorf("chain: building multi-ref anchor TX: %w", err)
	}

	// Map ref names to their new UTXOs.
	refUTXOs := make(map[string]*tx.UTXO)
	for i, ref := range params.Refs {
		refUTXOs[ref.BranchRef] = batchResult.NodeOps[i].NodeUTXO
	}

	return &MultiRefAnchorResult{
		RawTx:      batchResult.RawTx,
		RefUTXOs:   refUTXOs,
		ChangeUTXO: batchResult.ChangeUTXO,
	}, nil
}

// SignAndBroadcastAnchor signs an anchor result and broadcasts it.
// Returns the updated AnchorResult with TxID populated.
func SignAndBroadcastAnchor(ctx context.Context, bc network.BlockchainService, result *AnchorResult, params *AnchorParams) (*AnchorResult, error) {
	if result == nil {
		return nil, fmt.Errorf("chain: anchor result is nil")
	}

	// Collect signing UTXOs in input order.
	var signingUTXOs []*tx.UTXO

	if params.RefUTXO != nil && !params.Force {
		signingUTXOs = append(signingUTXOs, &tx.UTXO{
			TxID:         params.RefUTXO.TxID,
			Vout:         params.RefUTXO.Vout,
			Amount:       params.RefUTXO.Amount,
			ScriptPubKey: params.RefUTXO.ScriptPubKey,
			PrivateKey:   params.BranchPriv,
		})
	}

	signingUTXOs = append(signingUTXOs, params.FeeUTXO)

	mtx := &tx.MetanetTx{
		RawTx: result.RawTx,
	}

	signedHex, err := tx.SignMetanetTx(mtx, signingUTXOs)
	if err != nil {
		return nil, fmt.Errorf("chain: signing anchor TX: %w", err)
	}

	txidHex, err := bc.BroadcastTx(ctx, signedHex)
	if err != nil {
		return nil, fmt.Errorf("chain: broadcasting anchor TX: %w", err)
	}

	txidBytes, err := hex.DecodeString(txidHex)
	if err != nil {
		return nil, fmt.Errorf("chain: invalid anchor broadcast txid: %w", err)
	}

	result.TxID = txidBytes
	result.RawTx = mtx.RawTx

	if result.RefUTXO != nil {
		result.RefUTXO.TxID = txidBytes
	}
	if result.ChangeUTXO != nil {
		result.ChangeUTXO.TxID = txidBytes
	}

	return result, nil
}

// validateAnchorParams checks required fields for BuildAnchor.
func validateAnchorParams(params *AnchorParams) error {
	if params == nil {
		return fmt.Errorf("chain: anchor params is nil")
	}
	if params.BranchPub == nil {
		return fmt.Errorf("chain: missing branch public key")
	}
	if params.BranchPriv == nil {
		return fmt.Errorf("chain: missing branch private key")
	}
	if params.FeeUTXO == nil {
		return fmt.Errorf("chain: missing fee UTXO for anchor")
	}
	return nil
}
