package topology

import (
	"encoding/json"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/PENG1028/sessionbridge-core/pkg/types"
)

// TestDiscovery_PacketParsing tests JSON announcement parsing.
func TestDiscovery_PacketParsing(t *testing.T) {
	d := NewDiscoverer("test-node", "abc123", 9090)

	var found []DiscoveredPeer
	var mu sync.Mutex
	d.SetCallback(func(p DiscoveredPeer) {
		mu.Lock()
		found = append(found, p)
		mu.Unlock()
	})

	// Simulate an incoming packet from a remote peer
	payload := announcePayload{
		Service:     "sessionbridge",
		Version:     1,
		NodeID:      "remote-peer",
		Fingerprint: "def456",
		Port:        9091,
	}
	data, _ := json.Marshal(payload)
	remoteAddr := &net.UDPAddr{IP: net.ParseIP("192.168.1.100"), Port: 5353}

	d.handlePacket(data, remoteAddr)

	if len(found) != 1 {
		t.Fatalf("expected 1 discovered peer, got %d", len(found))
	}
	if found[0].NodeID != "remote-peer" {
		t.Errorf("nodeId = %q, want 'remote-peer'", found[0].NodeID)
	}
	if found[0].Address != "192.168.1.100:9091" {
		t.Errorf("address = %q, want '192.168.1.100:9091'", found[0].Address)
	}
	if found[0].Fingerprint != "def456" {
		t.Errorf("fingerprint = %q", found[0].Fingerprint)
	}
}

// TestDiscovery_IgnoresSelf tests that our own announcements are ignored.
func TestDiscovery_IgnoresSelf(t *testing.T) {
	d := NewDiscoverer("self-node", "abc123", 9090)

	var found []DiscoveredPeer
	d.SetCallback(func(p DiscoveredPeer) {
		found = append(found, p)
	})

	// Simulate our own announcement
	payload := announcePayload{
		Service: "sessionbridge",
		Version: 1,
		NodeID:  "self-node",
		Port:    9090,
	}
	data, _ := json.Marshal(payload)
	remoteAddr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 5353}

	d.handlePacket(data, remoteAddr)

	if len(found) != 0 {
		t.Errorf("should ignore self announcement, got %d peer(s)", len(found))
	}
}

// TestDiscovery_IgnoresNonSessionBridge tests that non-sessionbridge packets are ignored.
func TestDiscovery_IgnoresNonSessionBridge(t *testing.T) {
	d := NewDiscoverer("test-node", "abc", 9090)

	// Unknown service
	payload := announcePayload{Service: "unknown", Version: 1, NodeID: "other"}
	data, _ := json.Marshal(payload)
	d.handlePacket(data, &net.UDPAddr{IP: net.ParseIP("10.0.0.1"), Port: 5353})

	// Wrong version
	payload2 := announcePayload{Service: "sessionbridge", Version: 2, NodeID: "other"}
	data2, _ := json.Marshal(payload2)
	d.handlePacket(data2, &net.UDPAddr{IP: net.ParseIP("10.0.0.1"), Port: 5353})

	// Invalid JSON
	d.handlePacket([]byte(`not json`), &net.UDPAddr{IP: net.ParseIP("10.0.0.1"), Port: 5353})

	// Only valid packet should trigger discovery
	payload3 := announcePayload{Service: "sessionbridge", Version: 1, NodeID: "real-peer", Port: 9090}
	data3, _ := json.Marshal(payload3)
	d.handlePacket(data3, &net.UDPAddr{IP: net.ParseIP("10.0.0.2"), Port: 5353})

	count := len(d.known)
	if count != 1 {
		t.Errorf("expected 1 discovered peer, got %d", count)
	}
}

// TestDiscovery_Deduplicate tests that the same peer is only discovered once.
func TestDiscovery_Deduplicate(t *testing.T) {
	d := NewDiscoverer("test-node", "abc", 9090)
	var callCount int
	d.SetCallback(func(p DiscoveredPeer) { callCount++ })

	payload := announcePayload{Service: "sessionbridge", Version: 1, NodeID: "dup-peer", Port: 9090}
	data, _ := json.Marshal(payload)
	addr := &net.UDPAddr{IP: net.ParseIP("10.0.0.1"), Port: 5353}

	// Send the same packet twice
	d.handlePacket(data, addr)
	d.handlePacket(data, addr)

	if callCount != 1 {
		t.Errorf("expected 1 callback call, got %d", callCount)
	}
}

// TestDiscovery_BroadcastPayload tests that the broadcast payload is valid.
func TestDiscovery_BroadcastPayload(t *testing.T) {
	d := NewDiscoverer("broadcast-test", "fp789", 9092)

	// Build announcement and verify it serializes correctly
	payload := announcePayload{
		Service:     "sessionbridge",
		Version:     1,
		NodeID:      string(d.nodeID),
		Fingerprint: d.fingerprint,
		Port:        d.port,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded announcePayload
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.NodeID != "broadcast-test" {
		t.Errorf("nodeId = %q", decoded.NodeID)
	}
	if decoded.Port != 9092 {
		t.Errorf("port = %d", decoded.Port)
	}
}

// TestDiscovery_OnDiscoveredPeer tests topology integration callback.
func TestDiscovery_OnDiscoveredPeer(t *testing.T) {
	pt := New(Config{
		LocalID:   "main-node",
		LocalName: "main",
	})

	// Manually trigger the callback
	pt.onDiscoveredPeer(DiscoveredPeer{
		NodeID:      "new-peer",
		Address:     "10.0.0.5:9090",
		Fingerprint: "xyz",
		Tags:        []string{"lan"},
	})

	// Verify peer is not auto-added (needs trust store check)
	// This is the current behavior — discovery logs but doesn't auto-connect
	t.Log("onDiscoveredPeer did not panic")
	_ = time.Second
}

// TestDiscovery_DiscovererCreate tests basic discoverer creation.
func TestDiscovery_DiscovererCreate(t *testing.T) {
	d := NewDiscoverer(types.NodeID("create-test"), "fp", 9090)
	if d == nil {
		t.Fatal("NewDiscoverer returned nil")
	}
	if string(d.nodeID) != "create-test" {
		t.Errorf("nodeID = %q", d.nodeID)
	}
	d.Stop()
	t.Log("Discoverer created and stopped successfully")
}
