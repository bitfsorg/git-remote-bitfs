package chain

import (
	"context"
	"encoding/hex"
	"fmt"
	"path"

	ec "github.com/bsv-blockchain/go-sdk/primitives/ec"

	"github.com/tongxiaofeng/libbitfs/metanet"
	"github.com/tongxiaofeng/libbitfs/method42"
)

// ReadResult holds the data for one file read from chain.
type ReadResult struct {
	Path    string // File path relative to tree root
	Content []byte // Decrypted plaintext
	Mode    uint32 // Git file mode (e.g. 0100644)
}

// AnchorInfo holds parsed anchor data from a chain walk.
type AnchorInfo struct {
	TxID              []byte   // Anchor transaction ID
	PNode             []byte   // Branch P_node (33 bytes compressed)
	TreeRootPNode     []byte   // Root directory's P_node
	TreeRootTxID      []byte   // Root directory's latest TxID
	ParentAnchorTxIDs [][]byte // Previous anchor TxIDs (merge commits have multiple)
	Author            string   // Git commit author
	CommitMessage     string   // Git commit message
	Timestamp         uint64   // Unix timestamp
	GitCommitSHA      []byte   // 20 bytes
	BranchRef         string   // e.g. "refs/heads/main" (set by caller, not in TX data)
}

// ChainReader abstracts reading Metanet nodes from the blockchain.
// The interface makes testing easy -- mock at the node level without
// needing to build real raw transactions.
type ChainReader interface {
	// GetNodeByTxID reads a Metanet node from a transaction.
	GetNodeByTxID(ctx context.Context, txID []byte) (*metanet.Node, error)

	// GetNodeByPubKey returns the latest version of a node by its P_node.
	GetNodeByPubKey(ctx context.Context, pNode []byte) (*metanet.Node, error)

	// GetEncryptedContent reads the encrypted content for a file node.
	// If content is inline (in the EncPayload field), returns it directly.
	// If content is in separate data TXs (ContentTxIDs), fetches and concatenates them.
	GetEncryptedContent(ctx context.Context, node *metanet.Node) ([]byte, error)
}

// Reader reads and decrypts Metanet DAG data from the blockchain.
type Reader struct {
	chain ChainReader
}

// NewReader creates a new Reader that reads from the given chain reader.
func NewReader(cr ChainReader) *Reader {
	return &Reader{chain: cr}
}

// ReadAnchor reads and parses an anchor TX from chain.
func (r *Reader) ReadAnchor(ctx context.Context, txID []byte) (*AnchorInfo, error) {
	if len(txID) == 0 {
		return nil, fmt.Errorf("chain: ReadAnchor: empty txID")
	}

	node, err := r.chain.GetNodeByTxID(ctx, txID)
	if err != nil {
		return nil, fmt.Errorf("chain: ReadAnchor: reading node: %w", err)
	}

	if node.Type != metanet.NodeTypeAnchor {
		return nil, fmt.Errorf("chain: ReadAnchor: expected anchor node, got %s", node.Type)
	}

	info := &AnchorInfo{
		TxID:              make([]byte, len(txID)),
		PNode:             node.PNode,
		TreeRootPNode:     node.TreeRootPNode,
		TreeRootTxID:      node.TreeRootTxID,
		ParentAnchorTxIDs: node.ParentAnchorTxID,
		Author:            node.Author,
		CommitMessage:     node.CommitMessage,
		Timestamp:         node.Timestamp,
		GitCommitSHA:      node.GitCommitSHA,
	}
	copy(info.TxID, txID)

	return info, nil
}

// WalkAnchorChain walks the anchor chain from latest back to a stop point.
// Returns anchors in reverse chronological order (newest first).
// Stops when it reaches stopTxID (exclusive) or beginning of chain.
// If stopTxID is nil, walks to the genesis anchor.
func (r *Reader) WalkAnchorChain(ctx context.Context, latestTxID []byte, stopTxID []byte) ([]*AnchorInfo, error) {
	if len(latestTxID) == 0 {
		return nil, fmt.Errorf("chain: WalkAnchorChain: empty latestTxID")
	}

	var anchors []*AnchorInfo
	currentTxID := latestTxID

	for {
		// Check if we've reached the stop point.
		if stopTxID != nil && hex.EncodeToString(currentTxID) == hex.EncodeToString(stopTxID) {
			break
		}

		anchor, err := r.ReadAnchor(ctx, currentTxID)
		if err != nil {
			return nil, fmt.Errorf("chain: WalkAnchorChain: %w", err)
		}

		anchors = append(anchors, anchor)

		// Follow the first parent anchor (direct parent) to walk backward.
		if len(anchor.ParentAnchorTxIDs) == 0 {
			// Genesis anchor: no more parents.
			break
		}

		currentTxID = anchor.ParentAnchorTxIDs[0]
	}

	return anchors, nil
}

// ReadTree reads a directory tree recursively from chain.
// Returns all files with their decrypted content.
func (r *Reader) ReadTree(ctx context.Context, rootPNode []byte, rootTxID []byte,
	privKey *ec.PrivateKey) ([]ReadResult, error) {

	return r.readTreeRecursive(ctx, rootTxID, privKey, "")
}

// readTreeRecursive recursively reads a directory tree from chain.
func (r *Reader) readTreeRecursive(ctx context.Context, dirTxID []byte,
	privKey *ec.PrivateKey, prefix string) ([]ReadResult, error) {

	if len(dirTxID) == 0 {
		return nil, fmt.Errorf("chain: ReadTree: empty directory txID")
	}

	node, err := r.chain.GetNodeByTxID(ctx, dirTxID)
	if err != nil {
		return nil, fmt.Errorf("chain: ReadTree: reading directory %s: %w",
			hex.EncodeToString(dirTxID), err)
	}

	if node.Type != metanet.NodeTypeDir {
		return nil, fmt.Errorf("chain: ReadTree: expected directory node, got %s", node.Type)
	}

	var results []ReadResult

	for _, child := range node.Children {
		childPath := child.Name
		if prefix != "" {
			childPath = path.Join(prefix, child.Name)
		}

		switch child.Type {
		case metanet.NodeTypeFile:
			result, err := r.readFileByPubKey(ctx, child.PubKey, privKey, childPath)
			if err != nil {
				return nil, fmt.Errorf("chain: ReadTree: reading file %s: %w", childPath, err)
			}
			results = append(results, *result)

		case metanet.NodeTypeDir:
			// Look up the child directory by its P_node to get its latest TxID.
			childNode, err := r.chain.GetNodeByPubKey(ctx, child.PubKey)
			if err != nil {
				return nil, fmt.Errorf("chain: ReadTree: looking up subdir %s: %w", childPath, err)
			}
			subResults, err := r.readTreeRecursive(ctx, childNode.TxID, privKey, childPath)
			if err != nil {
				return nil, fmt.Errorf("chain: ReadTree: reading subdir %s: %w", childPath, err)
			}
			results = append(results, subResults...)

		case metanet.NodeTypeLink:
			// Skip links for MVP.
			continue
		}
	}

	return results, nil
}

// readFileByPubKey reads and decrypts a file node looked up by its P_node.
func (r *Reader) readFileByPubKey(ctx context.Context, pNode []byte,
	privKey *ec.PrivateKey, filePath string) (*ReadResult, error) {

	node, err := r.chain.GetNodeByPubKey(ctx, pNode)
	if err != nil {
		return nil, fmt.Errorf("looking up file node: %w", err)
	}

	return r.decryptFileNode(ctx, node, privKey, filePath)
}

// ReadFileNode reads and decrypts a single file node by its transaction ID.
func (r *Reader) ReadFileNode(ctx context.Context, txID []byte,
	privKey *ec.PrivateKey, filePath string) (*ReadResult, error) {

	node, err := r.chain.GetNodeByTxID(ctx, txID)
	if err != nil {
		return nil, fmt.Errorf("chain: ReadFileNode: looking up node: %w", err)
	}

	return r.decryptFileNode(ctx, node, privKey, filePath)
}

// decryptFileNode decrypts a file node's content and returns a ReadResult.
func (r *Reader) decryptFileNode(ctx context.Context, node *metanet.Node,
	privKey *ec.PrivateKey, filePath string) (*ReadResult, error) {

	if node.Type != metanet.NodeTypeFile {
		return nil, fmt.Errorf("chain: ReadFileNode: expected file node, got %s", node.Type)
	}

	// Get the encrypted content.
	ciphertext, err := r.chain.GetEncryptedContent(ctx, node)
	if err != nil {
		return nil, fmt.Errorf("chain: ReadFileNode: reading content: %w", err)
	}

	// Determine the decryption parameters based on access level.
	var decryptPriv *ec.PrivateKey
	var decryptAccess method42.Access

	switch node.Access {
	case metanet.AccessFree:
		decryptPriv = method42.FreePrivateKey()
		decryptAccess = method42.AccessFree
	case metanet.AccessPrivate:
		if privKey == nil {
			return nil, fmt.Errorf("chain: ReadFileNode: private file requires private key")
		}
		decryptPriv = privKey
		decryptAccess = method42.AccessPrivate
	case metanet.AccessPaid:
		return nil, fmt.Errorf("chain: ReadFileNode: paid access not supported in fetch MVP")
	}

	// Reconstruct the public key from the node's P_node bytes.
	pubKey, err := ec.PublicKeyFromBytes(node.PNode)
	if err != nil {
		return nil, fmt.Errorf("chain: ReadFileNode: parsing P_node: %w", err)
	}

	// Decrypt.
	decResult, err := method42.Decrypt(ciphertext, decryptPriv, pubKey, node.KeyHash, decryptAccess)
	if err != nil {
		return nil, fmt.Errorf("chain: ReadFileNode: decrypting: %w", err)
	}

	// Determine git file mode.
	mode := node.FileMode
	if mode == 0 {
		mode = 0o100644 // Default: regular file
	}

	return &ReadResult{
		Path:    filePath,
		Content: decResult.Plaintext,
		Mode:    mode,
	}, nil
}
