package chain

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tongxiaofeng/libbitfs-go/metanet"
	"github.com/tongxiaofeng/libbitfs-go/tx"
)

func TestBuildAnchorFirstPush(t *testing.T) {
	priv, pub := testKeyPair(t)

	params := &AnchorParams{
		BranchRef:     "refs/heads/main",
		BranchPub:     pub,
		BranchPriv:    priv,
		TreeRootPNode: makePubKeyBytes(0x10),
		TreeRootTxID:  makeTxIDBytes(0x20),
		Author:        "Alice <alice@example.com>",
		CommitMessage: "Initial commit",
		Timestamp:     1700000000,
		GitCommitSHA:  bytes.Repeat([]byte{0xab}, 20),
		RefUTXO:       nil, // First push: no existing ref UTXO
		FeeUTXO:       testFeeUTXO(t),
	}

	result, err := BuildAnchor(params)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.NotEmpty(t, result.RawTx)
	assert.NotNil(t, result.RefUTXO)
	assert.Equal(t, tx.DustLimit, result.RefUTXO.Amount)
}

func TestBuildAnchorSubsequentPush(t *testing.T) {
	priv, pub := testKeyPair(t)
	scriptPubKey, err := tx.BuildP2PKHScript(pub)
	require.NoError(t, err)

	refUTXO := &tx.UTXO{
		TxID:         testTxID(t),
		Vout:         1,
		Amount:       tx.DustLimit,
		ScriptPubKey: scriptPubKey,
		PrivateKey:   priv,
	}

	params := &AnchorParams{
		BranchRef:     "refs/heads/main",
		BranchPub:     pub,
		BranchPriv:    priv,
		TreeRootPNode: makePubKeyBytes(0x10),
		TreeRootTxID:  makeTxIDBytes(0x20),
		ParentAnchorTxIDs: [][]byte{
			makeTxIDBytes(0x30),
		},
		Author:        "Alice <alice@example.com>",
		CommitMessage: "Second commit",
		Timestamp:     1700001000,
		GitCommitSHA:  bytes.Repeat([]byte{0xcd}, 20),
		RefUTXO:       refUTXO,
		FeeUTXO:       testFeeUTXO(t),
	}

	result, err := BuildAnchor(params)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.NotEmpty(t, result.RawTx)
	assert.NotNil(t, result.RefUTXO)
}

func TestBuildAnchorWithMergeParents(t *testing.T) {
	priv, pub := testKeyPair(t)

	params := &AnchorParams{
		BranchRef:  "refs/heads/main",
		BranchPub:  pub,
		BranchPriv: priv,
		ParentAnchorTxIDs: [][]byte{
			makeTxIDBytes(0xaa),
			makeTxIDBytes(0xbb),
			makeTxIDBytes(0xcc),
		},
		TreeRootPNode: makePubKeyBytes(0x50),
		TreeRootTxID:  makeTxIDBytes(0x60),
		Author:        "Bob <bob@example.com>",
		CommitMessage: "Merge branches",
		Timestamp:     1700002000,
		GitCommitSHA:  bytes.Repeat([]byte{0xdd}, 20),
		FeeUTXO:       testFeeUTXO(t),
	}

	result, err := BuildAnchor(params)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.NotEmpty(t, result.RawTx)
}

func TestBuildAnchorForcePush(t *testing.T) {
	priv, pub := testKeyPair(t)
	scriptPubKey, err := tx.BuildP2PKHScript(pub)
	require.NoError(t, err)

	// With Force=true, the RefUTXO should be ignored
	// (creates new anchor chain instead of spending existing).
	refUTXO := &tx.UTXO{
		TxID:         testTxID(t),
		Vout:         1,
		Amount:       tx.DustLimit,
		ScriptPubKey: scriptPubKey,
		PrivateKey:   priv,
	}

	params := &AnchorParams{
		BranchRef:     "refs/heads/main",
		BranchPub:     pub,
		BranchPriv:    priv,
		TreeRootPNode: makePubKeyBytes(0x10),
		TreeRootTxID:  makeTxIDBytes(0x20),
		Author:        "Alice",
		CommitMessage: "Force pushed commit",
		Timestamp:     1700003000,
		GitCommitSHA:  bytes.Repeat([]byte{0xef}, 20),
		RefUTXO:       refUTXO,
		FeeUTXO:       testFeeUTXO(t),
		Force:         true,
	}

	result, err := BuildAnchor(params)
	require.NoError(t, err)
	require.NotNil(t, result)
	// Force push: the ref UTXO is not spent, so the TX has fewer inputs.
	assert.NotEmpty(t, result.RawTx)
}

func TestParseAnchorRoundtrip(t *testing.T) {
	// Build an anchor payload, wrap in OP_RETURN pushes, and parse back.
	anchorNode := &metanet.Node{
		Version:       1,
		Type:          metanet.NodeTypeAnchor,
		Op:            metanet.OpCreate,
		Timestamp:     1700000000,
		TreeRootPNode: makePubKeyBytes(0x10),
		TreeRootTxID:  makeTxIDBytes(0x20),
		ParentAnchorTxID: [][]byte{
			makeTxIDBytes(0x30),
		},
		Author:        "Alice <alice@example.com>",
		CommitMessage: "Initial commit",
		GitCommitSHA:  bytes.Repeat([]byte{0xab}, 20),
		FileMode:      0o100644,
	}

	payload, err := metanet.SerializePayload(anchorNode)
	require.NoError(t, err)

	// Build OP_RETURN push data.
	_, pub := testKeyPair(t)
	pNodeBytes := pub.Compressed()
	parentTxID := makeTxIDBytes(0x30)

	pushes := [][]byte{
		tx.MetaFlagBytes,
		pNodeBytes,
		parentTxID,
		payload,
	}

	// Parse it back.
	parsed, err := ParseAnchor(pushes)
	require.NoError(t, err)
	require.NotNil(t, parsed)

	assert.Equal(t, metanet.NodeTypeAnchor, parsed.Type)
	assert.Equal(t, metanet.OpCreate, parsed.Op)
	assert.Equal(t, uint64(1700000000), parsed.Timestamp)
	assert.Equal(t, anchorNode.TreeRootPNode, parsed.TreeRootPNode)
	assert.Equal(t, anchorNode.TreeRootTxID, parsed.TreeRootTxID)
	require.Len(t, parsed.ParentAnchorTxID, 1)
	assert.Equal(t, anchorNode.ParentAnchorTxID[0], parsed.ParentAnchorTxID[0])
	assert.Equal(t, "Alice <alice@example.com>", parsed.Author)
	assert.Equal(t, "Initial commit", parsed.CommitMessage)
	assert.Equal(t, anchorNode.GitCommitSHA, parsed.GitCommitSHA)
	assert.Equal(t, uint32(0o100644), parsed.FileMode)
}

func TestBuildMultiRefAnchor(t *testing.T) {
	priv1, pub1 := testKeyPair(t)
	priv2, pub2 := testKeyPair(t)

	params := &MultiRefAnchorParams{
		Refs: []AnchorParams{
			{
				BranchRef:     "refs/heads/main",
				BranchPub:     pub1,
				BranchPriv:    priv1,
				TreeRootPNode: makePubKeyBytes(0x10),
				TreeRootTxID:  makeTxIDBytes(0x20),
				Author:        "Alice",
				CommitMessage: "commit on main",
				Timestamp:     1700000000,
				GitCommitSHA:  bytes.Repeat([]byte{0xab}, 20),
			},
			{
				BranchRef:     "refs/tags/v1.0",
				BranchPub:     pub2,
				BranchPriv:    priv2,
				TreeRootPNode: makePubKeyBytes(0x10),
				TreeRootTxID:  makeTxIDBytes(0x20),
				Author:        "Alice",
				CommitMessage: "tag v1.0",
				Timestamp:     1700000000,
				GitCommitSHA:  bytes.Repeat([]byte{0xab}, 20),
			},
		},
		FeeUTXOs: []*tx.UTXO{testFeeUTXO(t)},
	}

	result, err := BuildMultiRefAnchor(params)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.NotEmpty(t, result.RawTx)

	// Should have UTXOs for both refs.
	assert.Len(t, result.RefUTXOs, 2)
	_, hasMain := result.RefUTXOs["refs/heads/main"]
	assert.True(t, hasMain)
	_, hasTag := result.RefUTXOs["refs/tags/v1.0"]
	assert.True(t, hasTag)
}

func TestBuildAnchorMissingBranchKey(t *testing.T) {
	priv, _ := testKeyPair(t)

	// Missing BranchPub.
	params := &AnchorParams{
		BranchRef:  "refs/heads/main",
		BranchPub:  nil,
		BranchPriv: priv,
		FeeUTXO:    testFeeUTXO(t),
	}

	_, err := BuildAnchor(params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing branch public key")

	// Missing BranchPriv.
	_, pub := testKeyPair(t)
	params2 := &AnchorParams{
		BranchRef:  "refs/heads/main",
		BranchPub:  pub,
		BranchPriv: nil,
		FeeUTXO:    testFeeUTXO(t),
	}

	_, err = BuildAnchor(params2)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing branch private key")
}

func TestBuildAnchorMissingFeeUTXO(t *testing.T) {
	priv, pub := testKeyPair(t)

	params := &AnchorParams{
		BranchRef:  "refs/heads/main",
		BranchPub:  pub,
		BranchPriv: priv,
		FeeUTXO:    nil,
	}

	_, err := BuildAnchor(params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing fee UTXO")
}

func TestBuildAnchorNilParams(t *testing.T) {
	_, err := BuildAnchor(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "anchor params is nil")
}

func TestBuildMultiRefAnchorNoRefs(t *testing.T) {
	params := &MultiRefAnchorParams{
		Refs:     []AnchorParams{},
		FeeUTXOs: []*tx.UTXO{testFeeUTXO(t)},
	}

	_, err := BuildMultiRefAnchor(params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no refs")
}

func TestBuildMultiRefAnchorNilParams(t *testing.T) {
	_, err := BuildMultiRefAnchor(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "multi-ref anchor params is nil")
}

func TestBuildMultiRefAnchorNoFeeUTXOs(t *testing.T) {
	_, pub := testKeyPair(t)
	params := &MultiRefAnchorParams{
		Refs: []AnchorParams{
			{
				BranchRef: "refs/heads/main",
				BranchPub: pub,
			},
		},
		FeeUTXOs: []*tx.UTXO{},
	}

	_, err := BuildMultiRefAnchor(params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no fee UTXOs")
}

func TestBuildMultiRefAnchorMissingBranchPub(t *testing.T) {
	params := &MultiRefAnchorParams{
		Refs: []AnchorParams{
			{
				BranchRef: "refs/heads/main",
				BranchPub: nil,
			},
		},
		FeeUTXOs: []*tx.UTXO{testFeeUTXO(t)},
	}

	_, err := BuildMultiRefAnchor(params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing BranchPub")
}

func TestValidateAnchorParams(t *testing.T) {
	priv, pub := testKeyPair(t)
	feeUTXO := testFeeUTXO(t)

	tests := []struct {
		name   string
		params *AnchorParams
		errMsg string
	}{
		{
			name:   "nil params",
			params: nil,
			errMsg: "anchor params is nil",
		},
		{
			name: "missing branch pub",
			params: &AnchorParams{
				BranchPriv: priv,
				FeeUTXO:    feeUTXO,
			},
			errMsg: "missing branch public key",
		},
		{
			name: "missing branch priv",
			params: &AnchorParams{
				BranchPub: pub,
				FeeUTXO:   feeUTXO,
			},
			errMsg: "missing branch private key",
		},
		{
			name: "missing fee UTXO",
			params: &AnchorParams{
				BranchPub:  pub,
				BranchPriv: priv,
			},
			errMsg: "missing fee UTXO",
		},
		{
			name: "valid params",
			params: &AnchorParams{
				BranchPub:  pub,
				BranchPriv: priv,
				FeeUTXO:    feeUTXO,
			},
			errMsg: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateAnchorParams(tt.params)
			if tt.errMsg == "" {
				assert.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			}
		})
	}
}

// --- test helpers ---

// makePubKeyBytes creates a deterministic 33-byte compressed public key stub.
func makePubKeyBytes(seed byte) []byte {
	b := make([]byte, 33)
	b[0] = 0x02 // compressed prefix
	for i := 1; i < 33; i++ {
		b[i] = seed
	}
	return b
}

// makeTxIDBytes creates a deterministic 32-byte TxID.
func makeTxIDBytes(seed byte) []byte {
	b := make([]byte, 32)
	for i := range b {
		b[i] = seed
	}
	return b
}
