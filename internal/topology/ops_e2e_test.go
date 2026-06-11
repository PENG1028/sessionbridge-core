package topology

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/PENG1028/sessionbridge-core/internal/auth"
	"github.com/PENG1028/sessionbridge-core/internal/content"
	"github.com/PENG1028/sessionbridge-core/internal/dispatcher"
	"github.com/PENG1028/sessionbridge-core/internal/executor"
	"github.com/PENG1028/sessionbridge-core/internal/oplog"
	"github.com/PENG1028/sessionbridge-core/internal/permission"
	"github.com/PENG1028/sessionbridge-core/internal/process"
	"github.com/PENG1028/sessionbridge-core/internal/server"
	"github.com/PENG1028/sessionbridge-core/internal/session"
	"github.com/PENG1028/sessionbridge-core/internal/wsconn"
	"github.com/PENG1028/sessionbridge-core/pkg/types"
	"net/http/httptest"
)

// ─── Multi-Node Operations & Rollback E2E ───────────────────────────────
//
// These tests validate operations.* and rollback across two Core nodes:
//   - Node A (main) forwards an action.request to Node B (peer)
//   - Node B executes it, recording an OpLog entry
//   - Node A can query Node B's operations via mesh forwarding
//
// Prerequisite: both nodes must have OpLog + RollbackEngine configured.

// testPeerNodeWithOpLog creates a peer node with OpLog, ContentStore, and RollbackEngine.
func testPeerNodeWithOpLog(t *testing.T, id types.NodeID) (*server.Server, *httptest.Server, *oplog.Store) {
	t.Helper()

	sessStore := session.NewStore()
	cr := wsconn.NewRegistry()
	pm := process.NewManager(cr.PushChunk, cr.PushSessionEvent)

	// OpLog setup
	tmpDir := t.TempDir()
	cs := content.NewStore(filepath.Join(tmpDir, "trash"))
	if err := cs.Load(); err != nil {
		t.Fatalf("content load: %v", err)
	}
	opLog := oplog.NewStore(filepath.Join(tmpDir, "oplog"), oplog.WithChunkSize(10), oplog.WithContentStore(cs))
	if err := opLog.Load(); err != nil {
		t.Fatalf("oplog load: %v", err)
	}
	t.Cleanup(func() { opLog.Close() })

	execDeps := &executor.Deps{
		Sessions:       sessStore,
		Processes:      pm,
		ConnRoutes:     cr,
		OpLog:          opLog,
		RollbackEngine: oplog.NewRollbackEngine(opLog, cs),
	}
	execReg := executor.New(execDeps)

	permChecker := permission.NewChecker(
		&permitAllCaps{},
		&permitAllPolicy{},
	)

	peerTopo := New(Config{LocalID: id, LocalName: string(id)})

	// OpLogRecorder adapter
	recorder := &nodeOpLogRecorder{store: opLog}

	d := dispatcher.New(
		auth.NewTokenAuthenticator(""),
		&allowAnyPlugin{},
		permChecker,
		nil, /* planner */
		execReg,
		&silentAudit{},
		recorder,
		peerTopo,
		id,
	)

	sv := server.New("", d, sessStore, cr, pm, nil, nil, "")
	httpSrv := httptest.NewServer(sv.Handler())
	t.Cleanup(httpSrv.Close)
	return sv, httpSrv, opLog
}

// nodeOpLogRecorder adapts oplog.Store to dispatcher.OpLogRecorder.
type nodeOpLogRecorder struct {
	store *oplog.Store
}

func (r *nodeOpLogRecorder) Record(req *types.CapabilityRequest, result interface{}, execErr error) (types.OpID, error) {
	opClass := executor.ClassifyCapability(req.Capability)
	op := &types.Operation{
		Capability: req.Capability,
		Class:      opClass,
		Actor:      req.Actor,
	}
	return r.store.Append(op)
}

// ── Tests ─────────────────────────────────────────────────────────────────

// TestE2E_ForwardOperationsList forwards operations.list to a peer and validates response.
func TestE2E_ForwardOperationsList(t *testing.T) {
	_, peerHTTPSrv, peerOpLog := testPeerNodeWithOpLog(t, "ops-peer")
	addr := peerAddr(peerHTTPSrv)

	pt := New(Config{
		LocalID:   "ops-main",
		LocalName: "ops-main",
		Peers: []PeerConfig{
			{ID: "ops-peer", Address: addr},
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go pt.Start(ctx)
	waitPeerStatus(t, pt, "ops-peer", StatusConnected, 5*time.Second)

	// Do some operations on the peer to create OpLog entries
	// Write directly to peer's OpLog to simulate recorded operations
	peerOpLog.Append(&types.Operation{
		Capability: "system.info",
		Class:      types.OpClassNoop,
		Actor:      types.Actor{Type: "web", ID: "tester"},
	})
	peerOpLog.Append(&types.Operation{
		Capability: "node.health",
		Class:      types.OpClassNoop,
		Actor:      types.Actor{Type: "web", ID: "tester"},
	})

	d := newDispatcherForTopology(t, pt, "ops-main")

	// Forward operations.list to peer
	resp := d.Dispatch(&types.CapabilityRequest{
		RequestID:    "req_op_list",
		PluginID:     "sessionnode-core",
		Capability:   "operations.list",
		TargetNodeID: "ops-peer",
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
	if len(ops) < 2 {
		t.Errorf("expected at least 2 operations on peer, got %d", len(ops))
	}
	t.Logf("forwarded operations.list: %d operation(s) on peer", len(ops))
}

// TestE2E_ForwardOperationsGet forwards operations.get to a peer.
func TestE2E_ForwardOperationsGet(t *testing.T) {
	_, peerHTTPSrv, peerOpLog := testPeerNodeWithOpLog(t, "opsget-peer")
	addr := peerAddr(peerHTTPSrv)

	pt := New(Config{
		LocalID:   "opsget-main",
		LocalName: "opsget-main",
		Peers: []PeerConfig{
			{ID: "opsget-peer", Address: addr},
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go pt.Start(ctx)
	waitPeerStatus(t, pt, "opsget-peer", StatusConnected, 5*time.Second)

	// Record an operation so we have a known OpID
	opID, _ := peerOpLog.Append(&types.Operation{
		Capability: "system.info",
		Class:      types.OpClassNoop,
		Actor:      types.Actor{Type: "web", ID: "tester"},
	})

	d := newDispatcherForTopology(t, pt, "opsget-main")

	// Forward operations.get to peer with the known OpID
	getPayload, _ := json.Marshal(map[string]string{"opId": string(opID)})
	resp := d.Dispatch(&types.CapabilityRequest{
		RequestID:    "req_op_get",
		PluginID:     "sessionnode-core",
		Capability:   "operations.get",
		TargetNodeID: "opsget-peer",
		Payload:      getPayload,
		Actor:        types.Actor{Type: "web", ID: "tester"},
	})
	if !resp.OK {
		t.Fatalf("forwarded operations.get failed: %v", resp.Error)
	}

	payload, _ := json.Marshal(resp.Payload)
	var body map[string]interface{}
	json.Unmarshal(payload, &body)

	op, ok := body["operation"]
	if !ok {
		t.Fatal("operations.get: missing 'operation' key in response")
	}
	opMap, ok := op.(map[string]interface{})
	if !ok {
		t.Fatalf("expected operation object, got %T", op)
	}
	if opMap["capability"] != "system.info" {
		t.Errorf("expected capability=system.info, got %v", opMap["capability"])
	}
	t.Logf("forwarded operations.get: capability=%v", opMap["capability"])
}

// TestE2E_ForwardOperationsDryRun forwards a dry-run rollback request to a peer.
func TestE2E_ForwardOperationsDryRun(t *testing.T) {
	_, peerHTTPSrv, peerOpLog := testPeerNodeWithOpLog(t, "dryrun-peer")
	addr := peerAddr(peerHTTPSrv)

	pt := New(Config{
		LocalID:   "dryrun-main",
		LocalName: "dryrun-main",
		Peers: []PeerConfig{
			{ID: "dryrun-peer", Address: addr},
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go pt.Start(ctx)
	waitPeerStatus(t, pt, "dryrun-peer", StatusConnected, 5*time.Second)

	// Record a content-class operation (supports dry-run rollback)
	opID, _ := peerOpLog.Append(&types.Operation{
		Capability: "fs.write",
		Class:      types.OpClassContent,
		Actor:      types.Actor{Type: "web", ID: "tester"},
	})

	d := newDispatcherForTopology(t, pt, "dryrun-main")

	dryRunPayload, _ := json.Marshal(map[string]string{"opId": string(opID)})
	resp := d.Dispatch(&types.CapabilityRequest{
		RequestID:    "req_dryrun",
		PluginID:     "sessionnode-core",
		Capability:   "operations.dryRun",
		TargetNodeID: "dryrun-peer",
		Payload:      dryRunPayload,
		Actor:        types.Actor{Type: "web", ID: "tester"},
	})
	if !resp.OK {
		t.Fatalf("forwarded operations.dryRun failed: %v", resp.Error)
	}

	payload, _ := json.Marshal(resp.Payload)
	var body map[string]interface{}
	json.Unmarshal(payload, &body)

	preview, ok := body["preview"]
	if !ok {
		t.Fatal("operations.dryRun: missing 'preview' key")
	}
	previewMap, ok := preview.(map[string]interface{})
	if !ok {
		t.Fatalf("expected preview object, got %T", preview)
	}
	t.Logf("forwarded operations.dryRun: reversible=%v", previewMap["reversible"])
}

// TestE2E_ForwardSystemInfoThenQueryOps forwards system.info then checks operations.list.
func TestE2E_ForwardSystemInfoThenQueryOps(t *testing.T) {
	_, peerHTTPSrv, _ := testPeerNodeWithOpLog(t, "chain-peer")
	addr := peerAddr(peerHTTPSrv)

	pt := New(Config{
		LocalID:   "chain-main",
		LocalName: "chain-main",
		Peers: []PeerConfig{
			{ID: "chain-peer", Address: addr},
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go pt.Start(ctx)
	waitPeerStatus(t, pt, "chain-peer", StatusConnected, 5*time.Second)

	d := newDispatcherForTopology(t, pt, "chain-main")

	// Step 1: Forward system.info to peer — this creates an OpLog entry on the peer
	sysResp := d.Dispatch(&types.CapabilityRequest{
		RequestID:    "req_sys",
		PluginID:     "sessionnode-core",
		Capability:   "system.info",
		TargetNodeID: "chain-peer",
		Actor:        types.Actor{Type: "web", ID: "tester"},
	})
	if !sysResp.OK {
		t.Fatalf("forwarded system.info failed: %v", sysResp.Error)
	}

	// Step 2: Now query operations.list on the same peer
	opResp := d.Dispatch(&types.CapabilityRequest{
		RequestID:    "req_ops_after",
		PluginID:     "sessionnode-core",
		Capability:   "operations.list",
		TargetNodeID: "chain-peer",
		Actor:        types.Actor{Type: "web", ID: "tester"},
	})
	if !opResp.OK {
		t.Fatalf("forwarded operations.list after system.info failed: %v", opResp.Error)
	}

	payload, _ := json.Marshal(opResp.Payload)
	var body map[string]interface{}
	json.Unmarshal(payload, &body)

	ops, ok := body["operations"].([]interface{})
	if !ok {
		t.Fatalf("expected operations array, got %T", body["operations"])
	}
	t.Logf("peer operations after system.info: %d operation(s)", len(ops))
}

// TestE2E_ForwardRollbackVerify forwards operations.verify to a peer.
func TestE2E_ForwardRollbackVerify(t *testing.T) {
	_, peerHTTPSrv, _ := testPeerNodeWithOpLog(t, "verify-peer")
	addr := peerAddr(peerHTTPSrv)

	pt := New(Config{
		LocalID:   "verify-main",
		LocalName: "verify-main",
		Peers: []PeerConfig{
			{ID: "verify-peer", Address: addr},
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go pt.Start(ctx)
	waitPeerStatus(t, pt, "verify-peer", StatusConnected, 5*time.Second)

	d := newDispatcherForTopology(t, pt, "verify-main")

	// Forward operations.verify to peer
	resp := d.Dispatch(&types.CapabilityRequest{
		RequestID:    "req_verify",
		PluginID:     "sessionnode-core",
		Capability:   "operations.verify",
		TargetNodeID: "verify-peer",
		Actor:        types.Actor{Type: "web", ID: "tester"},
	})
	if !resp.OK {
		t.Fatalf("forwarded operations.verify failed: %v", resp.Error)
	}

	payload, _ := json.Marshal(resp.Payload)
	var body map[string]interface{}
	json.Unmarshal(payload, &body)

	rawArtifacts := body["artifacts"]
	if rawArtifacts == nil {
		t.Log("forwarded operations.verify: artifacts=nil (no operations to verify)")
	} else {
		reports, ok := rawArtifacts.([]interface{})
		if !ok {
			t.Fatalf("expected artifacts array, got %T", rawArtifacts)
		}
		t.Logf("forwarded operations.verify: %d artifact report(s)", len(reports))
	}

	// Also verify the content key exists
	if body["content"] == nil {
		t.Log("forwarded operations.verify: content=nil")
	}
	if _, ok := body["totalChecked"]; !ok {
		t.Error("operations.verify: missing totalChecked")
	}
}
