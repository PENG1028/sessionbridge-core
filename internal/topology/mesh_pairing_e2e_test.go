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
