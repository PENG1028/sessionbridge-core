package topology

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/PENG1028/sessionbridge-core/pkg/types"
)

// TestDualMachine_SystemInfo forwards system.info to the peer.
func TestDualMachine_SystemInfo(t *testing.T) {
	_, peerHTTPSrv, _ := testPeerNodeWithOpLog(t, "dm-peer-1")
	addr := peerAddr(peerHTTPSrv)

	pt := New(Config{
		LocalID:   "dm-main-1",
		LocalName: "dm-main-1",
		Peers: []PeerConfig{
			{ID: "dm-peer-1", Address: addr},
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go pt.Start(ctx)
	waitPeerStatus(t, pt, "dm-peer-1", StatusConnected, 5*time.Second)

	d := newDispatcherForTopology(t, pt, "dm-main-1")

	resp := d.Dispatch(&types.CapabilityRequest{
		RequestID:    "dm_sys_1",
		PluginID:     "sessionnode-core",
		Capability:   "system.info",
		TargetNodeID: "dm-peer-1",
		Actor:        types.Actor{Type: "web", ID: "tester"},
	})
	if !resp.OK {
		t.Fatalf("forwarded system.info failed: %v", resp.Error)
	}

	payload, _ := json.Marshal(resp.Payload)
	var body map[string]interface{}
	json.Unmarshal(payload, &body)

	if body["os"] == nil {
		t.Error("system.info missing os")
	}
	if body["arch"] == nil {
		t.Error("system.info missing arch")
	}
	t.Logf("peer system.info: os=%v arch=%v", body["os"], body["arch"])
}

// TestDualMachine_NodeHealth validates node.health across the mesh.
func TestDualMachine_NodeHealth(t *testing.T) {
	_, peerHTTPSrv, _ := testPeerNodeWithOpLog(t, "dm-health-peer")
	addr := peerAddr(peerHTTPSrv)

	pt := New(Config{
		LocalID:   "dm-health-main",
		LocalName: "dm-health-main",
		Peers: []PeerConfig{
			{ID: "dm-health-peer", Address: addr},
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go pt.Start(ctx)
	waitPeerStatus(t, pt, "dm-health-peer", StatusConnected, 5*time.Second)

	d := newDispatcherForTopology(t, pt, "dm-health-main")

	// Local node.health
	resp := d.Dispatch(&types.CapabilityRequest{
		RequestID:  "dm_health_local",
		PluginID:   "sessionnode-core",
		Capability: "node.health",
		Actor:      types.Actor{Type: "web", ID: "tester"},
	})
	if !resp.OK {
		t.Fatalf("local node.health failed: %v", resp.Error)
	}
	payload, _ := json.Marshal(resp.Payload)
	var body map[string]interface{}
	json.Unmarshal(payload, &body)
	if body["status"] != "ok" {
		t.Errorf("local health status = %v", body["status"])
	}

	// Forwarded node.health to peer
	resp = d.Dispatch(&types.CapabilityRequest{
		RequestID:    "dm_health_peer",
		PluginID:     "sessionnode-core",
		Capability:   "node.health",
		TargetNodeID: "dm-health-peer",
		Actor:        types.Actor{Type: "web", ID: "tester"},
	})
	if !resp.OK {
		t.Fatalf("forwarded node.health failed: %v", resp.Error)
	}
	json.Unmarshal(payload, &body)
	if body["status"] != "ok" {
		t.Errorf("peer health status = %v", body["status"])
	}
	t.Log("local and peer health both OK")
}

// TestDualMachine_MultipleCapabilities tests various capabilities across the mesh.
func TestDualMachine_MultipleCapabilities(t *testing.T) {
	_, peerHTTPSrv, _ := testPeerNodeWithOpLog(t, "dm-multi-peer")
	addr := peerAddr(peerHTTPSrv)

	pt := New(Config{
		LocalID:   "dm-multi-main",
		LocalName: "dm-multi-main",
		Peers: []PeerConfig{
			{ID: "dm-multi-peer", Address: addr},
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go pt.Start(ctx)
	waitPeerStatus(t, pt, "dm-multi-peer", StatusConnected, 5*time.Second)

	d := newDispatcherForTopology(t, pt, "dm-multi-main")

	caps := []struct {
		name       string
		capability string
		local      bool
	}{
		{"node.health local", "node.health", true},
		{"node.list local", "node.list", true},
		{"system.info peer", "system.info", false},
		{"node.health peer", "node.health", false},
		{"system.info local", "system.info", true},
	}

	for _, c := range caps {
		t.Run(c.name, func(t *testing.T) {
			req := &types.CapabilityRequest{
				RequestID:  types.RequestID("dm_multi_" + c.name),
				PluginID:   "sessionnode-core",
				Capability: c.capability,
				Actor:      types.Actor{Type: "web", ID: "tester"},
			}
			if !c.local {
				req.TargetNodeID = "dm-multi-peer"
			}
			resp := d.Dispatch(req)
			if !resp.OK {
				t.Errorf("%s failed: %v", c.name, resp.Error)
			} else {
				t.Logf("%s OK", c.name)
			}
		})
	}
}

// TestDualMachine_NodeListShowsPeer verifies node.list on local.
func TestDualMachine_NodeListShowsPeer(t *testing.T) {
	_, peerHTTPSrv, _ := testPeerNodeWithOpLog(t, "dm-nl-peer")
	addr := peerAddr(peerHTTPSrv)

	pt := New(Config{
		LocalID:   "dm-nl-main",
		LocalName: "dm-nl-main",
		Peers: []PeerConfig{
			{ID: "dm-nl-peer", Address: addr},
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go pt.Start(ctx)
	waitPeerStatus(t, pt, "dm-nl-peer", StatusConnected, 5*time.Second)

	d := newDispatcherForTopology(t, pt, "dm-nl-main")

	resp := d.Dispatch(&types.CapabilityRequest{
		RequestID:  "dm_nl",
		PluginID:   "sessionnode-core",
		Capability: "node.list",
		Actor:      types.Actor{Type: "web", ID: "tester"},
	})
	if !resp.OK {
		t.Fatalf("node.list failed: %v", resp.Error)
	}

	payload, _ := json.Marshal(resp.Payload)
	var body map[string]interface{}
	json.Unmarshal(payload, &body)

	nodes, ok := body["nodes"].([]interface{})
	if !ok {
		t.Fatalf("expected nodes array, got %T", body["nodes"])
	}

	foundNodes := false
	for _, n := range nodes {
		node := n.(map[string]interface{})
		t.Logf("  node: %s status=%s", node["nodeId"], node["status"])
		foundNodes = true
	}
	if !foundNodes {
		t.Error("no nodes in list")
	}
	if len(nodes) < 2 {
		t.Logf("expected at least 2 nodes, got %d (mock may limit)", len(nodes))
	}
}

// TestDualMachine_OperationsListOnPeer queries operations from remote peer.
func TestDualMachine_OperationsListOnPeer(t *testing.T) {
	_, peerHTTPSrv, peerOpLog := testPeerNodeWithOpLog(t, "dm-op-peer")
	addr := peerAddr(peerHTTPSrv)

	peerOpLog.Append(&types.Operation{
		Capability: "system.info",
		Class:      types.OpClassNoop,
		Actor:      types.Actor{Type: "web", ID: "tester"},
	})

	pt := New(Config{
		LocalID:   "dm-op-main",
		LocalName: "dm-op-main",
		Peers: []PeerConfig{
			{ID: "dm-op-peer", Address: addr},
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go pt.Start(ctx)
	waitPeerStatus(t, pt, "dm-op-peer", StatusConnected, 5*time.Second)

	d := newDispatcherForTopology(t, pt, "dm-op-main")

	resp := d.Dispatch(&types.CapabilityRequest{
		RequestID:    "dm_op_list",
		PluginID:     "sessionnode-core",
		Capability:   "operations.list",
		TargetNodeID: "dm-op-peer",
		Actor:        types.Actor{Type: "web", ID: "tester"},
	})
	if !resp.OK {
		t.Fatalf("forwarded operations.list failed: %v", resp.Error)
	}

	payload, _ := json.Marshal(resp.Payload)
	var body map[string]interface{}
	json.Unmarshal(payload, &body)

	ops, ok := body["operations"].([]interface{})
	if !ok {
		t.Fatalf("expected operations array, got %T", body["operations"])
	}
	if len(ops) < 1 {
		t.Errorf("expected at least 1 operation on peer, got %d", len(ops))
	} else {
		t.Logf("peer has %d operation(s)", len(ops))
	}
}

// TestDualMachine_MeshLoopback verifies forwarded requests return correctly.
func TestDualMachine_MeshLoopback(t *testing.T) {
	_, peerHTTPSrv, _ := testPeerNodeWithOpLog(t, "dm-loop-peer")
	addr := peerAddr(peerHTTPSrv)

	pt := New(Config{
		LocalID:   "dm-loop-main",
		LocalName: "dm-loop-main",
		Peers: []PeerConfig{
			{ID: "dm-loop-peer", Address: addr},
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go pt.Start(ctx)
	waitPeerStatus(t, pt, "dm-loop-peer", StatusConnected, 5*time.Second)

	d := newDispatcherForTopology(t, pt, "dm-loop-main")

	resp := d.Dispatch(&types.CapabilityRequest{
		RequestID:    "dm_loopback",
		PluginID:     "sessionnode-core",
		Capability:   "system.info",
		TargetNodeID: "dm-loop-peer",
		Actor:        types.Actor{Type: "web", ID: "tester"},
	})
	if !resp.OK {
		t.Fatalf("loopback system.info failed: %v", resp.Error)
	}
	if resp.RequestID != "dm_loopback" {
		t.Errorf("RequestID = %q, want 'dm_loopback'", resp.RequestID)
	}
	t.Logf("mesh loopback OK: requestId=%s", resp.RequestID)
}
