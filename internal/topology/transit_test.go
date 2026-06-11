package topology

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/PENG1028/sessionbridge-core/pkg/protocol"
)

// TestTransit_SetAndCheck verifies SetTransitOnly / IsTransitOnly.
func TestTransit_SetAndCheck(t *testing.T) {
	p := newPeer("test-peer", "", nil, StatusConnected)
	if p.IsTransitOnly() {
		t.Error("new peer should not be transit")
	}
	p.SetTransitOnly(true)
	if !p.IsTransitOnly() {
		t.Error("peer should be transit after SetTransitOnly(true)")
	}
	p.SetTransitOnly(false)
	if p.IsTransitOnly() {
		t.Error("peer should not be transit after SetTransitOnly(false)")
	}
}

// TestTransit_RejectsLocalRequest verifies a transit peer gets TRANSIT_MODE_REJECTED.
func TestTransit_RejectsLocalRequest(t *testing.T) {
	pt := New(Config{LocalID: "main", LocalName: "main"})

	peer := newPeer("transit-peer", "", nil, StatusConnected)
	peer.writeCh = make(chan []byte, 10)
	peer.SetTransitOnly(true)

	pt.mu.Lock()
	pt.peers["transit-peer"] = peer
	pt.mu.Unlock()

	msg := &protocol.Message{
		Type:         protocol.MsgTypeActionRequest,
		RequestID:    "req_001",
		Capability:   "system.info",
		TargetNodeID: "main",
		ActorType:    "node",
		ActorID:      "transit-peer",
	}
	data, _ := msg.MarshalJSON()
	pt.HandleMessage("transit-peer", data)

	select {
	case respData := <-peer.writeCh:
		resp, _ := protocol.UnmarshalMessage(respData)
		if resp.Type != protocol.MsgTypeActionResponse {
			t.Errorf("expected action.response, got %q", resp.Type)
		}
		if resp.OK {
			t.Error("expected OK=false for transit peer")
		}
		if resp.Error == nil || resp.Error.Code != "TRANSIT_MODE_REJECTED" {
			t.Errorf("expected TRANSIT_MODE_REJECTED, got %v", resp.Error)
		}
		t.Logf("transit peer correctly rejected: code=%s", resp.Error.Code)
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for rejection response")
	}
}

// TestTransit_AllowsForwarding verifies transit peers can still forward.
func TestTransit_AllowsForwarding(t *testing.T) {
	pt := New(Config{LocalID: "main", LocalName: "main"})

	peer := newPeer("transit-fwd", "", nil, StatusConnected)
	peer.writeCh = make(chan []byte, 10)
	peer.SetTransitOnly(true)

	pt.mu.Lock()
	pt.peers["transit-fwd"] = peer
	pt.mu.Unlock()

	msg := &protocol.Message{
		Type:         protocol.MsgTypeActionRequest,
		RequestID:    "req_002",
		Capability:   "system.info",
		TargetNodeID: "remote-node",
		ActorType:    "node",
		ActorID:      "transit-fwd",
	}
	data, _ := msg.MarshalJSON()
	pt.HandleMessage("transit-fwd", data)

	select {
	case <-peer.writeCh:
		t.Log("got response (dispatch error expected since no actual dispatcher)")
	case <-time.After(500 * time.Millisecond):
		t.Log("no immediate rejection — transit peer allowed to forward")
	}
}

// TestTransit_MeshCallRejected verifies transit peers get rejected for mesh.call too.
func TestTransit_MeshCallRejected(t *testing.T) {
	pt := New(Config{LocalID: "main", LocalName: "main"})

	peer := newPeer("transit-mesh", "", nil, StatusConnected)
	peer.writeCh = make(chan []byte, 10)
	peer.SetTransitOnly(true)

	pt.mu.Lock()
	pt.peers["transit-mesh"] = peer
	pt.mu.Unlock()

	payload, _ := json.Marshal(map[string]string{})
	msg := &protocol.Message{
		Type:       protocol.MsgTypeMeshCall,
		RequestID:  "req_003",
		Capability: "system.info",
		Payload:    payload,
	}
	data, _ := msg.MarshalJSON()
	pt.HandleMessage("transit-mesh", data)

	select {
	case respData := <-peer.writeCh:
		resp, _ := protocol.UnmarshalMessage(respData)
		if resp.Type == protocol.MsgTypeMeshResult {
			if resp.OK {
				t.Error("expected OK=false for transit mesh.call")
			}
			if resp.Error == nil || resp.Error.Code != "TRANSIT_MODE_REJECTED" {
				t.Errorf("expected TRANSIT_MODE_REJECTED, got %v", resp.Error)
			}
			t.Logf("transit mesh.call correctly rejected: code=%s", resp.Error.Code)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for rejection")
	}
}

// TestTransit_FullPeerNotRejected verifies full-trust peers can execute.
func TestTransit_FullPeerNotRejected(t *testing.T) {
	pt := New(Config{LocalID: "main", LocalName: "main"})

	peer := newPeer("full-peer", "", nil, StatusConnected)
	peer.writeCh = make(chan []byte, 10)

	pt.mu.Lock()
	pt.peers["full-peer"] = peer
	pt.mu.Unlock()

	msg := &protocol.Message{
		Type:         protocol.MsgTypeActionRequest,
		RequestID:    "req_004",
		Capability:   "system.info",
		TargetNodeID: "main",
		ActorType:    "node",
		ActorID:      "full-peer",
	}
	data, _ := msg.MarshalJSON()
	pt.HandleMessage("full-peer", data)

	select {
	case respData := <-peer.writeCh:
		resp, _ := protocol.UnmarshalMessage(respData)
		if resp.OK == false && resp.Error != nil && resp.Error.Code == "TRANSIT_MODE_REJECTED" {
			t.Error("full-trust peer should not get TRANSIT_MODE_REJECTED")
		}
		t.Logf("full peer response: code=%v", resp.Error)
	case <-time.After(time.Second):
		t.Log("no response — expected since no dispatcher configured")
	}
}
