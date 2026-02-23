package chain

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"testing"

	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tongxiaofeng/libbitfs/metanet"
	"github.com/tongxiaofeng/libbitfs/network"
	"github.com/tongxiaofeng/libbitfs/tx"
)

// --- test helpers ---

func testKeyPair(t *testing.T) (*ec.PrivateKey, *ec.PublicKey) {
	t.Helper()
	priv, err := ec.NewPrivateKey()
	require.NoError(t, err)
	return priv, priv.PubKey()
}

func testTxID(t *testing.T) []byte {
	t.Helper()
	b := make([]byte, 32)
	_, err := rand.Read(b)
	require.NoError(t, err)
	return b
}

func testFeeUTXO(t *testing.T) *tx.UTXO {
	t.Helper()
	priv, pub := testKeyPair(t)
	scriptPubKey, err := tx.BuildP2PKHScript(pub)
	require.NoError(t, err)
	return &tx.UTXO{
		TxID:         testTxID(t),
		Vout:         0,
		Amount:       100000, // 100k sat
		ScriptPubKey: scriptPubKey,
		PrivateKey:   priv,
	}
}

func testPayload(t *testing.T) []byte {
	t.Helper()
	node := &metanet.Node{
		Version:  1,
		Type:     metanet.NodeTypeFile,
		Op:       metanet.OpCreate,
		MimeType: "text/plain",
		FileSize: 42,
		Access:   metanet.AccessFree,
	}
	payload, err := metanet.SerializePayload(node)
	require.NoError(t, err)
	return payload
}

func successBroadcast(_ context.Context, _ string) (string, error) {
	txid := make([]byte, 32)
	for i := range txid {
		txid[i] = 0xaa
	}
	return hex.EncodeToString(txid), nil
}

func newMockBlockchain(broadcastFn func(context.Context, string) (string, error)) *network.MockBlockchainService {
	return &network.MockBlockchainService{
		BroadcastTxFn: broadcastFn,
	}
}

// --- EncryptFile / DecryptFile tests ---

func TestEncryptFilePrivate(t *testing.T) {
	priv, pub := testKeyPair(t)
	content := []byte("hello, private world!")

	result, err := EncryptFile(content, priv, pub, "private")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, result.Ciphertext)
	require.Len(t, result.KeyHash, 32)

	// Ciphertext should differ from plaintext.
	assert.NotEqual(t, content, result.Ciphertext)

	// Decrypt should recover original content.
	decrypted, err := DecryptFile(result.Ciphertext, priv, pub, result.KeyHash, "private")
	require.NoError(t, err)
	assert.Equal(t, content, decrypted)
}

func TestEncryptFileFree(t *testing.T) {
	priv, pub := testKeyPair(t)
	content := []byte("hello, free world!")

	result, err := EncryptFile(content, priv, pub, "free")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, result.Ciphertext)

	// Free content: anyone can decrypt (using any private key,
	// since FreePrivateKey scalar=1 is used internally).
	decrypted, err := DecryptFile(result.Ciphertext, nil, pub, result.KeyHash, "free")
	require.NoError(t, err)
	assert.Equal(t, content, decrypted)
}

func TestEncryptFileInvalidAccess(t *testing.T) {
	priv, pub := testKeyPair(t)
	_, err := EncryptFile([]byte("test"), priv, pub, "invalid")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown access mode")
}

func TestDecryptFileInvalidAccess(t *testing.T) {
	_, err := DecryptFile([]byte("test"), nil, nil, nil, "invalid")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown access mode")
}

func TestEncryptFileEmptyContent(t *testing.T) {
	priv, pub := testKeyPair(t)
	// Empty content should still work.
	result, err := EncryptFile([]byte{}, priv, pub, "private")
	require.NoError(t, err)

	decrypted, err := DecryptFile(result.Ciphertext, priv, pub, result.KeyHash, "private")
	require.NoError(t, err)
	assert.Equal(t, []byte{}, decrypted)
}

// --- Push tests ---

func TestPushSingleFile(t *testing.T) {
	_, pub := testKeyPair(t)
	mock := newMockBlockchain(successBroadcast)
	writer := NewWriter(mock)

	params := &PushParams{
		Ops: []NodeOp{
			{
				PubKey:  pub,
				Payload: testPayload(t),
				Purpose: "file",
			},
		},
		FeeUTXOs: []*tx.UTXO{testFeeUTXO(t)},
	}

	result, err := writer.Push(context.Background(), params)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, result.TxID, 32)
	assert.NotEmpty(t, result.RawTx)

	// Should have one node UTXO.
	pnodeHex := hex.EncodeToString(pub.Compressed())
	nodeUTXO, ok := result.NodeUTXOs[pnodeHex]
	assert.True(t, ok)
	assert.Equal(t, uint64(tx.DustLimit), nodeUTXO.Amount)
}

func TestPushMultipleFiles(t *testing.T) {
	mock := newMockBlockchain(successBroadcast)
	writer := NewWriter(mock)

	ops := make([]NodeOp, 3)
	pnodes := make([]string, 3)
	for i := range ops {
		_, pub := testKeyPair(t)
		pnodes[i] = hex.EncodeToString(pub.Compressed())
		ops[i] = NodeOp{
			PubKey:  pub,
			Payload: testPayload(t),
			Purpose: "file",
		}
	}

	params := &PushParams{
		Ops:      ops,
		FeeUTXOs: []*tx.UTXO{testFeeUTXO(t)},
	}

	result, err := writer.Push(context.Background(), params)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Should have a node UTXO per op.
	assert.Len(t, result.NodeUTXOs, 3)
	for _, pnode := range pnodes {
		_, ok := result.NodeUTXOs[pnode]
		assert.True(t, ok, "missing UTXO for pnode %s", pnode)
	}
}

func TestPushWithBroadcastError(t *testing.T) {
	mock := newMockBlockchain(func(_ context.Context, _ string) (string, error) {
		return "", fmt.Errorf("network timeout")
	})
	writer := NewWriter(mock)

	_, pub := testKeyPair(t)
	params := &PushParams{
		Ops: []NodeOp{
			{
				PubKey:  pub,
				Payload: testPayload(t),
				Purpose: "file",
			},
		},
		FeeUTXOs: []*tx.UTXO{testFeeUTXO(t)},
	}

	_, err := writer.Push(context.Background(), params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "broadcasting TX")
	assert.Contains(t, err.Error(), "network timeout")
}

func TestPushNoOps(t *testing.T) {
	mock := newMockBlockchain(successBroadcast)
	writer := NewWriter(mock)

	params := &PushParams{
		Ops:      []NodeOp{},
		FeeUTXOs: []*tx.UTXO{testFeeUTXO(t)},
	}

	_, err := writer.Push(context.Background(), params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no operations")
}

func TestPushNoFeeUTXOs(t *testing.T) {
	mock := newMockBlockchain(successBroadcast)
	writer := NewWriter(mock)

	_, pub := testKeyPair(t)
	params := &PushParams{
		Ops: []NodeOp{
			{
				PubKey:  pub,
				Payload: testPayload(t),
				Purpose: "file",
			},
		},
		FeeUTXOs: []*tx.UTXO{},
	}

	_, err := writer.Push(context.Background(), params)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no fee UTXOs")
}

func TestPushNilParams(t *testing.T) {
	mock := newMockBlockchain(successBroadcast)
	writer := NewWriter(mock)

	_, err := writer.Push(context.Background(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "push params is nil")
}

func TestPushWithExistingUTXO(t *testing.T) {
	// Test update scenario: op has an InputUTXO to spend.
	mock := newMockBlockchain(successBroadcast)
	writer := NewWriter(mock)

	priv, pub := testKeyPair(t)
	scriptPubKey, err := tx.BuildP2PKHScript(pub)
	require.NoError(t, err)

	inputUTXO := &tx.UTXO{
		TxID:         testTxID(t),
		Vout:         1,
		Amount:       tx.DustLimit,
		ScriptPubKey: scriptPubKey,
		PrivateKey:   priv,
	}

	params := &PushParams{
		Ops: []NodeOp{
			{
				PubKey:    pub,
				PrivKey:   priv,
				Payload:   testPayload(t),
				InputUTXO: inputUTXO,
				Purpose:   "file",
			},
		},
		FeeUTXOs: []*tx.UTXO{testFeeUTXO(t)},
	}

	result, err := writer.Push(context.Background(), params)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, result.TxID, 32)

	pnodeHex := hex.EncodeToString(pub.Compressed())
	_, ok := result.NodeUTXOs[pnodeHex]
	assert.True(t, ok)
}

func TestParseAccessModes(t *testing.T) {
	tests := []struct {
		input    string
		expected int
		hasErr   bool
	}{
		{"free", 1, false},
		{"private", 0, false},
		{"paid", 2, false},
		{"", 0, true},
		{"unknown", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result, err := parseAccess(tt.input)
			if tt.hasErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, int(result))
			}
		})
	}
}

func TestEncryptDecryptRoundtrip_LargeContent(t *testing.T) {
	priv, pub := testKeyPair(t)

	// 64KB of content.
	content := make([]byte, 65536)
	_, err := rand.Read(content)
	require.NoError(t, err)

	result, err := EncryptFile(content, priv, pub, "private")
	require.NoError(t, err)

	decrypted, err := DecryptFile(result.Ciphertext, priv, pub, result.KeyHash, "private")
	require.NoError(t, err)
	assert.True(t, bytes.Equal(content, decrypted))
}
