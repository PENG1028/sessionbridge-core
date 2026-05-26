package topology

import (
	"sync"
	"testing"
	"time"

	"github.com/user/sessionnode/go-core/pkg/protocol"
	"github.com/user/sessionnode/go-core/pkg/types"
)

func TestNew_SingleLocal(t *testing.T) {
	pt := New(Config{
		LocalID:   "node-main",
		LocalName: "dev",
	})

	nodes := pt.ListNodes()
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}

	n := nodes[0]
	if n.ID != "node-main" {
		t.Errorf("expected ID node-main, got %s", n.ID)
	}
	if n.Status != StatusLocal {
		t.Errorf("expected Status=local, got %s", n.Status)
	}
	if n.DisplayName != "dev" {
		t.Errorf("expected DisplayName=dev, got %s", n.DisplayName)
	}
	if n.Name != "dev" {
		t.Errorf("expected Name=dev, got %s", n.Name)
	}
}

func TestNew_WithPeers(t *testing.T) {
	pt := New(Config{
		LocalID:   "node-main",
		LocalName: "dev",
		Peers: []PeerConfig{
			{ID: "node-w1", Address: "localhost:9091", Tags: []string{"local"}},
			{ID: "vps-node", Address: "43.160.241.180:8080", Tags: []string{"remote"}},
		},
	})

	nodes := pt.ListNodes()
	if len(nodes) != 3 {
		t.Fatalf("expected 3 nodes (local + 2 peers), got %d", len(nodes))
	}

	// Build lookup
	byID := make(map[types.NodeID]int)
	for i, n := range nodes {
		byID[n.ID] = i
	}

	// Local node
	local, ok := byID["node-main"]
	if !ok {
		t.Fatal("local node not found in listing")
	}
	if nodes[local].Status != StatusLocal {
		t.Errorf("local status should be local, got %s", nodes[local].Status)
	}

	// Local peer
	w1, ok := byID["node-w1"]
	if !ok {
		t.Fatal("node-w1 not found")
	}
	if nodes[w1].Status != StatusDisconnected {
		t.Errorf("peer status should be disconnected, got %s", nodes[w1].Status)
	}
	if nodes[w1].Address != "localhost:9091" {
		t.Errorf("expected address localhost:9091, got %s", nodes[w1].Address)
	}

	// Remote peer
	vps, ok := byID["vps-node"]
	if !ok {
		t.Fatal("vps-node not found")
	}
	if nodes[vps].Address != "43.160.241.180:8080" {
		t.Errorf("expected address 43.160.241.180:8080, got %s", nodes[vps].Address)
	}
}

func TestGet_Unknown(t *testing.T) {
	pt := New(Config{
		LocalID:   "node-main",
		LocalName: "dev",
	})

	_, err := pt.Get("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown node")
	}
}

func TestGet_Local(t *testing.T) {
	pt := New(Config{
		LocalID:   "node-main",
		LocalName: "dev",
	})

	_, err := pt.Get("node-main")
	if err == nil {
		t.Fatal("expected error when Get is called for local node (should be handled by dispatcher)")
	}
}

func TestListNodes_LocalOnly(t *testing.T) {
	pt := New(Config{
		LocalID:   "main",
		LocalName: "my-machine",
	})

	nodes := pt.ListNodes()
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	if nodes[0].DisplayName != "my-machine" {
		t.Errorf("single local display should be name only, got %s", nodes[0].DisplayName)
	}
}

func TestListNodes_MultiLocalDisplay(t *testing.T) {
	pt := New(Config{
		LocalID:   "main",
		LocalName: "dev",
		Peers: []PeerConfig{
			{ID: "node-w1", Address: "localhost:9091", Tags: []string{"local"}},
		},
	})

	nodes := pt.ListNodes()
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got %d", len(nodes))
	}

	byID := make(map[types.NodeID]string)
	for _, n := range nodes {
		byID[n.ID] = n.DisplayName
	}

	// Two local peers → both get numbered
	if byID["main"] != "dev-(1)" {
		t.Errorf("expected main display 'dev-(1)', got %q", byID["main"])
	}
	if byID["node-w1"] != "dev-(2)" {
		t.Errorf("expected node-w1 display 'dev-(2)', got %q", byID["node-w1"])
	}
}

func TestListNodes_ThreeLocal(t *testing.T) {
	pt := New(Config{
		LocalID:   "a",
		LocalName: "dev",
		Peers: []PeerConfig{
			{ID: "b", Address: "localhost:9091", Tags: []string{"local"}},
			{ID: "c", Address: "localhost:9092", Tags: []string{"local"}},
		},
	})

	nodes := pt.ListNodes()
	byID := make(map[types.NodeID]string)
	for _, n := range nodes {
		byID[n.ID] = n.DisplayName
	}

	if byID["a"] != "dev-(1)" {
		t.Errorf("expected a='dev-(1)', got %q", byID["a"])
	}
	if byID["b"] != "dev-(2)" {
		t.Errorf("expected b='dev-(2)', got %q", byID["b"])
	}
	if byID["c"] != "dev-(3)" {
		t.Errorf("expected c='dev-(3)', got %q", byID["c"])
	}
}

func TestListNodes_RemoteDisplay(t *testing.T) {
	pt := New(Config{
		LocalID:   "main",
		LocalName: "dev",
		Peers: []PeerConfig{
			{ID: "vps-node", Address: "43.160.241.180:8080", Tags: []string{"remote"}},
		},
	})

	nodes := pt.ListNodes()
	byID := make(map[types.NodeID]string)
	for _, n := range nodes {
		byID[n.ID] = n.DisplayName
	}

	// Single local → no number
	if byID["main"] != "dev" {
		t.Errorf("expected main='dev', got %q", byID["main"])
	}
	// Remote → uses ID
	if byID["vps-node"] != "vps-node" {
		t.Errorf("expected vps-node='vps-node', got %q", byID["vps-node"])
	}
}

func TestHasTag(t *testing.T) {
	if !hasTag([]string{"local"}, "local") {
		t.Error("expected hasTag to find 'local'")
	}
	if !hasTag([]string{"remote", "local"}, "local") {
		t.Error("expected hasTag to find 'local' in multi-tag")
	}
	if hasTag([]string{"remote"}, "local") {
		t.Error("expected hasTag to not find 'local'")
	}
	if hasTag(nil, "local") {
		t.Error("expected hasTag to return false for nil")
	}
}

func TestComputeLocalDisplayNames_None(t *testing.T) {
	m := computeLocalDisplayNames(nil, "dev")
	if len(m) != 0 {
		t.Errorf("expected empty map, got %d entries", len(m))
	}
}

func TestComputeLocalDisplayNames_Single(t *testing.T) {
	peers := []*Peer{{ID: "main"}}
	m := computeLocalDisplayNames(peers, "dev")
	if m["main"] != "dev" {
		t.Errorf("expected 'dev', got %q", m["main"])
	}
}

func TestComputeLocalDisplayNames_Multi(t *testing.T) {
	peers := []*Peer{
		{ID: "a"},
		{ID: "b"},
		{ID: "c"},
	}
	m := computeLocalDisplayNames(peers, "dev")
	if m["a"] != "dev-(1)" {
		t.Errorf("expected a='dev-(1)', got %q", m["a"])
	}
	if m["b"] != "dev-(2)" {
		t.Errorf("expected b='dev-(2)', got %q", m["b"])
	}
	if m["c"] != "dev-(3)" {
		t.Errorf("expected c='dev-(3)', got %q", m["c"])
	}
}

func TestPeerName_Local(t *testing.T) {
	p := &Peer{ID: "main"}
	name := peerName(p, "main", "my-machine")
	if name != "my-machine" {
		t.Errorf("expected 'my-machine', got %q", name)
	}
}

func TestPeerName_Remote(t *testing.T) {
	p := &Peer{ID: "vps-node"}
	name := peerName(p, "main", "dev")
	if name != "vps-node" {
		t.Errorf("expected 'vps-node', got %q", name)
	}
}

// ---------------------------------------------------------------------------
// HandleMessage tests — stream.chunk / session.event forwarding
// ---------------------------------------------------------------------------

func TestHandleMessage_StreamChunk_WithHandler(t *testing.T) {
	pt := New(Config{LocalID: "test", LocalName: "test"})

	var (
		mu        sync.Mutex
		received  *protocol.Message
		callCount int
	)
	pt.SetStreamChunkHandler(func(msg *protocol.Message) {
		mu.Lock()
		defer mu.Unlock()
		received = msg
		callCount++
	})

	msg := protocol.NewStreamChunk("sess_1", "stdout", 42, "hello world")
	raw, err := msg.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	pt.HandleMessage("peer-a", raw)

	mu.Lock()
	defer mu.Unlock()
	if callCount != 1 {
		t.Fatalf("handler called %d times, want 1", callCount)
	}
	if received == nil {
		t.Fatal("handler did not receive message")
	}
	if received.Type != protocol.MsgTypeStreamChunk {
		t.Errorf("Type = %q, want %q", received.Type, protocol.MsgTypeStreamChunk)
	}
	if received.SessionID != "sess_1" {
		t.Errorf("SessionID = %q, want sess_1", received.SessionID)
	}
	if received.StreamType != "stdout" {
		t.Errorf("StreamType = %q, want stdout", received.StreamType)
	}
	if received.EventSeq != 42 {
		t.Errorf("EventSeq = %d, want 42", received.EventSeq)
	}
	if received.Data != "hello world" {
		t.Errorf("Data = %q, want hello world", received.Data)
	}
}

func TestHandleMessage_SessionEvent_WithHandler(t *testing.T) {
	pt := New(Config{LocalID: "test", LocalName: "test"})

	var (
		mu       sync.Mutex
		received *protocol.Message
	)
	pt.SetStreamChunkHandler(func(msg *protocol.Message) {
		mu.Lock()
		defer mu.Unlock()
		received = msg
	})

	msg := protocol.NewSessionEvent("sess_1", 7, "exited", nil)
	raw, err := msg.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	pt.HandleMessage("peer-b", raw)

	mu.Lock()
	defer mu.Unlock()
	if received == nil {
		t.Fatal("handler did not receive session.event message")
	}
	if received.Type != protocol.MsgTypeSessionEvent {
		t.Errorf("Type = %q, want %q", received.Type, protocol.MsgTypeSessionEvent)
	}
	if received.SessionID != "sess_1" {
		t.Errorf("SessionID = %q, want sess_1", received.SessionID)
	}
	if received.EventSeq != 7 {
		t.Errorf("EventSeq = %d, want 7", received.EventSeq)
	}
}

func TestHandleMessage_StreamChunk_NoHandler(t *testing.T) {
	pt := New(Config{LocalID: "test", LocalName: "test"})
	// No handler set — should not panic.

	msg := protocol.NewStreamChunk("sess_1", "stdout", 1, "data")
	raw, err := msg.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// This must not panic.
	pt.HandleMessage("peer-a", raw)
}

func TestHandleMessage_ActionResponse_StillWorks(t *testing.T) {
	pt := New(Config{LocalID: "test", LocalName: "test"})

	// Register a pending request
	reqID := types.RequestID("req_001")
	ch := make(chan *types.CapabilityResponse, 1)
	pt.pendingMu.Lock()
	pt.pending[reqID] = ch
	pt.pendingMu.Unlock()

	// Build an action.response message
	respMsg := &protocol.Message{
		Type:      protocol.MsgTypeActionResponse,
		RequestID: reqID,
		OK:        true,
	}
	raw, err := respMsg.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	pt.HandleMessage("peer-a", raw)

	select {
	case resp := <-ch:
		if !resp.OK {
			t.Error("expected OK=true")
		}
		if resp.RequestID != reqID {
			t.Errorf("RequestID = %q, want %q", resp.RequestID, reqID)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for action.response to be delivered")
	}
}

func TestHandleMessage_UnknownType_NoPanic(t *testing.T) {
	pt := New(Config{LocalID: "test", LocalName: "test"})

	msg := &protocol.Message{
		Type: "unknown.fake.type",
	}
	raw, err := msg.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Must not panic.
	pt.HandleMessage("peer-a", raw)
}

func TestHandleMessage_UnknownType_NoPanic_AfterStreamHandlerSet(t *testing.T) {
	pt := New(Config{LocalID: "test", LocalName: "test"})
	pt.SetStreamChunkHandler(func(msg *protocol.Message) {})

	msg := &protocol.Message{
		Type: "unknown.fake.type",
	}
	raw, err := msg.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// Must not panic — unknown type should still go to default case.
	pt.HandleMessage("peer-a", raw)
}

func TestHandleMessage_StreamChunk_NoPendingSideEffect(t *testing.T) {
	// Verifies that stream.chunk does not interfere with the pending request map.
	pt := New(Config{LocalID: "test", LocalName: "test"})

	// Register a pending request
	reqID := types.RequestID("req_002")
	ch := make(chan *types.CapabilityResponse, 1)
	pt.pendingMu.Lock()
	pt.pending[reqID] = ch
	pt.pendingMu.Unlock()

	var handlerCalled bool
	pt.SetStreamChunkHandler(func(msg *protocol.Message) {
		handlerCalled = true
	})

	// Send a stream.chunk first
	streamMsg := protocol.NewStreamChunk("sess_x", "stdout", 1, "data")
	raw, _ := streamMsg.MarshalJSON()
	pt.HandleMessage("peer-a", raw)

	if !handlerCalled {
		t.Error("stream chunk handler was not called")
	}

	// Pending request should still be intact
	pt.pendingMu.Lock()
	_, exists := pt.pending[reqID]
	pt.pendingMu.Unlock()
	if !exists {
		t.Error("pending request was incorrectly removed by stream.chunk handling")
	}

	// Action.response should still work
	respMsg := &protocol.Message{
		Type:      protocol.MsgTypeActionResponse,
		RequestID: reqID,
		OK:        true,
	}
	raw, _ = respMsg.MarshalJSON()
	pt.HandleMessage("peer-a", raw)

	select {
	case resp := <-ch:
		if !resp.OK {
			t.Error("expected OK=true")
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for action.response after stream.chunk")
	}
}

func TestAddOrUpdatePeer_NewPeer(t *testing.T) {
	pt := New(Config{
		LocalID:   "node-main",
		LocalName: "dev",
	})

	err := pt.AddOrUpdatePeer("node-remote", "10.0.0.1:8080", false)
	if err != nil {
		t.Fatalf("AddOrUpdatePeer failed: %v", err)
	}

	nodes := pt.ListNodes()
	found := false
	for _, n := range nodes {
		if n.ID == "node-remote" {
			found = true
			if n.Address != "10.0.0.1:8080" {
				t.Errorf("address = %q, want 10.0.0.1:8080", n.Address)
			}
			if n.Status != StatusDisconnected {
				t.Errorf("status = %q, want disconnected", n.Status)
			}
			break
		}
	}
	if !found {
		t.Fatal("node-remote not found in nodes list")
	}
}

func TestAddOrUpdatePeer_UpdateExisting(t *testing.T) {
	pt := New(Config{
		LocalID:   "node-main",
		LocalName: "dev",
		Peers: []PeerConfig{
			{ID: "node-remote", Address: "old:8080"},
		},
	})

	// Update the address
	err := pt.AddOrUpdatePeer("node-remote", "new:9090", false)
	if err != nil {
		t.Fatalf("AddOrUpdatePeer failed: %v", err)
	}

	nodes := pt.ListNodes()
	for _, n := range nodes {
		if n.ID == "node-remote" {
			if n.Address != "new:9090" {
				t.Errorf("address = %q, want new:9090", n.Address)
			}
			return
		}
	}
	t.Fatal("node-remote not found after update")
}

func TestAddOrUpdatePeer_SelfRejected(t *testing.T) {
	pt := New(Config{
		LocalID:   "node-main",
		LocalName: "dev",
	})

	err := pt.AddOrUpdatePeer("node-main", "somewhere:8080", false)
	if err == nil {
		t.Fatal("expected error when adding local node as peer")
	}
}

func TestConnectPeer_Unknown(t *testing.T) {
	pt := New(Config{
		LocalID:   "node-main",
		LocalName: "dev",
	})

	err := pt.ConnectPeer("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown peer")
	}
}

func TestConnectPeer_LocalRejected(t *testing.T) {
	pt := New(Config{
		LocalID:   "node-main",
		LocalName: "dev",
	})

	err := pt.ConnectPeer("node-main")
	if err == nil {
		t.Fatal("expected error when connecting to local node")
	}
}

func TestDisconnectPeer_Unknown(t *testing.T) {
	pt := New(Config{
		LocalID:   "node-main",
		LocalName: "dev",
	})

	// Disconnect of unknown peer should not error (best-effort)
	err := pt.DisconnectPeer("nonexistent")
	if err != nil {
		t.Fatalf("DisconnectPeer should not error for unknown peer, got: %v", err)
	}
}

func TestRemovePeer_RemovesFromListing(t *testing.T) {
	pt := New(Config{
		LocalID:   "node-main",
		LocalName: "dev",
		Peers: []PeerConfig{
			{ID: "node-remote", Address: "10.0.0.1:8080"},
		},
	})

	// Verify peer exists
	nodes := pt.ListNodes()
	if len(nodes) != 2 {
		t.Fatalf("expected 2 nodes before remove, got %d", len(nodes))
	}

	err := pt.RemovePeer("node-remote")
	if err != nil {
		t.Fatalf("RemovePeer failed: %v", err)
	}

	// Verify peer is removed
	nodes = pt.ListNodes()
	if len(nodes) != 1 {
		t.Fatalf("expected 1 node after remove, got %d", len(nodes))
	}
	if nodes[0].ID != "node-main" {
		t.Errorf("expected only local node, got %s", nodes[0].ID)
	}
}
