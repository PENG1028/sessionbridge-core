package executor

import (
	"path/filepath"
	"testing"

	"github.com/user/sessionnode/go-core/internal/config"
	"github.com/user/sessionnode/go-core/internal/mesh"
)

// configurableNodeLister lets tests specify which nodes the topology returns.
type configurableNodeLister struct {
	nodes []NodeInfo
}

func (m *configurableNodeLister) ListNodes() []NodeInfo {
	return m.nodes
}

// peerTestDeps returns Deps with a fresh TrustStore and a configurable node lister.
func peerTestDeps(t *testing.T, nodes []NodeInfo) *Deps {
	t.Helper()
	deps := testDeps(t)
	deps.Nodes = &configurableNodeLister{nodes: nodes}

	ts := mesh.NewTrustStore(filepath.Join(t.TempDir(), "trusted_peers_test.json"))
	deps.Mesh = &mesh.MeshState{
		TrustStore:  ts,
		InviteStore: mesh.NewInviteStore(),
	}
	return deps
}

// addTestPeer adds a peer to the trust store for testing convenience.
func addTestPeer(t *testing.T, ts *mesh.TrustStore, nodeID, name, fingerprint string, addresses []string, status string) {
	t.Helper()
	pub := make([]byte, 32)
	copy(pub, nodeID)
	if err := ts.Add(&mesh.TrustedPeer{
		NodeID:      nodeID,
		Name:        name,
		PublicKey:   pub,
		Fingerprint: fingerprint,
		Addresses:   addresses,
		Status:      status,
		Policy:      mesh.TrustPolicy{Mode: "full"},
	}); err != nil {
		t.Fatalf("addTestPeer(%s): %v", nodeID, err)
	}
}

// newConfigWithListenAddr creates a config.Manager with a given listen address.
func newConfigWithListenAddr(t *testing.T, listenAddr string) *config.Manager {
	t.Helper()
	cm := config.NewManager(filepath.Join(t.TempDir(), "config.json"))
	if err := cm.Load(); err != nil {
		t.Fatalf("config load: %v", err)
	}
	if err := cm.Set("core.listenAddr", listenAddr); err != nil {
		t.Fatalf("config set listenAddr: %v", err)
	}
	return cm
}

// ---------------------------------------------------------------------------
// node.peer.list
// ---------------------------------------------------------------------------

func TestPeerList_ReturnsTrustStorePlusRuntimeStatus(t *testing.T) {
	deps := peerTestDeps(t, []NodeInfo{
		{ID: "node-peer", Name: "peer", Status: "connected"},
	})

	addTestPeer(t, deps.Mesh.TrustStore, "node-peer", "peer", "fp-peer", []string{"ws://peer:9090/peer/ws"}, mesh.TrustStatusConnected)
	addTestPeer(t, deps.Mesh.TrustStore, "node-offline", "offline", "fp-offline", []string{"ws://offline:9090/peer/ws"}, mesh.TrustStatusOffline)

	r := New(deps)
	result, err := r.Execute(req("node.peer.list", nil))
	if err != nil {
		t.Fatalf("node.peer.list failed: %v", err)
	}

	normalized := normalize(result).(map[string]interface{})
	peers := normalized["peers"].([]interface{})
	if len(peers) != 2 {
		t.Fatalf("expected 2 peers, got %d", len(peers))
	}

	foundConnected := false
	foundOffline := false
	for _, p := range peers {
		peer := p.(map[string]interface{})
		nodeID := peer["nodeId"].(string)
		switch nodeID {
		case "node-peer":
			foundConnected = true
			if peer["status"] != "connected" {
				t.Errorf("peer node-peer: status = %s, want connected", peer["status"])
			}
		case "node-offline":
			foundOffline = true
			if peer["status"] != "offline" {
				t.Errorf("peer node-offline: status = %s, want offline", peer["status"])
			}
		}
	}

	if !foundConnected {
		t.Error("peer node-peer not found in list")
	}
	if !foundOffline {
		t.Error("peer node-offline not found in list")
	}
}

func TestPeerList_EmptyWhenNoTrustStore(t *testing.T) {
	deps := testDeps(t)
	deps.Mesh = nil

	r := New(deps)
	result, err := r.Execute(req("node.peer.list", nil))
	if err != nil {
		t.Fatalf("node.peer.list with nil mesh failed: %v", err)
	}

	normalized := normalize(result).(map[string]interface{})
	peers := normalized["peers"].([]interface{})
	if len(peers) != 0 {
		t.Errorf("expected 0 peers, got %d", len(peers))
	}
}

// ---------------------------------------------------------------------------
// node.peer.info
// ---------------------------------------------------------------------------

func TestPeerInfo_ReturnsSinglePeer(t *testing.T) {
	deps := peerTestDeps(t, []NodeInfo{
		{ID: "node-peer", Name: "peer", Status: "connected"},
	})

	addTestPeer(t, deps.Mesh.TrustStore, "node-peer", "peer", "fp-peer", []string{"ws://peer:9090/peer/ws"}, mesh.TrustStatusConnected)

	r := New(deps)
	result, err := r.Execute(req("node.peer.info", map[string]string{"nodeId": "node-peer"}))
	if err != nil {
		t.Fatalf("node.peer.info failed: %v", err)
	}

	normalized := normalize(result).(map[string]interface{})
	if normalized["nodeId"] != "node-peer" {
		t.Errorf("nodeId = %v, want node-peer", normalized["nodeId"])
	}
	if normalized["fingerprint"] != "fp-peer" {
		t.Errorf("fingerprint = %v, want fp-peer", normalized["fingerprint"])
	}
	if normalized["status"] != "connected" {
		t.Errorf("status = %v, want connected", normalized["status"])
	}
}

func TestPeerInfo_NotFound(t *testing.T) {
	deps := peerTestDeps(t, nil)
	r := New(deps)
	_, err := r.Execute(req("node.peer.info", map[string]string{"nodeId": "unknown"}))
	if err == nil {
		t.Fatal("expected error for unknown peer, got nil")
	}
}

// ---------------------------------------------------------------------------
// node.peer.revoke
// ---------------------------------------------------------------------------

func TestPeerRevoke_RemovesTrust(t *testing.T) {
	deps := peerTestDeps(t, nil)
	addTestPeer(t, deps.Mesh.TrustStore, "node-revoke", "revoke", "fp-revoke", []string{"ws://revoke:9090/peer/ws"}, mesh.TrustStatusConnected)

	r := New(deps)
	result, err := r.Execute(req("node.peer.revoke", map[string]string{"nodeId": "node-revoke"}))
	if err != nil {
		t.Fatalf("node.peer.revoke failed: %v", err)
	}

	normalized := normalize(result).(map[string]interface{})
	if normalized["status"] != "revoked" {
		t.Errorf("status = %v, want revoked", normalized["status"])
	}
	if normalized["nodeId"] != "node-revoke" {
		t.Errorf("nodeId = %v, want node-revoke", normalized["nodeId"])
	}

	// Verify the peer is no longer in the trust store
	_, err = deps.Mesh.TrustStore.Get("node-revoke")
	if err == nil {
		t.Error("expected peer to be removed from trust store after revoke")
	}
}

// ---------------------------------------------------------------------------
// node.peer.disconnect
// ---------------------------------------------------------------------------

func TestPeerDisconnect_DoesNotRemoveTrust(t *testing.T) {
	deps := peerTestDeps(t, nil)
	addTestPeer(t, deps.Mesh.TrustStore, "node-keep", "keep", "fp-keep", []string{"ws://keep:9090/peer/ws"}, mesh.TrustStatusConnected)

	r := New(deps)
	result, err := r.Execute(req("node.peer.disconnect", map[string]string{"nodeId": "node-keep"}))
	if err != nil {
		t.Fatalf("node.peer.disconnect failed: %v", err)
	}

	normalized := normalize(result).(map[string]interface{})
	if normalized["status"] != "disconnected" {
		t.Errorf("status = %v, want disconnected", normalized["status"])
	}

	// Verify the peer is STILL in the trust store
	p, err := deps.Mesh.TrustStore.Get("node-keep")
	if err != nil {
		t.Fatal("peer should still be in trust store after disconnect")
	}
	if p.NodeID != "node-keep" {
		t.Errorf("unexpected peer returned: %s", p.NodeID)
	}
}

// ---------------------------------------------------------------------------
// node.peer.reconnect
// ---------------------------------------------------------------------------

func TestPeerReconnect_ReturnsReconnectingStatus(t *testing.T) {
	deps := peerTestDeps(t, nil)
	addTestPeer(t, deps.Mesh.TrustStore, "node-reconn", "reconn", "fp-reconn", []string{"ws://reconn:9090/peer/ws"}, mesh.TrustStatusOffline)

	r := New(deps)
	result, err := r.Execute(req("node.peer.reconnect", map[string]string{"nodeId": "node-reconn"}))
	if err != nil {
		t.Fatalf("node.peer.reconnect failed: %v", err)
	}

	normalized := normalize(result).(map[string]interface{})
	if normalized["status"] != "reconnecting" {
		t.Errorf("status = %v, want reconnecting", normalized["status"])
	}
	if normalized["nodeId"] != "node-reconn" {
		t.Errorf("nodeId = %v, want node-reconn", normalized["nodeId"])
	}
}

// ---------------------------------------------------------------------------
// node.reachability.check
// ---------------------------------------------------------------------------

func TestReachability_ReturnsStableStructure(t *testing.T) {
	deps := testDeps(t)

	r := New(deps)
	result, err := r.Execute(req("node.reachability.check", nil))
	if err != nil {
		t.Fatalf("node.reachability.check failed: %v", err)
	}

	normalized := normalize(result).(map[string]interface{})

	// Verify all expected fields are present
	for _, field := range []string{"publicReachable", "inboundPeerAllowed", "outboundOnly", "reason"} {
		if _, ok := normalized[field]; !ok {
			t.Errorf("missing field: %s", field)
		}
	}
}

func TestReachability_WithNonLoopbackAddr(t *testing.T) {
	deps := testDeps(t)
	deps.Config = newConfigWithListenAddr(t, ":9090")

	r := New(deps)
	result, err := r.Execute(req("node.reachability.check", nil))
	if err != nil {
		t.Fatalf("node.reachability.check failed: %v", err)
	}

	normalized := normalize(result).(map[string]interface{})
	if normalized["inboundPeerAllowed"] != true {
		t.Errorf("expected inboundPeerAllowed=true for :9090, got %v", normalized["inboundPeerAllowed"])
	}
}

func TestReachability_WithLoopbackAddr(t *testing.T) {
	deps := testDeps(t)
	deps.Config = newConfigWithListenAddr(t, "127.0.0.1:9090")

	r := New(deps)
	result, err := r.Execute(req("node.reachability.check", nil))
	if err != nil {
		t.Fatalf("node.reachability.check failed: %v", err)
	}

	normalized := normalize(result).(map[string]interface{})
	if normalized["outboundOnly"] != true {
		t.Errorf("expected outboundOnly=true for 127.0.0.1:9090, got %v", normalized["outboundOnly"])
	}
}
