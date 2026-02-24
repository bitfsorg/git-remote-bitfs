package helper

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tongxiaofeng/git-remote-bitfs/internal/chain"
	"github.com/tongxiaofeng/git-remote-bitfs/internal/utxo"
	"github.com/tongxiaofeng/libbitfs-go/metanet"
	"github.com/tongxiaofeng/libbitfs-go/method42"
)

// --- mock chain reader for import tests ---

type mockChainReader struct {
	nodesByTxID   map[string]*metanet.Node
	nodesByPubKey map[string]*metanet.Node
	content       map[string][]byte
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
	if len(node.EncPayload) > 0 {
		return node.EncPayload, nil
	}
	key := hex.EncodeToString(node.PNode)
	content, ok := m.content[key]
	if !ok {
		return nil, fmt.Errorf("no content for node %s", key)
	}
	return content, nil
}

func (m *mockChainReader) addNode(node *metanet.Node) {
	if len(node.TxID) > 0 {
		m.nodesByTxID[hex.EncodeToString(node.TxID)] = node
	}
	if len(node.PNode) > 0 {
		m.nodesByPubKey[hex.EncodeToString(node.PNode)] = node
	}
}

func (m *mockChainReader) setContent(pNode []byte, ciphertext []byte) {
	m.content[hex.EncodeToString(pNode)] = ciphertext
}

// --- test helpers ---

func testKeyPairImport(t *testing.T) (*ec.PrivateKey, *ec.PublicKey) {
	t.Helper()
	priv, err := ec.NewPrivateKey()
	require.NoError(t, err)
	return priv, priv.PubKey()
}

func makeTxIDHex(seed byte) string {
	b := make([]byte, 32)
	for i := range b {
		b[i] = seed
	}
	return hex.EncodeToString(b)
}

func makeTxIDImport(seed byte) []byte {
	b := make([]byte, 32)
	for i := range b {
		b[i] = seed
	}
	return b
}

func makePubKeyImport(seed byte) []byte {
	b := make([]byte, 33)
	b[0] = 0x02
	for i := 1; i < 33; i++ {
		b[i] = seed
	}
	return b
}

// setupImportHelper creates a helper configured for import testing with
// a mock chain reader and UTXO store. Returns (helper, stdout, stderr, tmpDir).
func setupImportHelper(t *testing.T, input string, mock chain.ChainReader, refUTXOs []utxo.RefUTXO) (*Helper, *bytes.Buffer, *bytes.Buffer, string) {
	t.Helper()

	// Create a temp directory for the UTXO store.
	tmpDir := t.TempDir()
	gitDir := filepath.Join(tmpDir, ".git")
	bitfsDir := filepath.Join(gitDir, "bitfs")
	require.NoError(t, os.MkdirAll(bitfsDir, 0o755))

	// Write UTXO state if ref UTXOs are provided.
	if len(refUTXOs) > 0 {
		state := utxo.State{
			RefUTXOs: refUTXOs,
		}
		data, err := json.MarshalIndent(state, "", "  ")
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(bitfsDir, "state.json"), data, 0o644))
	}

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cfg := HelperConfig{
		RemoteName:  "origin",
		RemoteURL:   "bitfs://alice@bitfs.org",
		GitDir:      gitDir,
		ChainReader: mock,
	}
	h, err := New(cfg, strings.NewReader(input), stdout, stderr)
	require.NoError(t, err)

	return h, stdout, stderr, tmpDir
}

// --- import tests ---

func TestImportSingleRef(t *testing.T) {
	mock := newMockChainReader()

	// Create a file node with free access.
	priv, pub := testKeyPairImport(t)
	content := []byte("hello world")

	encResult, err := method42.Encrypt(content, priv, pub, method42.AccessFree)
	require.NoError(t, err)

	fileTxID := makeTxIDImport(0x10)
	fileNode := &metanet.Node{
		TxID:     fileTxID,
		PNode:    pub.Compressed(),
		Type:     metanet.NodeTypeFile,
		Access:   metanet.AccessFree,
		KeyHash:  encResult.KeyHash,
		FileSize: uint64(len(content)),
	}
	mock.addNode(fileNode)
	mock.setContent(pub.Compressed(), encResult.Ciphertext)

	// Create a directory containing the file.
	dirTxID := makeTxIDImport(0x20)
	_, dirPub := testKeyPairImport(t)
	dirNode := &metanet.Node{
		TxID:  dirTxID,
		PNode: dirPub.Compressed(),
		Type:  metanet.NodeTypeDir,
		Children: []metanet.ChildEntry{
			{
				Index:  0,
				Name:   "hello.txt",
				Type:   metanet.NodeTypeFile,
				PubKey: pub.Compressed(),
			},
		},
	}
	mock.addNode(dirNode)

	// Create an anchor pointing to the directory.
	anchorTxID := makeTxIDImport(0x01)
	anchorNode := &metanet.Node{
		TxID:          anchorTxID,
		PNode:         makePubKeyImport(0x02),
		Type:          metanet.NodeTypeAnchor,
		TreeRootPNode: dirPub.Compressed(),
		TreeRootTxID:  dirTxID,
		Author:        "Alice <alice@example.com> 1700000000 +0000",
		CommitMessage: "Initial commit",
		Timestamp:     1700000000,
	}
	mock.addNode(anchorNode)

	// Set up UTXO store with ref pointing to the anchor.
	refUTXOs := []utxo.RefUTXO{
		{
			Ref:        "refs/heads/main",
			AnchorTxID: hex.EncodeToString(anchorTxID),
			TxID:       makeTxIDHex(0x30),
			Vout:       1,
			Amount:     546,
		},
	}

	input := "import refs/heads/main\n\n"
	h, stdout, _, _ := setupImportHelper(t, input, mock, refUTXOs)

	err = h.Run()
	require.NoError(t, err)

	output := stdout.String()

	// Verify fast-import stream contains expected elements.
	assert.Contains(t, output, "blob\n")
	assert.Contains(t, output, "data 11\n")
	assert.Contains(t, output, "hello world")
	assert.Contains(t, output, "commit refs/heads/main\n")
	assert.Contains(t, output, "Initial commit")
	assert.Contains(t, output, "M 100644")
	assert.Contains(t, output, "hello.txt")
	assert.Contains(t, output, "done\n")
}

func TestImportMultipleRefs(t *testing.T) {
	mock := newMockChainReader()

	// Create file for ref 1.
	priv1, pub1 := testKeyPairImport(t)
	content1 := []byte("main content")
	enc1, err := method42.Encrypt(content1, priv1, pub1, method42.AccessFree)
	require.NoError(t, err)

	fileTxID1 := makeTxIDImport(0x10)
	mock.addNode(&metanet.Node{
		TxID:    fileTxID1,
		PNode:   pub1.Compressed(),
		Type:    metanet.NodeTypeFile,
		Access:  metanet.AccessFree,
		KeyHash: enc1.KeyHash,
	})
	mock.setContent(pub1.Compressed(), enc1.Ciphertext)

	// Dir and anchor for ref 1.
	dirTxID1 := makeTxIDImport(0x20)
	_, dirPub1 := testKeyPairImport(t)
	mock.addNode(&metanet.Node{
		TxID:  dirTxID1,
		PNode: dirPub1.Compressed(),
		Type:  metanet.NodeTypeDir,
		Children: []metanet.ChildEntry{
			{Name: "main.txt", Type: metanet.NodeTypeFile, PubKey: pub1.Compressed()},
		},
	})

	anchorTxID1 := makeTxIDImport(0x01)
	mock.addNode(&metanet.Node{
		TxID:          anchorTxID1,
		PNode:         makePubKeyImport(0x02),
		Type:          metanet.NodeTypeAnchor,
		TreeRootPNode: dirPub1.Compressed(),
		TreeRootTxID:  dirTxID1,
		Author:        "Alice <a@b.com> 1000 +0000",
		CommitMessage: "commit on main",
	})

	// Create file for ref 2.
	priv2, pub2 := testKeyPairImport(t)
	content2 := []byte("dev content")
	enc2, err := method42.Encrypt(content2, priv2, pub2, method42.AccessFree)
	require.NoError(t, err)

	fileTxID2 := makeTxIDImport(0x11)
	mock.addNode(&metanet.Node{
		TxID:    fileTxID2,
		PNode:   pub2.Compressed(),
		Type:    metanet.NodeTypeFile,
		Access:  metanet.AccessFree,
		KeyHash: enc2.KeyHash,
	})
	mock.setContent(pub2.Compressed(), enc2.Ciphertext)

	dirTxID2 := makeTxIDImport(0x21)
	_, dirPub2 := testKeyPairImport(t)
	mock.addNode(&metanet.Node{
		TxID:  dirTxID2,
		PNode: dirPub2.Compressed(),
		Type:  metanet.NodeTypeDir,
		Children: []metanet.ChildEntry{
			{Name: "dev.txt", Type: metanet.NodeTypeFile, PubKey: pub2.Compressed()},
		},
	})

	anchorTxID2 := makeTxIDImport(0x03)
	mock.addNode(&metanet.Node{
		TxID:          anchorTxID2,
		PNode:         makePubKeyImport(0x04),
		Type:          metanet.NodeTypeAnchor,
		TreeRootPNode: dirPub2.Compressed(),
		TreeRootTxID:  dirTxID2,
		Author:        "Bob <b@b.com> 2000 +0000",
		CommitMessage: "commit on dev",
	})

	refUTXOs := []utxo.RefUTXO{
		{
			Ref:        "refs/heads/main",
			AnchorTxID: hex.EncodeToString(anchorTxID1),
			TxID:       makeTxIDHex(0x30),
			Vout:       1,
			Amount:     546,
		},
		{
			Ref:        "refs/heads/dev",
			AnchorTxID: hex.EncodeToString(anchorTxID2),
			TxID:       makeTxIDHex(0x31),
			Vout:       1,
			Amount:     546,
		},
	}

	// Git sends multiple import lines followed by blank.
	input := "import refs/heads/main\nimport refs/heads/dev\n\n"
	h, stdout, _, _ := setupImportHelper(t, input, mock, refUTXOs)

	err = h.Run()
	require.NoError(t, err)

	output := stdout.String()
	assert.Contains(t, output, "commit refs/heads/main\n")
	assert.Contains(t, output, "commit refs/heads/dev\n")
	assert.Contains(t, output, "main.txt")
	assert.Contains(t, output, "dev.txt")
	assert.Contains(t, output, "done\n")
}

func TestImportEmptyRef(t *testing.T) {
	mock := newMockChainReader()

	// No ref UTXOs -- this ref doesn't exist on chain.
	input := "import refs/heads/main\n\n"
	h, stdout, _, _ := setupImportHelper(t, input, mock, nil)

	err := h.Run()
	require.NoError(t, err)

	output := stdout.String()
	// Should only contain "done" since there are no anchors.
	assert.Equal(t, "done\n", output)
}

func TestImportNoRefUTXO(t *testing.T) {
	mock := newMockChainReader()

	// UTXO store exists but doesn't have the requested ref.
	refUTXOs := []utxo.RefUTXO{
		{
			Ref:        "refs/heads/other",
			AnchorTxID: makeTxIDHex(0x01),
			TxID:       makeTxIDHex(0x30),
			Vout:       1,
			Amount:     546,
		},
	}

	input := "import refs/heads/main\n\n"
	h, stdout, _, _ := setupImportHelper(t, input, mock, refUTXOs)

	err := h.Run()
	require.NoError(t, err)

	output := stdout.String()
	assert.Equal(t, "done\n", output)
}

func TestImportAnchorChain(t *testing.T) {
	mock := newMockChainReader()

	// Create two commits (anchors) linked: anchor2 -> anchor1
	priv1, pub1 := testKeyPairImport(t)
	content1 := []byte("initial")
	enc1, err := method42.Encrypt(content1, priv1, pub1, method42.AccessFree)
	require.NoError(t, err)

	fileTxID1 := makeTxIDImport(0x10)
	mock.addNode(&metanet.Node{
		TxID:    fileTxID1,
		PNode:   pub1.Compressed(),
		Type:    metanet.NodeTypeFile,
		Access:  metanet.AccessFree,
		KeyHash: enc1.KeyHash,
	})
	mock.setContent(pub1.Compressed(), enc1.Ciphertext)

	dirTxID1 := makeTxIDImport(0x20)
	_, dirPub1 := testKeyPairImport(t)
	mock.addNode(&metanet.Node{
		TxID:  dirTxID1,
		PNode: dirPub1.Compressed(),
		Type:  metanet.NodeTypeDir,
		Children: []metanet.ChildEntry{
			{Name: "file.txt", Type: metanet.NodeTypeFile, PubKey: pub1.Compressed()},
		},
	})

	// Anchor 1 (genesis).
	anchorTxID1 := makeTxIDImport(0x01)
	mock.addNode(&metanet.Node{
		TxID:          anchorTxID1,
		PNode:         makePubKeyImport(0x02),
		Type:          metanet.NodeTypeAnchor,
		TreeRootPNode: dirPub1.Compressed(),
		TreeRootTxID:  dirTxID1,
		Author:        "Alice <a@b.com> 1000 +0000",
		CommitMessage: "first commit",
		Timestamp:     1000,
	})

	// Second file.
	priv2, pub2 := testKeyPairImport(t)
	content2 := []byte("updated")
	enc2, err := method42.Encrypt(content2, priv2, pub2, method42.AccessFree)
	require.NoError(t, err)

	fileTxID2 := makeTxIDImport(0x11)
	mock.addNode(&metanet.Node{
		TxID:    fileTxID2,
		PNode:   pub2.Compressed(),
		Type:    metanet.NodeTypeFile,
		Access:  metanet.AccessFree,
		KeyHash: enc2.KeyHash,
	})
	mock.setContent(pub2.Compressed(), enc2.Ciphertext)

	dirTxID2 := makeTxIDImport(0x21)
	_, dirPub2 := testKeyPairImport(t)
	mock.addNode(&metanet.Node{
		TxID:  dirTxID2,
		PNode: dirPub2.Compressed(),
		Type:  metanet.NodeTypeDir,
		Children: []metanet.ChildEntry{
			{Name: "file.txt", Type: metanet.NodeTypeFile, PubKey: pub2.Compressed()},
		},
	})

	// Anchor 2 (points to anchor 1 as parent).
	anchorTxID2 := makeTxIDImport(0x02)
	mock.addNode(&metanet.Node{
		TxID:             anchorTxID2,
		PNode:            makePubKeyImport(0x03),
		Type:             metanet.NodeTypeAnchor,
		TreeRootPNode:    dirPub2.Compressed(),
		TreeRootTxID:     dirTxID2,
		ParentAnchorTxID: [][]byte{anchorTxID1},
		Author:           "Alice <a@b.com> 2000 +0000",
		CommitMessage:    "second commit",
		Timestamp:        2000,
	})

	refUTXOs := []utxo.RefUTXO{
		{
			Ref:        "refs/heads/main",
			AnchorTxID: hex.EncodeToString(anchorTxID2),
			TxID:       makeTxIDHex(0x30),
			Vout:       1,
			Amount:     546,
		},
	}

	input := "import refs/heads/main\n\n"
	h, stdout, _, _ := setupImportHelper(t, input, mock, refUTXOs)

	err = h.Run()
	require.NoError(t, err)

	output := stdout.String()

	// Should have two commits in chronological order.
	assert.Contains(t, output, "first commit")
	assert.Contains(t, output, "second commit")
	assert.Contains(t, output, "done\n")

	// The first commit should appear before the second in the output.
	firstIdx := strings.Index(output, "first commit")
	secondIdx := strings.Index(output, "second commit")
	assert.True(t, firstIdx < secondIdx,
		"first commit should appear before second commit in output")

	// Second commit should reference the first via "from" mark.
	assert.Contains(t, output, "from :")
}

func TestImportProtocol(t *testing.T) {
	mock := newMockChainReader()

	// Create a minimal setup: one file, one dir, one anchor.
	priv, pub := testKeyPairImport(t)
	content := []byte("README")
	enc, err := method42.Encrypt(content, priv, pub, method42.AccessFree)
	require.NoError(t, err)

	fileTxID := makeTxIDImport(0x10)
	mock.addNode(&metanet.Node{
		TxID:    fileTxID,
		PNode:   pub.Compressed(),
		Type:    metanet.NodeTypeFile,
		Access:  metanet.AccessFree,
		KeyHash: enc.KeyHash,
	})
	mock.setContent(pub.Compressed(), enc.Ciphertext)

	dirTxID := makeTxIDImport(0x20)
	_, dirPub := testKeyPairImport(t)
	mock.addNode(&metanet.Node{
		TxID:  dirTxID,
		PNode: dirPub.Compressed(),
		Type:  metanet.NodeTypeDir,
		Children: []metanet.ChildEntry{
			{Name: "README.md", Type: metanet.NodeTypeFile, PubKey: pub.Compressed()},
		},
	})

	anchorTxID := makeTxIDImport(0x01)
	mock.addNode(&metanet.Node{
		TxID:          anchorTxID,
		PNode:         makePubKeyImport(0x02),
		Type:          metanet.NodeTypeAnchor,
		TreeRootPNode: dirPub.Compressed(),
		TreeRootTxID:  dirTxID,
		Author:        "Test <t@t.com> 1000 +0000",
		CommitMessage: "init",
	})

	refUTXOs := []utxo.RefUTXO{
		{
			Ref:        "refs/heads/main",
			AnchorTxID: hex.EncodeToString(anchorTxID),
			TxID:       makeTxIDHex(0x30),
			Vout:       1,
			Amount:     546,
		},
	}

	// Simulate full protocol: capabilities -> list -> import.
	input := "capabilities\nlist\nimport refs/heads/main\n\n"
	h, stdout, _, _ := setupImportHelper(t, input, mock, refUTXOs)

	err = h.Run()
	require.NoError(t, err)

	output := stdout.String()

	// Capabilities response.
	assert.Contains(t, output, "import\n")
	assert.Contains(t, output, "export\n")

	// List response (empty since no commit SHA in notes).
	// The list will output an empty line since no notes are available
	// to resolve commit SHAs.

	// Import response: fast-import stream.
	assert.Contains(t, output, "blob\n")
	assert.Contains(t, output, "README")
	assert.Contains(t, output, "commit refs/heads/main\n")
	assert.Contains(t, output, "README.md")
	assert.Contains(t, output, "done\n")
}

func TestImportAnchorWithNoTree(t *testing.T) {
	mock := newMockChainReader()

	// Anchor with no tree root (e.g., an empty commit).
	anchorTxID := makeTxIDImport(0x01)
	mock.addNode(&metanet.Node{
		TxID:          anchorTxID,
		PNode:         makePubKeyImport(0x02),
		Type:          metanet.NodeTypeAnchor,
		Author:        "Test <t@t.com> 1000 +0000",
		CommitMessage: "empty commit",
	})

	refUTXOs := []utxo.RefUTXO{
		{
			Ref:        "refs/heads/main",
			AnchorTxID: hex.EncodeToString(anchorTxID),
			TxID:       makeTxIDHex(0x30),
			Vout:       1,
			Amount:     546,
		},
	}

	input := "import refs/heads/main\n\n"
	h, stdout, _, _ := setupImportHelper(t, input, mock, refUTXOs)

	err := h.Run()
	require.NoError(t, err)

	output := stdout.String()
	// Should still have a commit (with no file ops) and done.
	assert.Contains(t, output, "commit refs/heads/main\n")
	assert.Contains(t, output, "empty commit")
	assert.Contains(t, output, "done\n")
}
