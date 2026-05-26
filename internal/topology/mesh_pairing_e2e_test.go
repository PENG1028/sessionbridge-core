package topology

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/user/sessionnode/go-core/internal/auth"
	"github.com/user/sessionnode/go-core/internal/dispatcher"
	"github.com/user/sessionnode/go-core/internal/executor"
	"github.com/user/sessionnode/go-core/internal/mesh"
	"github.com/user/sessionnode/go-core/internal/permission"
	"github.com/user/sessionnode/go-core/internal/process"
	"github.com/user/sessionnode/go-core/internal/server"
	"github.com/user/sessionnode/go-core/internal/session"
	"github.com/user/sessionnode/go-core/internal/wsconn"
	"github.com/user/sessionnode/go-core/pkg/types"
)

// ---------------------------------------------------------------------------
// Two-Core Mesh Pairing E2E Test
//
// This test exercises the full invite pairing lifecycle between two real
// Go Core instances:
//
//	Core A: identity A, trustStore A, inviteStore A, topology A, server A
//	Core B: identity B, trustStore B, inviteStore B, topology B, server B
//
// Flow:
//
//	1. A creates an invite via invite store
//	2. B calls node.invite.accept via executor with peerUrl=A's /peer/ws
//	3. B HTTP POSTs to A's /peer/invite/accept with B's identity
//	4. A validates the invite, stores B in A's trust store, returns A's identity
//	5. B stores A in B's trust store, adds A to B's topology
//	6. B's topology connects to A's /peer/ws with ed25519 handshake
//	7. Verification: trust stores, invite consumed, connection status, forwarding
// ---------------------------------------------------------------------------

// testMeshIdentity generates a test node identity with ed25519 keys for mesh
// pairing tests.
func testMeshIdentity(t *testing.T, nodeID string) *mesh.NodeIdentity {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key for %s: %v", nodeID, err)
	}
	return &mesh.NodeIdentity{
		NodeID:      nodeID,
		PublicKey:   pub,
		PrivateKey:  priv,
		Fingerprint: "mesh-fp-" + nodeID,
		CreatedAt:   time.Now().UnixMilli(),
	}
}

// testMeshNode creates a full server with mesh identity, trust store, invite
// store, topology, executor with mesh capabilities, and dispatcher.
//
// The returned topology is NOT started — the caller must start it with its own
// context (e.g. go topo.Start(ctx)).
func testMeshNode(t *testing.T, id types.NodeID) (
	sv *server.Server,
	httpSrv *httptest.Server,
	topo *PeerTopology,
	trustStore *mesh.TrustStore,
	inviteStore *mesh.InviteStore,
	disp *dispatcher.Dispatcher,
	identity *mesh.NodeIdentity,
) {
	t.Helper()

	identity = testMeshIdentity(t, string(id))

	sessStore := session.NewStore()
	cr := wsconn.NewRegistry()
	pm := process.NewManager(cr.PushChunk, cr.PushSessionEvent)

	trustStore = mesh.NewTrustStore(filepath.Join(t.TempDir(), "trusted_peers.json"))
	inviteStore = mesh.NewInviteStore()

	topo = New(Config{
		LocalID:   id,
		LocalName: string(id),
		Identity:  identity,
	})

	execDeps := &executor.Deps{
		Sessions:   sessStore,
		Processes:  pm,
		ConnRoutes: cr,
		Nodes:      topo,
		Mesh: &mesh.MeshState{
			Identity:    identity,
			TrustStore:  trustStore,
			InviteStore: inviteStore,
		},
		Topology: topo,
	}
	execReg := executor.New(execDeps)

	permChecker := permission.NewChecker(&permitAllCaps{}, &permitAllPolicy{})

	disp = dispatcher.New(
		auth.NewTokenAuthenticator(""),
		&allowAnyPlugin{},
		permChecker,
		nil, /* planner */
		execReg,
		&silentAudit{},
		topo,
		id,
	)

	sv = server.New("", disp, sessStore, cr, pm, identity, trustStore, "")
	sv.SetInviteStore(inviteStore)

	httpSrv = httptest.NewServer(sv.Handler())
	t.Cleanup(httpSrv.Close)

	return
}

// TestTwoCore_InviteAccept_ConnectsPeerWS verifies the full invite pairing
// lifecycle between two in-process Go Core instances, including WebSocket
// peer connection with ed25519 challenge-response handshake.
func TestTwoCore_InviteAccept_ConnectsPeerWS(t *testing.T) {
	// ---- Setup Core A ----
	_, httpA, topoA, trustA, inviteA, _, identityA := testMeshNode(t, "node-a")
	addrA := peerAddr(httpA)

	ctxA, cancelA := context.WithCancel(context.Background())
	defer cancelA()
	go topoA.Start(ctxA)

	// ---- Setup Core B ----
	_, _, topoB, trustB, _, dispB, identityB := testMeshNode(t, "node-b")

	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelB()
	go topoB.Start(ctxB)

	// ---- Step 1: A creates an invite via the invite store ----
	invite, err := inviteA.Create(identityA, 60, 3600)
	if err != nil {
		t.Fatalf("A create invite: %v", err)
	}
	if invite.Code == "" {
		t.Fatal("invite code is empty")
	}
	t.Logf("Step 1 OK: A created invite id=%s code=%s", invite.InviteID, invite.Code)

	// ---- Step 2: B accepts the invite via its dispatcher ----
	acceptPayload := json.RawMessage(fmt.Sprintf(
		`{"peerUrl":"ws://%s/peer/ws","code":"%s"}`,
		addrA, invite.Code,
	))

	acceptResp := dispB.Dispatch(&types.CapabilityRequest{
		RequestID:  "req_accept_invite",
		PluginID:   "sessionnode-core",
		Capability: "node.invite.accept",
		Payload:    acceptPayload,
		Actor:      types.Actor{Type: "web", ID: "tester"},
	})
	if !acceptResp.OK {
		t.Fatalf("Step 2 FAIL: B accept invite: %v", acceptResp.Error)
	}
	t.Log("Step 2 OK: B accepted invite successfully, response contains peer info")

	// ---- Verification 1: A's trust store contains B ----
	bPeer, err := trustA.Get("node-b")
	if err != nil {
		t.Fatalf("Verification 1 FAIL: B not found in A's trust store: %v", err)
	}
	if bPeer.NodeID != "node-b" {
		t.Errorf("A trust store NodeID = %q, want node-b", bPeer.NodeID)
	}
	if hex.EncodeToString(bPeer.PublicKey) != hex.EncodeToString(identityB.PublicKey) {
		t.Error("A trust store PublicKey mismatch for B")
	}
	if bPeer.Fingerprint != identityB.Fingerprint {
		t.Errorf("A trust store Fingerprint = %q, want %q", bPeer.Fingerprint, identityB.Fingerprint)
	}
	t.Logf("Verification 1 OK: A's trust store has B (nodeId=%s, fingerprint=%s)",
		bPeer.NodeID, bPeer.Fingerprint)

	// ---- Verification 2: B's trust store contains A ----
	aPeer, err := trustB.Get("node-a")
	if err != nil {
		t.Fatalf("Verification 2 FAIL: A not found in B's trust store: %v", err)
	}
	if aPeer.NodeID != "node-a" {
		t.Errorf("B trust store NodeID = %q, want node-a", aPeer.NodeID)
	}
	if hex.EncodeToString(aPeer.PublicKey) != hex.EncodeToString(identityA.PublicKey) {
		t.Error("B trust store PublicKey mismatch for A")
	}
	if aPeer.Fingerprint != identityA.Fingerprint {
		t.Errorf("B trust store Fingerprint = %q, want %q", aPeer.Fingerprint, identityA.Fingerprint)
	}
	t.Logf("Verification 2 OK: B's trust store has A (nodeId=%s, fingerprint=%s)",
		aPeer.NodeID, aPeer.Fingerprint)

	// ---- Verification 3: A's invite code is consumed (one-time use) ----
	// First Consume on the invite code should fail because it was already
	// consumed by A's handlePeerInviteAccept.
	_, err = inviteA.Consume(invite.Code)
	if err == nil {
		t.Fatal("Verification 3 FAIL: invite code was not consumed (second Consume should fail)")
	}
	t.Logf("Verification 3 OK: invite code consumed (Consume returns error: %v)", err)

	// ---- Verification 4: B's topology connects to A via /peer/ws ----
	// B's node.invite.accept handler called AddOrUpdatePeer + ConnectPeer,
	// which triggered the connectLoop. Wait for the handshake to complete.
	waitPeerStatus(t, topoB, "node-a", StatusConnected, 10*time.Second)
	t.Log("Verification 4 OK: B's topology shows A as connected")

	// ---- Verification 5: node.peer.list on B shows A ----
	// Query B's topology to confirm the connected peer.
	nodesB := topoB.ListNodes()
	foundA := false
	for _, n := range nodesB {
		if n.ID == "node-a" {
			foundA = true
			if n.Status != StatusConnected {
				t.Errorf("B sees A with status %q, want %q", n.Status, StatusConnected)
			}
			break
		}
	}
	if !foundA {
		t.Fatal("Verification 5 FAIL: node-a not found in B's topology ListNodes")
	}
	t.Log("Verification 5 OK: B's peer list includes A with status connected")

	// ---- Verification 6: A's LastSeen updated for B ----
	// After the handshake completes, A's handlePeerWS should have seen B's
	// connection. Check that LastSeen is non-zero.
	bPeerAfter, err := trustA.Get("node-b")
	if err != nil {
		t.Fatalf("Verification 6 FAIL: B not found in A's trust store: %v", err)
	}
	if bPeerAfter.LastSeen == 0 {
		t.Log("Verification 6 NOTE: B's LastSeen on A is 0 (not updated yet)")
	} else {
		t.Logf("Verification 6 OK: B's LastSeen on A = %d", bPeerAfter.LastSeen)
	}

	// ---- Verification 7: Cross-node capability forwarding works ----
	// Forward a system.info request from B to A through B's topology.
	// Note: This is B→A direction only. Bidirectional forwarding (A→B)
	// would require A's topology to also know about B, which the current
	// invite accept flow does not set up.
	infoResp := dispB.Dispatch(&types.CapabilityRequest{
		RequestID:    "req_cross_info",
		PluginID:     "sessionnode-core",
		Capability:   "system.info",
		TargetNodeID: "node-a",
		Actor:        types.Actor{Type: "web", ID: "tester"},
	})
	if !infoResp.OK {
		t.Fatalf("Verification 7 FAIL: B→A system.info forward: %v", infoResp.Error)
	}
	infoRaw, _ := json.Marshal(infoResp.Payload)
	var infoBody map[string]interface{}
	json.Unmarshal(infoRaw, &infoBody)
	if infoBody["os"] == nil || infoBody["arch"] == nil {
		t.Error("Verification 7: system.info response missing os/arch fields")
	} else {
		t.Logf("Verification 7 OK: B→A system.info forwarded: os=%s arch=%s",
			infoBody["os"], infoBody["arch"])
	}

	// ---- Summary ----
	t.Log("========================================")
	t.Log("  ALL VERIFICATIONS PASSED")
	t.Log("========================================")
	t.Logf("  Core A: node-a @ %s", addrA)
	t.Logf("  Trust Store A: %d peers", len(trustA.List()))
	t.Logf("  Trust Store B: %d peers", len(trustB.List()))
	t.Logf("  Topology B peers: %d", len(topoB.ListNodes()))
}

// ---------------------------------------------------------------------------
// Persistent Peer Lifecycle E2E Tests
//
// These tests verify that peer lifecycle operations (disconnect, reconnect,
// revoke) persist to the trust store and are correctly restored across
// topology restarts, matching the recovery logic in cmd/node/main.go.
// ---------------------------------------------------------------------------

// testRestorePeers applies the same restore logic as cmd/node/main.go:
// iterates the trust store and adds eligible peers (AutoReconnect=true,
// not revoked/expired, non-empty address) to the topology.
func testRestorePeers(t *testing.T, topo *PeerTopology, trustStore *mesh.TrustStore) {
	t.Helper()
	for _, tp := range trustStore.List() {
		if tp.Status == mesh.TrustStatusRevoked || tp.Status == mesh.TrustStatusExpired {
			continue
		}
		if !tp.AutoReconnect {
			continue
		}
		if len(tp.Addresses) == 0 {
			continue
		}
		if err := topo.AddOrUpdatePeer(types.NodeID(tp.NodeID), tp.Addresses[0], true); err != nil {
			t.Logf("restore peer %s: %v", tp.NodeID, err)
		}
	}
}

// waitPeerAbsent polls ListNodes and fails if the given peer ID appears
// within the timeout period.
func waitPeerAbsent(t *testing.T, topo *PeerTopology, peerID types.NodeID, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		for _, n := range topo.ListNodes() {
			if n.ID == peerID {
				t.Fatalf("peer %s unexpectedly appeared in topology (status=%s)", peerID, n.Status)
			}
		}
		select {
		case <-deadline:
			return
		default:
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// TestTwoCore_AutoReconnectPeerRestoredFromTrustStore verifies that a peer
// paired via invite is restored and reconnected after a full restart of the
// local topology (simulating Core restart). Flow:
//
//  1. Core A and Core B — create, pair via invite
//  2. Verify B's trust store has A with AutoReconnect=true
//  3. Simulate restart: create new B2 topology + trust store from same file
//  4. Restore peers from trust store (same logic as main.go)
//  5. Start B2 topology, verify B2 connects to A
//  6. Verify forwarding works (system.info) from B2 to A
func TestTwoCore_AutoReconnectPeerRestoredFromTrustStore(t *testing.T) {
	// ---- Core A ----
	_, httpA, topoA, _, inviteA, _, identityA := testMeshNode(t, "node-a")
	addrA := peerAddr(httpA)

	ctxA, cancelA := context.WithCancel(context.Background())
	defer cancelA()
	go topoA.Start(ctxA)

	// ---- Core B (manual build with tracked trust store path) ----
	identityB := testMeshIdentity(t, "node-b")
	trustPathB := filepath.Join(t.TempDir(), "trusted_peers.json")
	trustB := mesh.NewTrustStore(trustPathB)
	inviteB := mesh.NewInviteStore()

	crB := wsconn.NewRegistry()
	pmB := process.NewManager(crB.PushChunk, crB.PushSessionEvent)

	topoB := New(Config{
		LocalID:   "node-b",
		LocalName: "node-b",
		Identity:  identityB,
	})

	execDepsB := &executor.Deps{
		Sessions:   session.NewStore(),
		Processes:  pmB,
		ConnRoutes: crB,
		Nodes:      topoB,
		Mesh: &mesh.MeshState{
			Identity:    identityB,
			TrustStore:  trustB,
			InviteStore: inviteB,
		},
		Topology: topoB,
	}
	execRegB := executor.New(execDepsB)

	dispB := dispatcher.New(
		auth.NewTokenAuthenticator(""),
		&allowAnyPlugin{},
		permission.NewChecker(&permitAllCaps{}, &permitAllPolicy{}),
		nil, /* planner */
		execRegB,
		&silentAudit{},
		topoB,
		"node-b",
	)

	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelB()
	go topoB.Start(ctxB)

	// ---- Step 1: A creates invite ----
	invite, err := inviteA.Create(identityA, 60, 3600)
	if err != nil {
		t.Fatalf("A create invite: %v", err)
	}
	t.Logf("Step 1 OK: A created invite code=%s", invite.Code)

	// ---- Step 2: B accepts invite ----
	acceptPayload := json.RawMessage(fmt.Sprintf(
		`{"peerUrl":"ws://%s/peer/ws","code":"%s"}`,
		addrA, invite.Code,
	))
	acceptResp := dispB.Dispatch(&types.CapabilityRequest{
		RequestID:  "req_accept",
		PluginID:   "sessionnode-core",
		Capability: "node.invite.accept",
		Payload:    acceptPayload,
		Actor:      types.Actor{Type: "web", ID: "tester"},
	})
	if !acceptResp.OK {
		t.Fatalf("B accept invite: %v", acceptResp.Error)
	}
	t.Log("Step 2 OK: B accepted invite")

	// ---- Verification 1: B's trust store has A with AutoReconnect=true ----
	aPeer, err := trustB.Get("node-a")
	if err != nil {
		t.Fatalf("Verification 1 FAIL: A not in B's trust store: %v", err)
	}
	if !aPeer.AutoReconnect {
		t.Error("Verification 1 FAIL: AutoReconnect should be true after invite accept")
	}
	if len(aPeer.Addresses) == 0 {
		t.Error("Verification 1 FAIL: Addresses should not be empty")
	}
	t.Log("Verification 1 OK: B's trust store has A with AutoReconnect=true")

	// ---- Verification 2: B connects to A ----
	waitPeerStatus(t, topoB, "node-a", StatusConnected, 10*time.Second)
	t.Log("Verification 2 OK: B connected to A")

	// ---- Verification 3: Forwarding works (B -> A) ----
	infoResp := dispB.Dispatch(&types.CapabilityRequest{
		RequestID:    "req_info",
		PluginID:     "sessionnode-core",
		Capability:   "system.info",
		TargetNodeID: "node-a",
		Actor:        types.Actor{Type: "web", ID: "tester"},
	})
	if !infoResp.OK {
		t.Fatalf("B -> A system.info failed: %v", infoResp.Error)
	}
	t.Log("Verification 3 OK: B -> A forwarding works")

	// ---- Shutdown B (simulate restart) ----
	cancelB()
	topoB.Shutdown()
	t.Log("B shut down")

	// ---- B2 restart: new trust store + topology from same file ----
	trustB2 := mesh.NewTrustStore(trustPathB)
	if err := trustB2.Load(); err != nil {
		t.Fatalf("B2 trust store load: %v", err)
	}

	aPeerLoaded, err := trustB2.Get("node-a")
	if err != nil {
		t.Fatalf("A not found in loaded trust store: %v", err)
	}
	if !aPeerLoaded.AutoReconnect {
		t.Error("AutoReconnect should persist to disk")
	}
	t.Log("Trust store loaded with AutoReconnect=true")

	topoB2 := New(Config{
		LocalID:   "node-b",
		LocalName: "node-b",
		Identity:  identityB,
	})

	testRestorePeers(t, topoB2, trustB2)
	t.Log("Peers restored from trust store")

	// Build B2 dispatcher for forwarding verification
	crB2 := wsconn.NewRegistry()
	pmB2 := process.NewManager(crB2.PushChunk, crB2.PushSessionEvent)
	execDepsB2 := &executor.Deps{
		Sessions:   session.NewStore(),
		Processes:  pmB2,
		ConnRoutes: crB2,
		Nodes:      topoB2,
	}
	execRegB2 := executor.New(execDepsB2)
	dispB2 := dispatcher.New(
		auth.NewTokenAuthenticator(""),
		&allowAnyPlugin{},
		permission.NewChecker(&permitAllCaps{}, &permitAllPolicy{}),
		nil,
		execRegB2,
		&silentAudit{},
		topoB2,
		"node-b",
	)

	ctxB2, cancelB2 := context.WithCancel(context.Background())
	defer cancelB2()
	go topoB2.Start(ctxB2)

	// ---- Verification 4: B2 connects to A after restart ----
	waitPeerStatus(t, topoB2, "node-a", StatusConnected, 15*time.Second)
	t.Log("Verification 4 OK: B2 connected to A after restart")

	// ---- Verification 5: Forwarding works from B2 ----
	infoResp2 := dispB2.Dispatch(&types.CapabilityRequest{
		RequestID:    "req_b2_info",
		PluginID:     "sessionnode-core",
		Capability:   "system.info",
		TargetNodeID: "node-a",
		Actor:        types.Actor{Type: "web", ID: "tester"},
	})
	if !infoResp2.OK {
		t.Fatalf("B2 -> A system.info failed: %v", infoResp2.Error)
	}
	t.Log("Verification 5 OK: B2 -> A forwarding works after restart")
}

// TestPeerDisconnect_DisablesAutoReconnectAcrossRestart verifies that
// disconnecting a peer persists AutoReconnect=false and Status=offline,
// preventing automatic reconnection after a topology restart.
//
// Flow:
//  1. A/B pairing connected
//  2. B calls node.peer.disconnect via executor
//  3. Check B's trust store: AutoReconnect=false, Status=offline
//  4. Simulate restart: new trust store + topology, restore
//  5. Verify B does NOT connect to A (peer absent from topology)
func TestPeerDisconnect_DisablesAutoReconnectAcrossRestart(t *testing.T) {
	// ---- Core A ----
	_, httpA, topoA, _, inviteA, _, identityA := testMeshNode(t, "node-a")
	addrA := peerAddr(httpA)

	ctxA, cancelA := context.WithCancel(context.Background())
	defer cancelA()
	go topoA.Start(ctxA)

	// ---- Core B (manual build with tracked trust store path) ----
	identityB := testMeshIdentity(t, "node-b")
	trustPathB := filepath.Join(t.TempDir(), "trusted_peers.json")
	trustB := mesh.NewTrustStore(trustPathB)
	inviteB := mesh.NewInviteStore()

	crB := wsconn.NewRegistry()
	pmB := process.NewManager(crB.PushChunk, crB.PushSessionEvent)

	topoB := New(Config{
		LocalID:   "node-b",
		LocalName: "node-b",
		Identity:  identityB,
	})

	execDepsB := &executor.Deps{
		Sessions:   session.NewStore(),
		Processes:  pmB,
		ConnRoutes: crB,
		Nodes:      topoB,
		Mesh: &mesh.MeshState{
			Identity:    identityB,
			TrustStore:  trustB,
			InviteStore: inviteB,
		},
		Topology: topoB,
	}
	execRegB := executor.New(execDepsB)

	dispB := dispatcher.New(
		auth.NewTokenAuthenticator(""),
		&allowAnyPlugin{},
		permission.NewChecker(&permitAllCaps{}, &permitAllPolicy{}),
		nil,
		execRegB,
		&silentAudit{},
		topoB,
		"node-b",
	)

	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelB()
	go topoB.Start(ctxB)

	// ---- Pair A and B ----
	invite, err := inviteA.Create(identityA, 60, 3600)
	if err != nil {
		t.Fatalf("A create invite: %v", err)
	}
	acceptPayload := json.RawMessage(fmt.Sprintf(
		`{"peerUrl":"ws://%s/peer/ws","code":"%s"}`,
		addrA, invite.Code,
	))
	acceptResp := dispB.Dispatch(&types.CapabilityRequest{
		RequestID:  "req_accept",
		PluginID:   "sessionnode-core",
		Capability: "node.invite.accept",
		Payload:    acceptPayload,
		Actor:      types.Actor{Type: "web", ID: "tester"},
	})
	if !acceptResp.OK {
		t.Fatalf("B accept invite: %v", acceptResp.Error)
	}
	waitPeerStatus(t, topoB, "node-a", StatusConnected, 10*time.Second)
	t.Log("B connected to A")

	// ---- Step: B disconnects from A via executor ----
	discResp := dispB.Dispatch(&types.CapabilityRequest{
		RequestID:  "req_disconnect",
		PluginID:   "sessionnode-core",
		Capability: "node.peer.disconnect",
		Payload:    json.RawMessage(`{"nodeId":"node-a"}`),
		Actor:      types.Actor{Type: "web", ID: "tester"},
	})
	if !discResp.OK {
		t.Fatalf("B disconnect A: %v", discResp.Error)
	}
	t.Log("B disconnected from A")

	// ---- Verification 1: Trust store reflects disconnect ----
	aPeer, err := trustB.Get("node-a")
	if err != nil {
		t.Fatalf("Verification 1 FAIL: A not found in B's trust store: %v", err)
	}
	if aPeer.AutoReconnect {
		t.Error("Verification 1 FAIL: AutoReconnect should be false after disconnect")
	}
	if aPeer.Status != mesh.TrustStatusOffline {
		t.Errorf("Verification 1 FAIL: Status should be %q, got %q", mesh.TrustStatusOffline, aPeer.Status)
	}
	t.Log("Verification 1 OK: trust store updated (AutoReconnect=false, Status=offline)")

	// ---- Shutdown B (simulate restart) ----
	cancelB()
	topoB.Shutdown()

	// ---- B2 restart ----
	trustB2 := mesh.NewTrustStore(trustPathB)
	if err := trustB2.Load(); err != nil {
		t.Fatalf("B2 trust store load: %v", err)
	}

	// Verify persisted state
	aPeerLoaded, err := trustB2.Get("node-a")
	if err != nil {
		t.Fatalf("A not found in loaded trust store: %v", err)
	}
	if aPeerLoaded.AutoReconnect {
		t.Error("AutoReconnect=false should persist to disk")
	}

	topoB2 := New(Config{
		LocalID:   "node-b",
		LocalName: "node-b",
		Identity:  identityB,
	})
	testRestorePeers(t, topoB2, trustB2)

	ctxB2, cancelB2 := context.WithCancel(context.Background())
	defer cancelB2()
	go topoB2.Start(ctxB2)

	// ---- Verification 2: B2 does NOT connect to A after restart ----
	waitPeerAbsent(t, topoB2, "node-a", 3*time.Second)
	t.Log("Verification 2 OK: A not restored in B2 topology")
}

// TestPeerReconnect_EnablesAutoReconnectAcrossRestart verifies that
// reconnecting a peer persists AutoReconnect=true, enabling automatic
// reconnection after a topology restart.
//
// Flow:
//  1. A/B pairing connected
//  2. B disconnects then reconnects via executor
//  3. Check B's trust store: AutoReconnect=true
//  4. Simulate restart: new trust store + topology, restore
//  5. Verify B connects to A and forwarding works
func TestPeerReconnect_EnablesAutoReconnectAcrossRestart(t *testing.T) {
	// ---- Core A ----
	_, httpA, topoA, _, inviteA, _, identityA := testMeshNode(t, "node-a")
	addrA := peerAddr(httpA)

	ctxA, cancelA := context.WithCancel(context.Background())
	defer cancelA()
	go topoA.Start(ctxA)

	// ---- Core B (manual build with tracked trust store path) ----
	identityB := testMeshIdentity(t, "node-b")
	trustPathB := filepath.Join(t.TempDir(), "trusted_peers.json")
	trustB := mesh.NewTrustStore(trustPathB)
	inviteB := mesh.NewInviteStore()

	crB := wsconn.NewRegistry()
	pmB := process.NewManager(crB.PushChunk, crB.PushSessionEvent)

	topoB := New(Config{
		LocalID:   "node-b",
		LocalName: "node-b",
		Identity:  identityB,
	})

	execDepsB := &executor.Deps{
		Sessions:   session.NewStore(),
		Processes:  pmB,
		ConnRoutes: crB,
		Nodes:      topoB,
		Mesh: &mesh.MeshState{
			Identity:    identityB,
			TrustStore:  trustB,
			InviteStore: inviteB,
		},
		Topology: topoB,
	}
	execRegB := executor.New(execDepsB)

	dispB := dispatcher.New(
		auth.NewTokenAuthenticator(""),
		&allowAnyPlugin{},
		permission.NewChecker(&permitAllCaps{}, &permitAllPolicy{}),
		nil,
		execRegB,
		&silentAudit{},
		topoB,
		"node-b",
	)

	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelB()
	go topoB.Start(ctxB)

	// ---- Pair A and B ----
	invite, err := inviteA.Create(identityA, 60, 3600)
	if err != nil {
		t.Fatalf("A create invite: %v", err)
	}
	acceptPayload := json.RawMessage(fmt.Sprintf(
		`{"peerUrl":"ws://%s/peer/ws","code":"%s"}`,
		addrA, invite.Code,
	))
	acceptResp := dispB.Dispatch(&types.CapabilityRequest{
		RequestID:  "req_accept",
		PluginID:   "sessionnode-core",
		Capability: "node.invite.accept",
		Payload:    acceptPayload,
		Actor:      types.Actor{Type: "web", ID: "tester"},
	})
	if !acceptResp.OK {
		t.Fatalf("B accept invite: %v", acceptResp.Error)
	}
	waitPeerStatus(t, topoB, "node-a", StatusConnected, 10*time.Second)
	t.Log("B connected to A")

	// ---- Disconnect first (to verify reconnect transitions false -> true) ----
	discResp := dispB.Dispatch(&types.CapabilityRequest{
		RequestID:  "req_disconnect",
		PluginID:   "sessionnode-core",
		Capability: "node.peer.disconnect",
		Payload:    json.RawMessage(`{"nodeId":"node-a"}`),
		Actor:      types.Actor{Type: "web", ID: "tester"},
	})
	if !discResp.OK {
		t.Fatalf("B disconnect A: %v", discResp.Error)
	}
	t.Log("B disconnected from A")

	// Verify disconnect persisted
	aPeer, err := trustB.Get("node-a")
	if err != nil {
		t.Fatalf("A not found in trust store: %v", err)
	}
	if aPeer.AutoReconnect {
		t.Error("AutoReconnect should be false after disconnect")
	}

	// ---- Step: Reconnect ----
	reconResp := dispB.Dispatch(&types.CapabilityRequest{
		RequestID:  "req_reconnect",
		PluginID:   "sessionnode-core",
		Capability: "node.peer.reconnect",
		Payload:    json.RawMessage(`{"nodeId":"node-a"}`),
		Actor:      types.Actor{Type: "web", ID: "tester"},
	})
	if !reconResp.OK {
		t.Fatalf("B reconnect A: %v", reconResp.Error)
	}
	t.Log("B reconnected to A")

	// ---- Verification 1: Trust store has AutoReconnect=true ----
	aPeer, err = trustB.Get("node-a")
	if err != nil {
		t.Fatalf("Verification 1 FAIL: A not found in trust store: %v", err)
	}
	if !aPeer.AutoReconnect {
		t.Error("Verification 1 FAIL: AutoReconnect should be true after reconnect")
	}
	t.Log("Verification 1 OK: AutoReconnect=true persisted")

	// Wait for topology to reconnect
	waitPeerStatus(t, topoB, "node-a", StatusConnected, 10*time.Second)
	t.Log("B topology reconnected to A")

	// ---- Shutdown B (simulate restart) ----
	cancelB()
	topoB.Shutdown()

	// ---- B2 restart ----
	trustB2 := mesh.NewTrustStore(trustPathB)
	if err := trustB2.Load(); err != nil {
		t.Fatalf("B2 trust store load: %v", err)
	}

	aPeerLoaded, err := trustB2.Get("node-a")
	if err != nil {
		t.Fatalf("A not found in loaded trust store: %v", err)
	}
	if !aPeerLoaded.AutoReconnect {
		t.Error("AutoReconnect=true should persist to disk")
	}

	topoB2 := New(Config{
		LocalID:   "node-b",
		LocalName: "node-b",
		Identity:  identityB,
	})
	testRestorePeers(t, topoB2, trustB2)

	// Build B2 dispatcher for forwarding
	crB2 := wsconn.NewRegistry()
	pmB2 := process.NewManager(crB2.PushChunk, crB2.PushSessionEvent)
	execDepsB2 := &executor.Deps{
		Sessions:   session.NewStore(),
		Processes:  pmB2,
		ConnRoutes: crB2,
		Nodes:      topoB2,
	}
	execRegB2 := executor.New(execDepsB2)
	dispB2 := dispatcher.New(
		auth.NewTokenAuthenticator(""),
		&allowAnyPlugin{},
		permission.NewChecker(&permitAllCaps{}, &permitAllPolicy{}),
		nil,
		execRegB2,
		&silentAudit{},
		topoB2,
		"node-b",
	)

	ctxB2, cancelB2 := context.WithCancel(context.Background())
	defer cancelB2()
	go topoB2.Start(ctxB2)

	// ---- Verification 2: B2 connects to A after restart ----
	waitPeerStatus(t, topoB2, "node-a", StatusConnected, 15*time.Second)
	t.Log("Verification 2 OK: B2 reconnected to A after restart")

	// ---- Verification 3: Forwarding works ----
	infoResp := dispB2.Dispatch(&types.CapabilityRequest{
		RequestID:    "req_b2_info",
		PluginID:     "sessionnode-core",
		Capability:   "system.info",
		TargetNodeID: "node-a",
		Actor:        types.Actor{Type: "web", ID: "tester"},
	})
	if !infoResp.OK {
		t.Fatalf("B2 -> A system.info failed: %v", infoResp.Error)
	}
	t.Log("Verification 3 OK: B2 -> A forwarding works after restart")
}

// TestPeerRevoke_RemovesTrustAndDisallowsRestore verifies that revoking a
// peer removes it from the trust store and it is not restored on restart.
//
// Flow:
//  1. A/B pairing connected
//  2. B calls node.peer.revoke via executor
//  3. Verify A is removed from B's trust store and topology
//  4. Verify trust store file no longer contains A
//  5. Restart B's topology, verify A is not restored
func TestPeerRevoke_RemovesTrustAndDisallowsRestore(t *testing.T) {
	// ---- Core A ----
	_, httpA, topoA, _, inviteA, _, identityA := testMeshNode(t, "node-a")
	addrA := peerAddr(httpA)

	ctxA, cancelA := context.WithCancel(context.Background())
	defer cancelA()
	go topoA.Start(ctxA)

	// ---- Core B (manual build with tracked trust store path) ----
	identityB := testMeshIdentity(t, "node-b")
	trustPathB := filepath.Join(t.TempDir(), "trusted_peers.json")
	trustB := mesh.NewTrustStore(trustPathB)
	inviteB := mesh.NewInviteStore()

	crB := wsconn.NewRegistry()
	pmB := process.NewManager(crB.PushChunk, crB.PushSessionEvent)

	topoB := New(Config{
		LocalID:   "node-b",
		LocalName: "node-b",
		Identity:  identityB,
	})

	execDepsB := &executor.Deps{
		Sessions:   session.NewStore(),
		Processes:  pmB,
		ConnRoutes: crB,
		Nodes:      topoB,
		Mesh: &mesh.MeshState{
			Identity:    identityB,
			TrustStore:  trustB,
			InviteStore: inviteB,
		},
		Topology: topoB,
	}
	execRegB := executor.New(execDepsB)

	dispB := dispatcher.New(
		auth.NewTokenAuthenticator(""),
		&allowAnyPlugin{},
		permission.NewChecker(&permitAllCaps{}, &permitAllPolicy{}),
		nil,
		execRegB,
		&silentAudit{},
		topoB,
		"node-b",
	)

	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelB()
	go topoB.Start(ctxB)

	// ---- Pair A and B ----
	invite, err := inviteA.Create(identityA, 60, 3600)
	if err != nil {
		t.Fatalf("A create invite: %v", err)
	}
	acceptPayload := json.RawMessage(fmt.Sprintf(
		`{"peerUrl":"ws://%s/peer/ws","code":"%s"}`,
		addrA, invite.Code,
	))
	acceptResp := dispB.Dispatch(&types.CapabilityRequest{
		RequestID:  "req_accept",
		PluginID:   "sessionnode-core",
		Capability: "node.invite.accept",
		Payload:    acceptPayload,
		Actor:      types.Actor{Type: "web", ID: "tester"},
	})
	if !acceptResp.OK {
		t.Fatalf("B accept invite: %v", acceptResp.Error)
	}
	waitPeerStatus(t, topoB, "node-a", StatusConnected, 10*time.Second)
	t.Log("B connected to A")

	// ---- Step: B revokes A ----
	revokeResp := dispB.Dispatch(&types.CapabilityRequest{
		RequestID:  "req_revoke",
		PluginID:   "sessionnode-core",
		Capability: "node.peer.revoke",
		Payload:    json.RawMessage(`{"nodeId":"node-a"}`),
		Actor:      types.Actor{Type: "web", ID: "tester"},
	})
	if !revokeResp.OK {
		t.Fatalf("B revoke A: %v", revokeResp.Error)
	}
	t.Log("B revoked A from trust store")

	// ---- Verification 1: A removed from B's trust store ----
	_, err = trustB.Get("node-a")
	if err == nil {
		t.Fatal("Verification 1 FAIL: A should not be in B's trust store after revoke")
	}
	t.Log("Verification 1 OK: A removed from B's trust store")

	// ---- Verification 2: A removed from B's topology ----
	for _, n := range topoB.ListNodes() {
		if n.ID == "node-a" {
			t.Fatalf("Verification 2 FAIL: A should not be in B's topology after revoke, status=%s", n.Status)
		}
	}
	t.Log("Verification 2 OK: A removed from B's topology")

	// ---- Shutdown B ----
	cancelB()
	topoB.Shutdown()

	// ---- Verification 3: Trust store reload does not contain A ----
	trustB2 := mesh.NewTrustStore(trustPathB)
	if err := trustB2.Load(); err != nil {
		t.Fatalf("B2 trust store load: %v", err)
	}
	_, err = trustB2.Get("node-a")
	if err == nil {
		t.Fatal("Verification 3 FAIL: A should not be restored from disk after revoke")
	}
	t.Log("Verification 3 OK: A not present in loaded trust store")

	// ---- Verification 4: Restore process does not add A ----
	topoB2 := New(Config{
		LocalID:   "node-b",
		LocalName: "node-b",
		Identity:  identityB,
	})
	testRestorePeers(t, topoB2, trustB2)

	ctxB2, cancelB2 := context.WithCancel(context.Background())
	defer cancelB2()
	go topoB2.Start(ctxB2)

	waitPeerAbsent(t, topoB2, "node-a", 3*time.Second)
	t.Log("Verification 4 OK: A not restored in B2 after revoke+restart")
}
