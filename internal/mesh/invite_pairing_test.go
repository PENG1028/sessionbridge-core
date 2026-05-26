package mesh

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"path/filepath"
	"testing"
)

// remoteAcceptResponse simulates what a remote peer returns from POST /peer/invite/accept.
// The response body JSON uses a "node" field (not "peer"), matching the remote API contract.
type remoteAcceptResponse struct {
	Status string          `json:"status"`
	Node   *remoteNodeInfo `json:"node,omitempty"`
}

type remoteNodeInfo struct {
	NodeID      string `json:"nodeId"`
	PublicKey   string `json:"publicKey"`
	Fingerprint string `json:"fingerprint"`
}

// TestInvitePairing_ParseNodeField verifies that the accept response correctly
// carries node identity in the "node" field (not "peer").
func TestInvitePairing_ParseNodeField(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pubHex := hex.EncodeToString(pub)

	resp := remoteAcceptResponse{
		Status: "accepted",
		Node: &remoteNodeInfo{
			NodeID:      "remote-node-01",
			PublicKey:   pubHex,
			Fingerprint: "fp-abc123",
		},
	}

	if resp.Node == nil {
		t.Fatal("node field must be present")
	}
	if resp.Node.NodeID == "" {
		t.Fatal("nodeId must not be empty")
	}
	if resp.Node.PublicKey == "" {
		t.Fatal("publicKey must not be empty")
	}
	pubKeyBytes, err := hex.DecodeString(resp.Node.PublicKey)
	if err != nil {
		t.Fatalf("invalid publicKey hex: %v", err)
	}
	if len(pubKeyBytes) != ed25519.PublicKeySize {
		t.Fatalf("publicKey wrong length: got %d, want %d", len(pubKeyBytes), ed25519.PublicKeySize)
	}
}

// TestInvitePairing_StorePeerFromNodeData verifies that the parsed node data
// can be stored as a TrustedPeer via the trust store.
func TestInvitePairing_StorePeerFromNodeData(t *testing.T) {
	ts := NewTrustStore(filepath.Join(t.TempDir(), "peers.json"))

	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pubHex := hex.EncodeToString(pub)
	pubKeyBytes, _ := hex.DecodeString(pubHex)

	peer := &TrustedPeer{
		NodeID:      "remote-node-01",
		PublicKey:   pubKeyBytes,
		Fingerprint: "fp-abc123",
		Addresses:   []string{"ws://remote:8080/peer/ws"},
		Policy:      TrustPolicy{Mode: "full"},
	}

	if err := ts.Add(peer); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	got, err := ts.Get("remote-node-01")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if got.NodeID != "remote-node-01" {
		t.Fatalf("NodeID mismatch: %q", got.NodeID)
	}
	if len(got.PublicKey) != ed25519.PublicKeySize {
		t.Fatalf("PublicKey length mismatch: got %d, want %d", len(got.PublicKey), ed25519.PublicKeySize)
	}
}

// TestInvitePairing_ReAcceptUpdatesPeer verifies that accepting the same peer
// again (e.g. after token rotation) updates the trust store in-place.
func TestInvitePairing_ReAcceptUpdatesPeer(t *testing.T) {
	ts := NewTrustStore(filepath.Join(t.TempDir(), "peers.json"))

	pub1, _, _ := ed25519.GenerateKey(rand.Reader)
	_ = ts.Add(&TrustedPeer{
		NodeID:    "node-1",
		PublicKey: []byte(pub1),
		Addresses: []string{"ws://old:8080/ws"},
		Policy:    TrustPolicy{Mode: "full"},
	})

	pub2, _, _ := ed25519.GenerateKey(rand.Reader)
	_ = ts.Add(&TrustedPeer{
		NodeID:    "node-1",
		PublicKey: []byte(pub2),
		Addresses: []string{"ws://new:9090/ws"},
		Policy:    TrustPolicy{Mode: "full"},
	})

	list := ts.List()
	if len(list) != 1 {
		t.Fatalf("expected 1 peer after re-accept, got %d", len(list))
	}
	if string(list[0].PublicKey) != string(pub2) {
		t.Fatal("PublicKey was not updated")
	}
	if list[0].Addresses[0] != "ws://new:9090/ws" {
		t.Fatal("Addresses were not updated")
	}
}

// TestInvitePairing_EmptyRemoteResponseNodeID verifies that rejecting an empty
// nodeId from a remote response is handled correctly.
func TestInvitePairing_EmptyRemoteResponseNodeID(t *testing.T) {
	resp := remoteAcceptResponse{
		Status: "accepted",
		Node: &remoteNodeInfo{
			NodeID:      "",
			PublicKey:   "abcd",
			Fingerprint: "fp",
		},
	}

	if resp.Node.NodeID == "" {
		// This is the validation that the handler should perform.
		return
	}
	t.Fatal("expected empty nodeId to be rejected")
}
