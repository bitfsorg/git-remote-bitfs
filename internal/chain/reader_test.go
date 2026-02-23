package chain

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"testing"

	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tongxiaofeng/libbitfs/metanet"
	"github.com/tongxiaofeng/libbitfs/method42"
)

// --- mock chain reader ---

// mockChainReader stores nodes indexed by hex key (txid or pubkey hex).
type mockChainReader struct {
	nodesByTxID   map[string]*metanet.Node // txid hex -> node
	nodesByPubKey map[string]*metanet.Node // pubkey hex -> node
	content       map[string][]byte        // pubkey hex -> encrypted content
}

func newMockChainReader() *mockChainReader {
	return &mockChainReader{
		nodesByTxID:   make(map[string]*metanet.Node),
		nodesByPubKey: make(map[string]*metanet.Node),
		content:       make(map[string][]byte),
	}
}

func (m *mockChainReader) GetNodeByTxID(_ context.Context, txID []byte) (*metanet.Node, error) {
	key := hex.EncodeToString(txID)
	node, ok := m.nodesByTxID[key]
	if !ok {
		return nil, fmt.Errorf("node not found for txid: %s", key)
	}
	return node, nil
}

func (m *mockChainReader) GetNodeByPubKey(_ context.Context, pNode []byte) (*metanet.Node, error) {
	key := hex.EncodeToString(pNode)
	node, ok := m.nodesByPubKey[key]
	if !ok {
		return nil, fmt.Errorf("node not found for pubkey: %s", key)
	}
	return node, nil
}

func (m *mockChainReader) GetEncryptedContent(_ context.Context, node *metanet.Node) ([]byte, error) {
	// First check if the node has inline encrypted payload.
	if len(node.EncPayload) > 0 {
		return node.EncPayload, nil
	}
	// Fall back to content stored by PNode hex key.
	key := hex.EncodeToString(node.PNode)
	content, ok := m.content[key]
	if !ok {
		return nil, fmt.Errorf("no content for node %s", key)
	}
	return content, nil
}

// addNode stores a node indexed by both TxID hex and PNode hex.
func (m *mockChainReader) addNode(node *metanet.Node) {
	if len(node.TxID) > 0 {
		m.nodesByTxID[hex.EncodeToString(node.TxID)] = node
	}
	if len(node.PNode) > 0 {
		m.nodesByPubKey[hex.EncodeToString(node.PNode)] = node
	}
}

// setContent stores encrypted content for a node (by PNode hex).
func (m *mockChainReader) setContent(pNode []byte, ciphertext []byte) {
	m.content[hex.EncodeToString(pNode)] = ciphertext
}

// --- helper functions ---

// encryptTestContent encrypts content with the given key pair and access.
func encryptTestContent(t *testing.T, content []byte, priv *ec.PrivateKey, pub *ec.PublicKey, access method42.Access) (*method42.EncryptResult, error) {
	t.Helper()
	return method42.Encrypt(content, priv, pub, access)
}

// --- anchor tests ---

func TestReadAnchor(t *testing.T) {
	mock := newMockChainReader()
	reader := NewReader(mock)

	anchorTxID := makeTxIDBytes(0x01)
	treeRootPNode := makePubKeyBytes(0x10)
	treeRootTxID := makeTxIDBytes(0x20)
	parentAnchorTxID := makeTxIDBytes(0x30)

	anchorNode := &metanet.Node{
		TxID:          anchorTxID,
		PNode:         makePubKeyBytes(0x02),
		Type:          metanet.NodeTypeAnchor,
		Version:       1,
		Op:            metanet.OpCreate,
		TreeRootPNode: treeRootPNode,
		TreeRootTxID:  treeRootTxID,
		ParentAnchorTxID: [][]byte{
			parentAnchorTxID,
		},
		Author:        "Alice <alice@example.com>",
		CommitMessage: "Initial commit",
		Timestamp:     1700000000,
		GitCommitSHA:  bytes.Repeat([]byte{0xab}, 20),
	}
	mock.addNode(anchorNode)

	ctx := context.Background()
	info, err := reader.ReadAnchor(ctx, anchorTxID)
	require.NoError(t, err)
	require.NotNil(t, info)

	assert.Equal(t, anchorTxID, info.TxID)
	assert.Equal(t, anchorNode.PNode, info.PNode)
	assert.Equal(t, treeRootPNode, info.TreeRootPNode)
	assert.Equal(t, treeRootTxID, info.TreeRootTxID)
	assert.Equal(t, "Alice <alice@example.com>", info.Author)
	assert.Equal(t, "Initial commit", info.CommitMessage)
	assert.Equal(t, uint64(1700000000), info.Timestamp)
	assert.Equal(t, bytes.Repeat([]byte{0xab}, 20), info.GitCommitSHA)
	require.Len(t, info.ParentAnchorTxIDs, 1)
	assert.Equal(t, parentAnchorTxID, info.ParentAnchorTxIDs[0])
}

func TestReadAnchorEmptyTxID(t *testing.T) {
	reader := NewReader(newMockChainReader())
	_, err := reader.ReadAnchor(context.Background(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty txID")
}

func TestReadAnchorNotFound(t *testing.T) {
	reader := NewReader(newMockChainReader())
	_, err := reader.ReadAnchor(context.Background(), makeTxIDBytes(0xff))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reading node")
}

func TestReadAnchorWrongType(t *testing.T) {
	mock := newMockChainReader()
	reader := NewReader(mock)

	txID := makeTxIDBytes(0x01)
	mock.addNode(&metanet.Node{
		TxID:  txID,
		PNode: makePubKeyBytes(0x02),
		Type:  metanet.NodeTypeFile,
	})

	_, err := reader.ReadAnchor(context.Background(), txID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected anchor node")
}

// --- walk anchor chain tests ---

func TestWalkAnchorChain(t *testing.T) {
	mock := newMockChainReader()
	reader := NewReader(mock)

	// Build a chain of 3 anchors: anchor3 -> anchor2 -> anchor1 (genesis)
	txID1 := makeTxIDBytes(0x01)
	txID2 := makeTxIDBytes(0x02)
	txID3 := makeTxIDBytes(0x03)

	// Genesis anchor (no parent)
	mock.addNode(&metanet.Node{
		TxID:          txID1,
		PNode:         makePubKeyBytes(0x11),
		Type:          metanet.NodeTypeAnchor,
		CommitMessage: "commit 1",
		Timestamp:     1000,
	})

	// Second anchor
	mock.addNode(&metanet.Node{
		TxID:             txID2,
		PNode:            makePubKeyBytes(0x12),
		Type:             metanet.NodeTypeAnchor,
		ParentAnchorTxID: [][]byte{txID1},
		CommitMessage:    "commit 2",
		Timestamp:        2000,
	})

	// Third anchor (latest)
	mock.addNode(&metanet.Node{
		TxID:             txID3,
		PNode:            makePubKeyBytes(0x13),
		Type:             metanet.NodeTypeAnchor,
		ParentAnchorTxID: [][]byte{txID2},
		CommitMessage:    "commit 3",
		Timestamp:        3000,
	})

	ctx := context.Background()
	anchors, err := reader.WalkAnchorChain(ctx, txID3, nil)
	require.NoError(t, err)
	require.Len(t, anchors, 3)

	// Should be in reverse chronological order.
	assert.Equal(t, "commit 3", anchors[0].CommitMessage)
	assert.Equal(t, "commit 2", anchors[1].CommitMessage)
	assert.Equal(t, "commit 1", anchors[2].CommitMessage)
}

func TestWalkAnchorChainWithStop(t *testing.T) {
	mock := newMockChainReader()
	reader := NewReader(mock)

	txID1 := makeTxIDBytes(0x01)
	txID2 := makeTxIDBytes(0x02)
	txID3 := makeTxIDBytes(0x03)

	mock.addNode(&metanet.Node{
		TxID:          txID1,
		PNode:         makePubKeyBytes(0x11),
		Type:          metanet.NodeTypeAnchor,
		CommitMessage: "commit 1",
	})

	mock.addNode(&metanet.Node{
		TxID:             txID2,
		PNode:            makePubKeyBytes(0x12),
		Type:             metanet.NodeTypeAnchor,
		ParentAnchorTxID: [][]byte{txID1},
		CommitMessage:    "commit 2",
	})

	mock.addNode(&metanet.Node{
		TxID:             txID3,
		PNode:            makePubKeyBytes(0x13),
		Type:             metanet.NodeTypeAnchor,
		ParentAnchorTxID: [][]byte{txID2},
		CommitMessage:    "commit 3",
	})

	// Walk from txID3, stop at txID2 (exclusive -- txID2 should NOT be included).
	ctx := context.Background()
	anchors, err := reader.WalkAnchorChain(ctx, txID3, txID2)
	require.NoError(t, err)
	// Since stopTxID check happens before reading, and we start at txID3 (not txID2),
	// walk reads txID3, then its parent is txID2 which matches stopTxID, so we stop.
	require.Len(t, anchors, 1)
	assert.Equal(t, "commit 3", anchors[0].CommitMessage)
}

func TestWalkAnchorChainEmptyTxID(t *testing.T) {
	reader := NewReader(newMockChainReader())
	_, err := reader.WalkAnchorChain(context.Background(), nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty latestTxID")
}

func TestWalkAnchorChainSingle(t *testing.T) {
	mock := newMockChainReader()
	reader := NewReader(mock)

	txID := makeTxIDBytes(0x01)
	mock.addNode(&metanet.Node{
		TxID:          txID,
		PNode:         makePubKeyBytes(0x11),
		Type:          metanet.NodeTypeAnchor,
		CommitMessage: "genesis",
	})

	anchors, err := reader.WalkAnchorChain(context.Background(), txID, nil)
	require.NoError(t, err)
	require.Len(t, anchors, 1)
	assert.Equal(t, "genesis", anchors[0].CommitMessage)
}

// --- file node tests ---

func TestReadFileNodeFree(t *testing.T) {
	mock := newMockChainReader()
	reader := NewReader(mock)

	// Create a file node with free access.
	priv, pub := testKeyPair(t)
	content := []byte("hello free world")

	encResult, err := encryptTestContent(t, content, priv, pub, method42.AccessFree)
	require.NoError(t, err)

	txID := testTxID(t)
	fileNode := &metanet.Node{
		TxID:     txID,
		PNode:    pub.Compressed(),
		Type:     metanet.NodeTypeFile,
		Access:   metanet.AccessFree,
		KeyHash:  encResult.KeyHash,
		FileSize: uint64(len(content)),
		FileMode: 0o100644,
	}
	mock.addNode(fileNode)
	mock.setContent(pub.Compressed(), encResult.Ciphertext)

	ctx := context.Background()
	result, err := reader.ReadFileNode(ctx, txID, nil, "hello.txt")
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "hello.txt", result.Path)
	assert.Equal(t, content, result.Content)
	assert.Equal(t, uint32(0o100644), result.Mode)
}

func TestReadFileNodePrivate(t *testing.T) {
	mock := newMockChainReader()
	reader := NewReader(mock)

	priv, pub := testKeyPair(t)
	content := []byte("hello private world")

	encResult, err := encryptTestContent(t, content, priv, pub, method42.AccessPrivate)
	require.NoError(t, err)

	txID := testTxID(t)
	fileNode := &metanet.Node{
		TxID:     txID,
		PNode:    pub.Compressed(),
		Type:     metanet.NodeTypeFile,
		Access:   metanet.AccessPrivate,
		KeyHash:  encResult.KeyHash,
		FileSize: uint64(len(content)),
	}
	mock.addNode(fileNode)
	mock.setContent(pub.Compressed(), encResult.Ciphertext)

	ctx := context.Background()
	result, err := reader.ReadFileNode(ctx, txID, priv, "secret.txt")
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Equal(t, "secret.txt", result.Path)
	assert.Equal(t, content, result.Content)
	assert.Equal(t, uint32(0o100644), result.Mode) // Default mode
}

func TestReadFileNodePrivateWithoutKey(t *testing.T) {
	mock := newMockChainReader()
	reader := NewReader(mock)

	priv, pub := testKeyPair(t)
	content := []byte("secret")

	encResult, err := encryptTestContent(t, content, priv, pub, method42.AccessPrivate)
	require.NoError(t, err)

	txID := testTxID(t)
	fileNode := &metanet.Node{
		TxID:    txID,
		PNode:   pub.Compressed(),
		Type:    metanet.NodeTypeFile,
		Access:  metanet.AccessPrivate,
		KeyHash: encResult.KeyHash,
	}
	mock.addNode(fileNode)
	mock.setContent(pub.Compressed(), encResult.Ciphertext)

	// No private key provided -- should fail for private access.
	_, err = reader.ReadFileNode(context.Background(), txID, nil, "secret.txt")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "private file requires private key")
}

func TestReadFileNodeCustomMode(t *testing.T) {
	mock := newMockChainReader()
	reader := NewReader(mock)

	priv, pub := testKeyPair(t)
	content := []byte("#!/bin/bash\necho hello")

	encResult, err := encryptTestContent(t, content, priv, pub, method42.AccessFree)
	require.NoError(t, err)

	txID := testTxID(t)
	fileNode := &metanet.Node{
		TxID:     txID,
		PNode:    pub.Compressed(),
		Type:     metanet.NodeTypeFile,
		Access:   metanet.AccessFree,
		KeyHash:  encResult.KeyHash,
		FileMode: 0o100755, // Executable
	}
	mock.addNode(fileNode)
	mock.setContent(pub.Compressed(), encResult.Ciphertext)

	result, err := reader.ReadFileNode(context.Background(), txID, nil, "run.sh")
	require.NoError(t, err)
	assert.Equal(t, uint32(0o100755), result.Mode)
}

// --- tree tests ---

func TestReadTreeSimple(t *testing.T) {
	mock := newMockChainReader()
	reader := NewReader(mock)

	// Create two file nodes.
	priv1, pub1 := testKeyPair(t)
	priv2, pub2 := testKeyPair(t)

	content1 := []byte("file one content")
	content2 := []byte("file two content")

	enc1, err := encryptTestContent(t, content1, priv1, pub1, method42.AccessFree)
	require.NoError(t, err)
	enc2, err := encryptTestContent(t, content2, priv2, pub2, method42.AccessFree)
	require.NoError(t, err)

	fileTxID1 := testTxID(t)
	fileTxID2 := testTxID(t)

	fileNode1 := &metanet.Node{
		TxID:     fileTxID1,
		PNode:    pub1.Compressed(),
		Type:     metanet.NodeTypeFile,
		Access:   metanet.AccessFree,
		KeyHash:  enc1.KeyHash,
		FileSize: uint64(len(content1)),
	}

	fileNode2 := &metanet.Node{
		TxID:     fileTxID2,
		PNode:    pub2.Compressed(),
		Type:     metanet.NodeTypeFile,
		Access:   metanet.AccessFree,
		KeyHash:  enc2.KeyHash,
		FileSize: uint64(len(content2)),
	}

	mock.addNode(fileNode1)
	mock.addNode(fileNode2)
	mock.setContent(pub1.Compressed(), enc1.Ciphertext)
	mock.setContent(pub2.Compressed(), enc2.Ciphertext)

	// Create a directory node with two children.
	dirTxID := testTxID(t)
	_, dirPub := testKeyPair(t)

	dirNode := &metanet.Node{
		TxID:  dirTxID,
		PNode: dirPub.Compressed(),
		Type:  metanet.NodeTypeDir,
		Children: []metanet.ChildEntry{
			{
				Index:  0,
				Name:   "hello.txt",
				Type:   metanet.NodeTypeFile,
				PubKey: pub1.Compressed(),
			},
			{
				Index:  1,
				Name:   "world.txt",
				Type:   metanet.NodeTypeFile,
				PubKey: pub2.Compressed(),
			},
		},
	}
	mock.addNode(dirNode)

	ctx := context.Background()
	results, err := reader.ReadTree(ctx, dirPub.Compressed(), dirTxID, nil)
	require.NoError(t, err)
	require.Len(t, results, 2)

	// Verify file contents (order matches children list).
	assert.Equal(t, "hello.txt", results[0].Path)
	assert.Equal(t, content1, results[0].Content)
	assert.Equal(t, "world.txt", results[1].Path)
	assert.Equal(t, content2, results[1].Content)
}

func TestReadTreeNested(t *testing.T) {
	mock := newMockChainReader()
	reader := NewReader(mock)

	// Create a file node in a subdirectory.
	priv1, pub1 := testKeyPair(t)
	content1 := []byte("nested file content")

	enc1, err := encryptTestContent(t, content1, priv1, pub1, method42.AccessFree)
	require.NoError(t, err)

	fileTxID := testTxID(t)
	fileNode := &metanet.Node{
		TxID:    fileTxID,
		PNode:   pub1.Compressed(),
		Type:    metanet.NodeTypeFile,
		Access:  metanet.AccessFree,
		KeyHash: enc1.KeyHash,
	}
	mock.addNode(fileNode)
	mock.setContent(pub1.Compressed(), enc1.Ciphertext)

	// Create subdirectory.
	subDirTxID := testTxID(t)
	_, subDirPub := testKeyPair(t)

	subDirNode := &metanet.Node{
		TxID:  subDirTxID,
		PNode: subDirPub.Compressed(),
		Type:  metanet.NodeTypeDir,
		Children: []metanet.ChildEntry{
			{
				Index:  0,
				Name:   "nested.txt",
				Type:   metanet.NodeTypeFile,
				PubKey: pub1.Compressed(),
			},
		},
	}
	mock.addNode(subDirNode)

	// Create root directory with subdirectory child.
	rootTxID := testTxID(t)
	_, rootPub := testKeyPair(t)

	rootNode := &metanet.Node{
		TxID:  rootTxID,
		PNode: rootPub.Compressed(),
		Type:  metanet.NodeTypeDir,
		Children: []metanet.ChildEntry{
			{
				Index:  0,
				Name:   "subdir",
				Type:   metanet.NodeTypeDir,
				PubKey: subDirPub.Compressed(),
			},
		},
	}
	mock.addNode(rootNode)

	ctx := context.Background()
	results, err := reader.ReadTree(ctx, rootPub.Compressed(), rootTxID, nil)
	require.NoError(t, err)
	require.Len(t, results, 1)

	assert.Equal(t, "subdir/nested.txt", results[0].Path)
	assert.Equal(t, content1, results[0].Content)
}

func TestReadTreeEmpty(t *testing.T) {
	mock := newMockChainReader()
	reader := NewReader(mock)

	dirTxID := testTxID(t)
	_, dirPub := testKeyPair(t)

	dirNode := &metanet.Node{
		TxID:  dirTxID,
		PNode: dirPub.Compressed(),
		Type:  metanet.NodeTypeDir,
	}
	mock.addNode(dirNode)

	results, err := reader.ReadTree(context.Background(), dirPub.Compressed(), dirTxID, nil)
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestReadTreeEmptyTxID(t *testing.T) {
	reader := NewReader(newMockChainReader())
	_, err := reader.ReadTree(context.Background(), nil, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty directory txID")
}

func TestReadTreeNotADirectory(t *testing.T) {
	mock := newMockChainReader()
	reader := NewReader(mock)

	txID := testTxID(t)
	mock.addNode(&metanet.Node{
		TxID:  txID,
		PNode: makePubKeyBytes(0x01),
		Type:  metanet.NodeTypeFile,
	})

	_, err := reader.ReadTree(context.Background(), makePubKeyBytes(0x01), txID, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected directory node")
}

func TestReadTreeSkipsLinks(t *testing.T) {
	mock := newMockChainReader()
	reader := NewReader(mock)

	// Create one file and one link child.
	priv, pub := testKeyPair(t)
	content := []byte("file content")

	enc, err := encryptTestContent(t, content, priv, pub, method42.AccessFree)
	require.NoError(t, err)

	fileTxID := testTxID(t)
	fileNode := &metanet.Node{
		TxID:    fileTxID,
		PNode:   pub.Compressed(),
		Type:    metanet.NodeTypeFile,
		Access:  metanet.AccessFree,
		KeyHash: enc.KeyHash,
	}
	mock.addNode(fileNode)
	mock.setContent(pub.Compressed(), enc.Ciphertext)

	dirTxID := testTxID(t)
	_, dirPub := testKeyPair(t)

	dirNode := &metanet.Node{
		TxID:  dirTxID,
		PNode: dirPub.Compressed(),
		Type:  metanet.NodeTypeDir,
		Children: []metanet.ChildEntry{
			{
				Index:  0,
				Name:   "file.txt",
				Type:   metanet.NodeTypeFile,
				PubKey: pub.Compressed(),
			},
			{
				Index:  1,
				Name:   "link-to-file",
				Type:   metanet.NodeTypeLink,
				PubKey: makePubKeyBytes(0xff),
			},
		},
	}
	mock.addNode(dirNode)

	results, err := reader.ReadTree(context.Background(), dirPub.Compressed(), dirTxID, nil)
	require.NoError(t, err)
	// Link should be skipped, only file returned.
	require.Len(t, results, 1)
	assert.Equal(t, "file.txt", results[0].Path)
}
