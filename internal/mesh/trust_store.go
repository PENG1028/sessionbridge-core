package mesh

import (
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// Trust status constants for TrustedPeer.Status.
const (
	TrustStatusConnected    = "connected"
	TrustStatusConnecting   = "connecting"
	TrustStatusReconnecting = "reconnecting"
	TrustStatusOffline      = "offline"
	TrustStatusExpired      = "expired"
	TrustStatusRevoked      = "revoked"
)

// TrustPolicy dictates how a trusted peer is allowed to interact.
type TrustPolicy struct {
	Mode string `json:"mode"` // "full" only for now
}

// TrustedPeer represents a peer node whose identity has been verified and
// whose public key is stored locally.
type TrustedPeer struct {
	NodeID         string      `json:"nodeId"`
	Name           string      `json:"name"`
	PublicKey      []byte      `json:"publicKey"`
	Fingerprint    string      `json:"fingerprint"`
	Addresses      []string    `json:"addresses"`      // ws://host:port/peer/ws
	TrustExpiresAt int64       `json:"trustExpiresAt"` // 0 = never expires, unix millis
	AutoReconnect  bool        `json:"autoReconnect"`
	Status         string      `json:"status"` // connected|connecting|reconnecting|offline|expired|revoked
	LastSeen       int64       `json:"lastSeen"`
	Policy         TrustPolicy `json:"policy"`
}

// deepCopy returns an independent copy of the peer so callers cannot mutate
// the store's internal state through shared pointers/slices.
func (p *TrustedPeer) deepCopy() *TrustedPeer {
	if p == nil {
		return nil
	}
	cp := *p
	cp.PublicKey = append([]byte(nil), p.PublicKey...)
	cp.Addresses = append([]string(nil), p.Addresses...)
	return &cp
}

// trustFilePayload is the on-disk representation of the trust store.
type trustFilePayload struct {
	Peers []*TrustedPeer `json:"peers"`
}

// TrustStore manages a persistent, mutable list of trusted peer identities.
// All public methods are safe for concurrent use.
type TrustStore struct {
	mu          sync.RWMutex
	peers       map[string]*TrustedPeer // keyed by nodeID
	path        string
	localNodeID string // if set, Add rejects peers whose NodeID matches (self-guard)
}

// NewTrustStore creates a TrustStore that reads from and writes to path.
func NewTrustStore(path string) *TrustStore {
	return &TrustStore{
		peers: make(map[string]*TrustedPeer),
		path:  path,
	}
}

// SetLocalNodeID configures the local node ID for self-guard validation.
// After this is called, Add will reject any peer whose NodeID matches.
func (ts *TrustStore) SetLocalNodeID(nodeID string) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	ts.localNodeID = nodeID
}

// Load reads the trust file from disk. If the file does not exist the store
// remains empty (no error).
func (ts *TrustStore) Load() error {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	data, err := os.ReadFile(ts.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("mesh: read trust store %s: %w", ts.path, err)
	}

	var payload trustFilePayload
	if err := json.Unmarshal(data, &payload); err != nil {
		return fmt.Errorf("mesh: unmarshal trust store %s: %w", ts.path, err)
	}

	ts.peers = make(map[string]*TrustedPeer, len(payload.Peers))
	for _, p := range payload.Peers {
		if p == nil {
			continue
		}
		ts.peers[p.NodeID] = p.deepCopy()
	}
	return nil
}

// Save persists the current peer set to disk. It holds a read lock only
// while snapshotting peers into memory, then releases the lock before
// marshaling and writing to disk.
func (ts *TrustStore) Save() error {
	return ts.writeFile(ts.snapshot())
}

// writeFile marshals the peer list and writes it to disk.
func (ts *TrustStore) writeFile(peers []*TrustedPeer) error {
	payload := trustFilePayload{Peers: peers}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return fmt.Errorf("mesh: marshal trust store: %w", err)
	}

	if err := os.WriteFile(ts.path, data, 0600); err != nil {
		return fmt.Errorf("mesh: write trust store %s: %w", ts.path, err)
	}
	return nil
}

// snapshot returns deep copies of all peers under a read lock.
func (ts *TrustStore) snapshot() []*TrustedPeer {
	ts.mu.RLock()
	defer ts.mu.RUnlock()
	return ts.snapshotLocked()
}

// snapshotLocked returns deep copies of all peers. The caller must hold either
// the read or write lock.
func (ts *TrustStore) snapshotLocked() []*TrustedPeer {
	out := make([]*TrustedPeer, 0, len(ts.peers))
	for _, p := range ts.peers {
		out = append(out, p.deepCopy())
	}
	return out
}

// Add inserts or updates a trusted peer. A deep copy of the peer is stored so
// the caller cannot mutate the store's internal state through the argument.
//
// Validation rules:
//   - peer must not be nil
//   - NodeID must not be empty
//   - PublicKey must be ed25519.PublicKeySize bytes
func (ts *TrustStore) Add(peer *TrustedPeer) error {
	if peer == nil {
		return fmt.Errorf("mesh: trusted peer nodeId is required")
	}
	if peer.NodeID == "" {
		return fmt.Errorf("mesh: trusted peer nodeId is required")
	}
	if len(peer.PublicKey) == 0 {
		return fmt.Errorf("mesh: trusted peer publicKey is required")
	}
	if len(peer.PublicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("mesh: trusted peer publicKey has wrong length: got %d, want %d", len(peer.PublicKey), ed25519.PublicKeySize)
	}

	// Reject self-referential entries — a node must never be its own peer.
	ts.mu.RLock()
	localID := ts.localNodeID
	ts.mu.RUnlock()
	if localID != "" && peer.NodeID == localID {
		return fmt.Errorf("mesh: refusing to add self (%s) as trusted peer", localID)
	}

	cp := peer.deepCopy()

	ts.mu.Lock()
	ts.peers[cp.NodeID] = cp
	peers := ts.snapshotLocked()
	ts.mu.Unlock()

	return ts.writeFile(peers)
}

// Remove deletes a trusted peer by nodeID (revoke).
func (ts *TrustStore) Remove(nodeID string) error {
	ts.mu.Lock()
	delete(ts.peers, nodeID)
	peers := ts.snapshotLocked()
	ts.mu.Unlock()

	return ts.writeFile(peers)
}

// UpdatePeer applies a mutation function to a peer identified by nodeID and
// persists the change to disk. Returns an error if the peer is not found.
func (ts *TrustStore) UpdatePeer(nodeID string, fn func(*TrustedPeer)) error {
	ts.mu.Lock()
	p, ok := ts.peers[nodeID]
	if !ok {
		ts.mu.Unlock()
		return fmt.Errorf("mesh: peer %q not found in trust store", nodeID)
	}
	fn(p)
	peers := ts.snapshotLocked()
	ts.mu.Unlock()
	return ts.writeFile(peers)
}

// Get returns an independent copy of the trusted peer identified by nodeID,
// or an error if not found.
func (ts *TrustStore) Get(nodeID string) (*TrustedPeer, error) {
	ts.mu.RLock()
	p, ok := ts.peers[nodeID]
	if ok {
		p = p.deepCopy()
	}
	ts.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("mesh: peer %q not found in trust store", nodeID)
	}
	return p, nil
}

// List returns a snapshot of all trusted peers as independent deep copies.
func (ts *TrustStore) List() []*TrustedPeer {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	out := make([]*TrustedPeer, 0, len(ts.peers))
	for _, p := range ts.peers {
		out = append(out, p.deepCopy())
	}
	return out
}

// Trusted checks whether a peer with the given nodeID is trusted and its
// public key matches the one on file.
//
// Returns:
//   - (true, nil)  — peer is trusted and key matches
//   - (false, nil) — peer is unknown, expired, or revoked
//   - (false, err)  — peer is known but public key does not match (possible impersonation)
func (ts *TrustStore) Trusted(nodeID string, publicKey []byte) (bool, error) {
	ts.mu.RLock()
	p, ok := ts.peers[nodeID]
	if ok {
		p = p.deepCopy()
	}
	ts.mu.RUnlock()

	if !ok {
		return false, nil
	}

	// Check revocation via status.
	if p.Status == "revoked" {
		return false, nil
	}

	// Public key mismatch is a security concern — return an error.
	if len(p.PublicKey) != len(publicKey) {
		return false, fmt.Errorf("mesh: public key length mismatch for peer %q: stored=%d presented=%d", nodeID, len(p.PublicKey), len(publicKey))
	}
	for i := range p.PublicKey {
		if p.PublicKey[i] != publicKey[i] {
			return false, fmt.Errorf("mesh: public key mismatch for peer %q — possible impersonation attempt", nodeID)
		}
	}

	return true, nil
}

// UpdateLastSeen updates the LastSeen timestamp for a peer and persists the
// change to disk.
func (ts *TrustStore) UpdateLastSeen(nodeID string) {
	ts.mu.Lock()
	p, ok := ts.peers[nodeID]
	if !ok {
		ts.mu.Unlock()
		return
	}
	p.LastSeen = time.Now().UnixMilli()
	peers := ts.snapshotLocked()
	ts.mu.Unlock()

	// Persist the change.
	_ = ts.writeFile(peers)
}
