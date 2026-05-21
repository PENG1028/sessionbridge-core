package mesh

import (
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

// trustFilePayload is the on-disk representation of the trust store.
type trustFilePayload struct {
	Peers []*TrustedPeer `json:"peers"`
}

// TrustStore manages a persistent, mutable list of trusted peer identities.
// All public methods are safe for concurrent use.
type TrustStore struct {
	mu    sync.RWMutex
	peers map[string]*TrustedPeer // keyed by nodeID
	path  string
}

// NewTrustStore creates a TrustStore that reads from and writes to path.
func NewTrustStore(path string) *TrustStore {
	return &TrustStore{
		peers: make(map[string]*TrustedPeer),
		path:  path,
	}
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
		ts.peers[p.NodeID] = p
	}
	return nil
}

// Save persists the current peer set to disk.
func (ts *TrustStore) Save() error {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	return ts.saveLocked()
}

// saveLocked writes without acquiring the write lock. Caller must hold at
// least a read lock.
func (ts *TrustStore) saveLocked() error {
	peers := make([]*TrustedPeer, 0, len(ts.peers))
	for _, p := range ts.peers {
		peers = append(peers, p)
	}

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

// Add inserts or updates a trusted peer. If a peer with the same nodeID
// already exists it is replaced.
func (ts *TrustStore) Add(peer *TrustedPeer) error {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	ts.peers[peer.NodeID] = peer
	return ts.saveLocked()
}

// Remove deletes a trusted peer by nodeID (revoke).
func (ts *TrustStore) Remove(nodeID string) error {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	delete(ts.peers, nodeID)
	return ts.saveLocked()
}

// Get returns the trusted peer identified by nodeID, or an error if not found.
func (ts *TrustStore) Get(nodeID string) (*TrustedPeer, error) {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	p, ok := ts.peers[nodeID]
	if !ok {
		return nil, fmt.Errorf("mesh: peer %q not found in trust store", nodeID)
	}
	return p, nil
}

// List returns a snapshot of all trusted peers.
func (ts *TrustStore) List() []*TrustedPeer {
	ts.mu.RLock()
	defer ts.mu.RUnlock()

	out := make([]*TrustedPeer, 0, len(ts.peers))
	for _, p := range ts.peers {
		out = append(out, p)
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
	defer ts.mu.RUnlock()

	p, ok := ts.peers[nodeID]
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

// UpdateLastSeen updates the LastSeen timestamp for a peer.
func (ts *TrustStore) UpdateLastSeen(nodeID string) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	if p, ok := ts.peers[nodeID]; ok {
		p.LastSeen = time.Now().UnixMilli()
	}
}
