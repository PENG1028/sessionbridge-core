// Package mesh provides cryptographic node identity and a trust store for
// peer-to-peer authentication between SessionNode instances.
//
// NodeIdentity uses ed25519 key pairs. The private key is persisted to disk
// (identity.json, 0600 permissions) alongside the public key and fingerprint.
// LoadOrCreateIdentity validates the loaded identity on every restart.
package mesh

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// NodeIdentity is a cryptographic node identity backed by ed25519.
type NodeIdentity struct {
	NodeID      string `json:"nodeId"`
	PublicKey   []byte `json:"publicKey"`
	PrivateKey  []byte `json:"privateKey"` // ed25519 private key (64 bytes), persisted to identity.json
	Fingerprint string `json:"fingerprint"` // hex SHA-256 of public key
	CreatedAt   int64  `json:"createdAt"`   // unix millis
}

// PublicIdentity is a safe-to-share subset of NodeIdentity.
type PublicIdentity struct {
	NodeID      string `json:"nodeId"`
	PublicKey   []byte `json:"publicKey"`
	Fingerprint string `json:"fingerprint"`
	CreatedAt   int64  `json:"createdAt"`
}

// MeshState bundles the local node identity and the trust store so they can
// be passed through executor deps and other subsystems.
type MeshState struct {
	Identity    *NodeIdentity
	TrustStore  *TrustStore
	InviteStore *InviteStore
}

// identityFile returns the path to the identity JSON file inside dataDir.
func identityFile(dataDir string) string {
	return filepath.Join(dataDir, "identity.json")
}

// LoadOrCreateIdentity loads the node identity from {dataDir}/identity.json.
// If the file does not exist, a fresh ed25519 key pair is generated and persisted.
//
// When loading from disk, the identity is validated:
//   - non-empty keys and fingerprint
//   - correct ed25519 key lengths (public=32, private=64)
//   - key-pair consistency (sign/verify round-trip)
//   - fingerprint matches SHA-256 of public key
//
// Validation failure returns an error — it does NOT silently regenerate.
//
// The nodeID parameter is the preferred node identifier. If it is empty or the
// sentinel value "node_local", the first 12 hex characters of the public-key
// fingerprint are used instead.
func LoadOrCreateIdentity(dataDir, nodeID string) (*NodeIdentity, error) {
	path := identityFile(dataDir)

	if data, err := os.ReadFile(path); err == nil {
		var ni NodeIdentity
		if err := json.Unmarshal(data, &ni); err != nil {
			return nil, fmt.Errorf("mesh: unmarshal identity %s: %w", path, err)
		}
		if err := ni.validate(); err != nil {
			return nil, fmt.Errorf("mesh: validate identity %s: %w", path, err)
		}
		return &ni, nil
	}

	// Generate a fresh key pair.
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("mesh: generate ed25519 key: %w", err)
	}

	hash := sha256.Sum256(pub)
	fingerprint := hex.EncodeToString(hash[:])

	// Deterministic node ID: use the first 12 hex chars of the fingerprint
	// unless the caller specified an explicit non-sentinel ID.
	id := nodeID
	if id == "" || id == "node_local" {
		id = fingerprint[:12]
	}

	ni := &NodeIdentity{
		NodeID:      id,
		PublicKey:   []byte(pub),
		PrivateKey:  []byte(priv),
		Fingerprint: fingerprint,
		CreatedAt:   time.Now().UnixMilli(),
	}

	if err := ni.save(dataDir); err != nil {
		return nil, fmt.Errorf("mesh: save new identity: %w", err)
	}

	return ni, nil
}

// validate checks the structural integrity of a loaded identity.
func (n *NodeIdentity) validate() error {
	if n.NodeID == "" {
		return fmt.Errorf("nodeId is empty")
	}
	if len(n.PublicKey) == 0 {
		return fmt.Errorf("publicKey is empty")
	}
	if len(n.PrivateKey) == 0 {
		return fmt.Errorf("privateKey is empty")
	}
	if n.Fingerprint == "" {
		return fmt.Errorf("fingerprint is empty")
	}
	if n.CreatedAt == 0 {
		return fmt.Errorf("createdAt is zero")
	}

	// Verify key lengths.
	if len(n.PublicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("publicKey has wrong length: got %d, expected %d", len(n.PublicKey), ed25519.PublicKeySize)
	}
	if len(n.PrivateKey) != ed25519.PrivateKeySize {
		return fmt.Errorf("privateKey has wrong length: got %d, expected %d", len(n.PrivateKey), ed25519.PrivateKeySize)
	}

	// Verify key-pair consistency.
	message := []byte("mesh-identity-validation")
	sig, err := n.Sign(message)
	if err != nil {
		return fmt.Errorf("sign during validation: %w", err)
	}
	if !ed25519.Verify(ed25519.PublicKey(n.PublicKey), message, sig) {
		return fmt.Errorf("key-pair mismatch: public key does not verify signature from private key")
	}

	// Verify fingerprint.
	hash := sha256.Sum256(n.PublicKey)
	expectedFP := hex.EncodeToString(hash[:])
	if n.Fingerprint != expectedFP {
		return fmt.Errorf("fingerprint mismatch: stored=%q computed=%q", n.Fingerprint, expectedFP)
	}

	return nil
}

// PublicIdentity returns a safe-to-share copy of the identity without the
// private key.
func (n *NodeIdentity) PublicIdentity() *PublicIdentity {
	return &PublicIdentity{
		NodeID:      n.NodeID,
		PublicKey:   append([]byte(nil), n.PublicKey...),
		Fingerprint: n.Fingerprint,
		CreatedAt:   n.CreatedAt,
	}
}

// Sign signs message with the node's ed25519 private key.
// Returns an error if the private key is nil or empty.
func (n *NodeIdentity) Sign(message []byte) ([]byte, error) {
	if len(n.PrivateKey) == 0 {
		return nil, fmt.Errorf("mesh: private key is empty, cannot sign")
	}
	return ed25519.Sign(ed25519.PrivateKey(n.PrivateKey), message), nil
}

// Verify checks whether signature is a valid ed25519 signature of message by
// the given publicKey.
func (n *NodeIdentity) Verify(publicKey, message, signature []byte) bool {
	return ed25519.Verify(ed25519.PublicKey(publicKey), message, signature)
}

// VerifySignature is a standalone ed25519 signature verification helper.
func VerifySignature(publicKey, message, signature []byte) bool {
	return ed25519.Verify(ed25519.PublicKey(publicKey), message, signature)
}

// Save persists the identity to {dataDir}/identity.json with 0600 permissions.
func (n *NodeIdentity) Save(dataDir string) error {
	return n.save(dataDir)
}

// save is the internal implementation shared by LoadOrCreateIdentity and Save.
func (n *NodeIdentity) save(dataDir string) error {
	path := identityFile(dataDir)

	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return fmt.Errorf("mesh: create data dir %s: %w", dataDir, err)
	}

	data, err := json.MarshalIndent(n, "", "  ")
	if err != nil {
		return fmt.Errorf("mesh: marshal identity: %w", err)
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("mesh: write identity %s: %w", path, err)
	}

	return nil
}
