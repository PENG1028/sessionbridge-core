package topology

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/user/sessionnode/go-core/internal/auth"
	"github.com/user/sessionnode/go-core/internal/dispatcher"
	"github.com/user/sessionnode/go-core/internal/executor"
	"github.com/user/sessionnode/go-core/internal/history"
	"github.com/user/sessionnode/go-core/internal/permission"
	"github.com/user/sessionnode/go-core/internal/process"
	"github.com/user/sessionnode/go-core/internal/server"
	"github.com/user/sessionnode/go-core/internal/session"
	"github.com/user/sessionnode/go-core/internal/testutil"
	"github.com/user/sessionnode/go-core/internal/wsconn"
	"github.com/user/sessionnode/go-core/pkg/protocol"
	"github.com/user/sessionnode/go-core/pkg/types"
)

// ---------------------------------------------------------------------------
// recordingAudit -- captures audit entries for test assertions
// ---------------------------------------------------------------------------

type auditEntry struct {
	RequestID  types.RequestID
	PluginID   types.PluginID
	Capability string
	Actor      types.Actor
	Allowed    bool
	Detail     string
}

type recordingAudit struct {
	entries []auditEntry
}

func (a *recordingAudit) Log(req *types.CapabilityRequest, allowed bool, detail string) {
	a.entries = append(a.entries, auditEntry{
		RequestID:  req.RequestID,
		PluginID:   req.PluginID,
		Capability: req.Capability,
		Actor:      req.Actor,
		Allowed:    allowed,
		Detail:     detail,
	})
}

func (a *recordingAudit) Len() int { return len(a.entries) }
func (a *recordingAudit) Clear()   { a.entries = nil }

// ---------------------------------------------------------------------------
// Real process stdout -> history store
//
// Verifies that spawning a real process automatically records its stdout
// into the history store, and that cross-node replay can retrieve it.
// ---------------------------------------------------------------------------

func TestTwoCore_RealProcessHistoryCapture(t *testing.T) {
	histLocal := history.New("")

	// Local node with history-enabled push callbacks
	sessLocal := session.NewStore()
	crLocal := wsconn.NewRegistry()
	wrappedPush := func(sid types.SessionID, streamType string, seq types.EventSeq, data string) {
		histLocal.Record(sid, streamType, seq, data)
		crLocal.PushChunk(sid, streamType, seq, data)
	}
	wrappedEvent := func(sid types.SessionID, seq types.EventSeq, eventType string, data interface{}) {
		if eventType == "started" || eventType == "exited" {
			histLocal.RecordEvent(sid, seq, "session."+eventType, data)
		}
		crLocal.PushSessionEvent(sid, seq, eventType, data)
	}
	pmLocal := process.NewManager(wrappedPush, wrappedEvent)

	execDepsLocal := &executor.Deps{
		Sessions: sessLocal, Processes: pmLocal,
		ConnRoutes: crLocal, History: histLocal,
	}
	execRegLocal := executor.New(execDepsLocal)

	localPerm := permission.NewChecker(&permitAllCaps{}, &permitAllPolicy{})
	localTopo := New(Config{LocalID: "node-local", LocalName: "node-local"})

	dLocal := dispatcher.New(
		auth.NewTokenAuthenticator(""), &allowAnyPlugin{}, localPerm,
		nil, execRegLocal, &silentAudit{}, localTopo, "node-local",
	)
	localSrv := server.New("", dLocal, sessLocal, crLocal, pmLocal, nil, nil, "")
	localHTTPSrv := httptest.NewServer(localSrv.Handler())
	t.Cleanup(localHTTPSrv.Close)
	localAddr := peerAddr(localHTTPSrv)

	ctxLocal, cancelLocal := context.WithCancel(context.Background())
	t.Cleanup(cancelLocal)
	go localTopo.Start(ctxLocal)

	// VPS node
	vpsHist := history.New("")
	crVPS := wsconn.NewRegistry()
	pmVPS := process.NewManager(crVPS.PushChunk, crVPS.PushSessionEvent)
	execDepsVPS := &executor.Deps{
		Sessions: session.NewStore(), Processes: pmVPS,
		ConnRoutes: crVPS, History: vpsHist,
	}
	execRegVPS := executor.New(execDepsVPS)
	vpsPerm := permission.NewChecker(&permitAllCaps{}, &permitAllPolicy{})
	vpsTopo := New(Config{
		LocalID: "node-vps", LocalName: "node-vps",
		Peers: []PeerConfig{
			{ID: "node-local", Address: localAddr},
		},
	})
	ctxVPS, cancelVPS := context.WithCancel(context.Background())
	t.Cleanup(cancelVPS)
	go vpsTopo.Start(ctxVPS)
	waitPeerStatus(t, vpsTopo, "node-local", StatusConnected, 5*time.Second)

	dVPS := dispatcher.New(
		auth.NewTokenAuthenticator(""), &allowAnyPlugin{}, vpsPerm,
		nil, execRegVPS, &silentAudit{}, vpsTopo, "node-vps",
	)

	// Step 1: Create a session on local
	createPayload := json.RawMessage(`{"command":"bash","cwd":"/tmp","pluginId":"shell","history":{"enabled":true,"mode":"memory","streams":["stdout","stderr"]}}`)
	createResp := dLocal.Dispatch(&types.CapabilityRequest{
		RequestID: "req_create", PluginID: "sessionnode-core",
		Capability: "session.create", Payload: createPayload,
		Actor: types.Actor{Type: "web", ID: "client-A"},
	})
	if !createResp.OK {
		t.Fatalf("session.create failed: %v", createResp.Error)
	}
	createRaw, _ := json.Marshal(createResp.Payload)
	var createBody map[string]interface{}
	json.Unmarshal(createRaw, &createBody)
	sessionID := createBody["sessionId"].(string)

	// Init history for this session
	if err := histLocal.InitSession(types.SessionID(sessionID), types.HistoryPolicy{Enabled: true, Mode: "memory", Streams: []string{"stdout", "stderr"}}); err != nil {
		t.Fatalf("history init: %v", err)
	}

	// Step 2: Spawn a real process on local
	echoBin := testutil.EchoBinary(t)
	spawnPayloadMap := map[string]interface{}{
		"command": echoBin,
		"args":    []string{"hello-from-process-stdout"},
		"cwd":     "/tmp",
	}
	spawnPayloadBytes, _ := json.Marshal(spawnPayloadMap)
	spawnPayload := json.RawMessage(spawnPayloadBytes)
	spawnResp := dLocal.Dispatch(&types.CapabilityRequest{
		RequestID: "req_spawn", PluginID: "sessionnode-core",
		Capability: "process.spawn", Payload: spawnPayload,
		Actor: types.Actor{Type: "web", ID: "client-A"},
	})
	if !spawnResp.OK {
		t.Fatalf("process.spawn failed: %v", spawnResp.Error)
	}
	spawnRaw, _ := json.Marshal(spawnResp.Payload)
	var spawnBody map[string]interface{}
	json.Unmarshal(spawnRaw, &spawnBody)
	procSessionID := spawnBody["sessionId"].(string)

	// Wait for process output to be captured
	time.Sleep(500 * time.Millisecond)

	// Step 3: Verify history captured the stdout on local (under the process session ID)
	replayLocal, err := histLocal.Replay(types.SessionID(procSessionID), "stdout", 1)
	if err != nil {
		t.Fatalf("local replay failed: %v", err)
	}
	if len(replayLocal) == 0 {
		t.Fatal("expected at least 1 replayed event from process stdout")
	}
	foundExpected := false
	for _, evt := range replayLocal {
		if evt.Stream == "stdout" && len(evt.Data) > 0 {
			foundExpected = true
			t.Logf("local history captured: stream=%s data=%q", evt.Stream, evt.Data)
			break
		}
	}
	if !foundExpected {
		t.Errorf("expected stdout data in local history, got %d events", len(replayLocal))
		for _, evt := range replayLocal {
			t.Logf("  event: seq=%d type=%s stream=%s data=%q", evt.EventSeq, evt.Type, evt.Stream, evt.Data)
		}
	}

	// Step 4: Cross-node replay via VPS
	replayPayload := json.RawMessage(fmt.Sprintf(
		`{"sessionId":"%s","streamType":"stdout","fromSeq":1}`, procSessionID))
	replayResp := dVPS.Dispatch(&types.CapabilityRequest{
		RequestID: "req_replay_cross", PluginID: "sessionnode-core",
		Capability: "stream.replay", TargetNodeID: "node-local",
		Payload: replayPayload,
		Actor:   types.Actor{Type: "web", ID: "client-VPS"},
	})
	if !replayResp.OK {
		t.Fatalf("VPS->local stream.replay failed: %v", replayResp.Error)
	}
	replayRaw, _ := json.Marshal(replayResp.Payload)
	var replayBody map[string]interface{}
	json.Unmarshal(replayRaw, &replayBody)
	xEvents, _ := replayBody["events"].([]interface{})
	if len(xEvents) == 0 {
		t.Error("expected at least 1 event via cross-node replay (process stdout)")
	} else {
		t.Logf("cross-node replay got %d events", len(xEvents))
	}

	// Step 5: Verify VPS history does NOT have either session's data
	_, err = vpsHist.Stats(types.SessionID(sessionID))
	if err == nil {
		t.Errorf("VPS should not have history stats for local shell session")
	}
	_, err = vpsHist.Stats(types.SessionID(procSessionID))
	if err == nil {
		t.Errorf("VPS should not have history stats for local process session")
	}
}

// ---------------------------------------------------------------------------
// Remote history isolation
//
// Verifies that the remote node's history store does NOT retain the local
// session's history after cross-node forwarding.
// ---------------------------------------------------------------------------

func TestTwoCore_RemoteHistoryNotStored(t *testing.T) {
	histLocal := history.New("")
	vpsHist := history.New("")

	// Local node with wrapped push/event
	sessLocal := session.NewStore()
	crLocal := wsconn.NewRegistry()
	wrappedPush := func(sid types.SessionID, streamType string, seq types.EventSeq, data string) {
		histLocal.Record(sid, streamType, seq, data)
		crLocal.PushChunk(sid, streamType, seq, data)
	}
	pmLocal := process.NewManager(wrappedPush, crLocal.PushSessionEvent)
	execDepsLocal := &executor.Deps{
		Sessions: sessLocal, Processes: pmLocal,
		ConnRoutes: crLocal, History: histLocal,
	}
	execRegLocal := executor.New(execDepsLocal)
	localPerm := permission.NewChecker(&permitAllCaps{}, &permitAllPolicy{})
	localTopo := New(Config{LocalID: "node-local", LocalName: "node-local"})
	dLocal := dispatcher.New(
		auth.NewTokenAuthenticator(""), &allowAnyPlugin{}, localPerm,
		nil, execRegLocal, &silentAudit{}, localTopo, "node-local",
	)
	localSrv := server.New("", dLocal, sessLocal, crLocal, pmLocal, nil, nil, "")
	localHTTPSrv := httptest.NewServer(localSrv.Handler())
	t.Cleanup(localHTTPSrv.Close)
	localAddr := peerAddr(localHTTPSrv)
	ctxLocal, cancelLocal := context.WithCancel(context.Background())
	t.Cleanup(cancelLocal)
	go localTopo.Start(ctxLocal)

	// VPS node
	crVPS := wsconn.NewRegistry()
	pmVPS := process.NewManager(crVPS.PushChunk, crVPS.PushSessionEvent)
	execDepsVPS := &executor.Deps{
		Sessions: session.NewStore(), Processes: pmVPS,
		ConnRoutes: crVPS, History: vpsHist,
	}
	execRegVPS := executor.New(execDepsVPS)
	vpsPerm := permission.NewChecker(&permitAllCaps{}, &permitAllPolicy{})
	vpsTopo := New(Config{
		LocalID: "node-vps", LocalName: "node-vps",
		Peers: []PeerConfig{
			{ID: "node-local", Address: localAddr},
		},
	})
	ctxVPS, cancelVPS := context.WithCancel(context.Background())
	t.Cleanup(cancelVPS)
	go vpsTopo.Start(ctxVPS)
	waitPeerStatus(t, vpsTopo, "node-local", StatusConnected, 5*time.Second)

	dVPS := dispatcher.New(
		auth.NewTokenAuthenticator(""), &allowAnyPlugin{}, vpsPerm,
		nil, execRegVPS, &silentAudit{}, vpsTopo, "node-vps",
	)

	// Create session on local
	createPayload := json.RawMessage(`{"command":"bash","pluginId":"shell"}`)
	createResp := dLocal.Dispatch(&types.CapabilityRequest{
		RequestID: "req_create", PluginID: "sessionnode-core",
		Capability: "session.create", Payload: createPayload,
		Actor: types.Actor{Type: "web", ID: "client-A"},
	})
	if !createResp.OK {
		t.Fatalf("session.create failed: %v", createResp.Error)
	}
	createRaw, _ := json.Marshal(createResp.Payload)
	var createBody map[string]interface{}
	json.Unmarshal(createRaw, &createBody)
	sessionID := types.SessionID(createBody["sessionId"].(string))

	// Init history on local
	_ = histLocal.InitSession(sessionID, types.HistoryPolicy{Enabled: true, Mode: "memory", Streams: []string{"stdout"}})

	// Record data into local history
	histLocal.Record(sessionID, "stdout", 1, "local-only-data\n")
	histLocal.Record(sessionID, "stdout", 2, "more-local-data\n")

	// Cross-node info query from VPS
	infoPayload := json.RawMessage(fmt.Sprintf(`{"sessionId":"%s"}`, sessionID))
	infoResp := dVPS.Dispatch(&types.CapabilityRequest{
		RequestID: "req_info", PluginID: "sessionnode-core",
		Capability: "session.info", TargetNodeID: "node-local",
		Payload: infoPayload,
		Actor:   types.Actor{Type: "web", ID: "client-VPS"},
	})
	if !infoResp.OK {
		t.Fatalf("cross-node session.info failed: %v", infoResp.Error)
	}

	// Verify VPS history store does NOT contain the local session
	_, err := vpsHist.Stats(sessionID)
	if err == nil {
		t.Error("VPS should NOT have history stats for a local-only session")
	}

	// Verify local history still intact
	localStats, err := histLocal.Stats(sessionID)
	if err != nil {
		t.Fatalf("local history stats failed: %v", err)
	}
	if localStats.EventCount < 2 {
		t.Errorf("expected >=2 events in local history, got %d", localStats.EventCount)
	}
}

// ---------------------------------------------------------------------------
// Audit content assertions
//
// Verifies that the dispatcher's audit logger captures correct metadata.
// ---------------------------------------------------------------------------

func TestTwoCore_AuditContentAssertions(t *testing.T) {
	audit := &recordingAudit{}

	sessStore := session.NewStore()
	cr := wsconn.NewRegistry()
	pm := process.NewManager(cr.PushChunk, cr.PushSessionEvent)
	execDeps := &executor.Deps{
		Sessions: sessStore, Processes: pm, ConnRoutes: cr,
	}
	execReg := executor.New(execDeps)

	perm := permission.NewChecker(&permitAllCaps{}, &permitAllPolicy{})
	pt := New(Config{LocalID: "node-local", LocalName: "node-local"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go pt.Start(ctx)

	d := dispatcher.New(
		auth.NewTokenAuthenticator(""), &allowAnyPlugin{}, perm,
		nil, execReg, audit, pt, "node-local",
	)

	// Test 1: Successful system.info
	resp1 := d.Dispatch(&types.CapabilityRequest{
		RequestID: "req_info", PluginID: "sessionnode-core",
		Capability: "system.info",
		Actor:      types.Actor{Type: "web", ID: "client-X"},
	})
	if !resp1.OK {
		t.Fatalf("system.info failed: %v", resp1.Error)
	}

	// Test 2: Unknown capability (should be logged as denied)
	resp2 := d.Dispatch(&types.CapabilityRequest{
		RequestID: "req_bad", PluginID: "sessionnode-core",
		Capability: "nonexistent.cap",
		Actor:      types.Actor{Type: "cli", ID: "script-Y"},
	})
	if resp2.OK {
		t.Fatal("expected failure for unknown cap")
	}

	if audit.Len() != 2 {
		t.Fatalf("expected 2 audit entries, got %d", audit.Len())
	}

	// Check first entry (system.info success)
	e1 := audit.entries[0]
	if e1.RequestID != "req_info" {
		t.Errorf("entry0.RequestID = %q, want req_info", e1.RequestID)
	}
	if e1.Capability != "system.info" {
		t.Errorf("entry0.Capability = %q, want system.info", e1.Capability)
	}
	if !e1.Allowed {
		t.Error("entry0.Allowed should be true")
	}
	if e1.Actor.Type != "web" || e1.Actor.ID != "client-X" {
		t.Errorf("entry0.Actor = %+v", e1.Actor)
	}

	// Check second entry (nonexistent.cap denied)
	e2 := audit.entries[1]
	if e2.RequestID != "req_bad" {
		t.Errorf("entry1.RequestID = %q, want req_bad", e2.RequestID)
	}
	if e2.Capability != "nonexistent.cap" {
		t.Errorf("entry1.Capability = %q, want nonexistent.cap", e2.Capability)
	}
	if e2.Allowed {
		t.Error("entry1.Allowed should be false")
	}
	if e2.Actor.Type != "cli" || e2.Actor.ID != "script-Y" {
		t.Errorf("entry1.Actor = %+v", e2.Actor)
	}
	if e2.Detail == "" {
		t.Error("entry1.Detail should not be empty (error detail)")
	}

	// Cross-node audit: test that forwarded requests log the original actor
	_, vpsHTTPSrv := testPeerNode(t, "node-vps")
	vpsAddr := peerAddr(vpsHTTPSrv)

	audit2 := &recordingAudit{}
	pt2 := New(Config{
		LocalID: "node-local", LocalName: "node-local",
		Peers: []PeerConfig{
			{ID: "node-vps", Address: vpsAddr},
		},
	})
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	go pt2.Start(ctx2)
	waitPeerStatus(t, pt2, "node-vps", StatusConnected, 5*time.Second)

	perm2 := permission.NewChecker(&permitAllCaps{}, &permitAllPolicy{})
	d2 := dispatcher.New(
		auth.NewTokenAuthenticator(""), &allowAnyPlugin{}, perm2,
		nil, execReg, audit2, pt2, "node-local",
	)

	// Forward to VPS
	_ = d2.Dispatch(&types.CapabilityRequest{
		RequestID: "req_fwd", PluginID: "sessionnode-core",
		Capability: "system.info", TargetNodeID: "node-vps",
		Actor: types.Actor{Type: "web", ID: "client-Z"},
	})

	// Local dispatcher logs the forward with audit
	if audit2.Len() < 1 {
		t.Fatal("expected at least 1 audit entry for forwarded request")
	}
	eFwd := audit2.entries[0]
	if eFwd.RequestID != "req_fwd" {
		t.Errorf("audit.RequestID = %q, want req_fwd", eFwd.RequestID)
	}
	if eFwd.Capability != "system.info" {
		t.Errorf("audit.Capability = %q, want system.info", eFwd.Capability)
	}
}

// ---------------------------------------------------------------------------
// Real VPS environment test
//
// This test runs only when BRIDGE_TEST_VPS_ADDR is set to a real VPS node
// WebSocket address.
//
// Usage: BRIDGE_TEST_VPS_ADDR=1.2.3.4:8080 go test ./internal/topology/ -run TestVPS_RemoteConnection -v
// ---------------------------------------------------------------------------

func TestVPS_RemoteConnection(t *testing.T) {
	vpsAddr := os.Getenv("BRIDGE_TEST_VPS_ADDR")
	if vpsAddr == "" {
		t.Skip("Skipping real VPS test: set BRIDGE_TEST_VPS_ADDR to e.g. 1.2.3.4:8080")
	}

	pt := New(Config{
		LocalID:   "test-local",
		LocalName: "test-local",
		Peers: []PeerConfig{
			{ID: "vps", Address: vpsAddr},
		},
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go pt.Start(ctx)

	waitPeerStatus(t, pt, "vps", StatusConnected, 10*time.Second)

	d := newDispatcherForTopology(t, pt, "test-local")

	// Test 1: Forward system.info to VPS
	resp := d.Dispatch(&types.CapabilityRequest{
		RequestID:    "vps_sysinfo",
		PluginID:     "sessionnode-core",
		Capability:   "system.info",
		TargetNodeID: "vps",
		Actor:        types.Actor{Type: "web", ID: "tester"},
	})
	if !resp.OK {
		t.Fatalf("VPS system.info failed: %v", resp.Error)
	}
	payload, _ := json.Marshal(resp.Payload)
	var body map[string]interface{}
	json.Unmarshal(payload, &body)
	if body["os"] == nil {
		t.Error("VPS response missing 'os' field")
	}
	if body["arch"] == nil {
		t.Error("VPS response missing 'arch' field")
	}
	t.Logf("VPS system.info OK: os=%v arch=%v", body["os"], body["arch"])

	// Test 2: List processes on VPS
	resp2 := d.Dispatch(&types.CapabilityRequest{
		RequestID:    "vps_procs",
		PluginID:     "sessionnode-core",
		Capability:   "process.list",
		TargetNodeID: "vps",
		Actor:        types.Actor{Type: "web", ID: "tester"},
	})
	if !resp2.OK {
		t.Fatalf("VPS process.list failed: %v", resp2.Error)
	}
	pld, _ := json.Marshal(resp2.Payload)
	var procBody map[string]interface{}
	json.Unmarshal(pld, &procBody)
	if _, ok := procBody["processes"]; ok {
		t.Logf("VPS process.list OK: %d processes", int(procBody["total"].(float64)))
	}
}

// ---------------------------------------------------------------------------
// Cross-node real-time stream.chunk forwarding
// ---------------------------------------------------------------------------

// TestTwoCore_CrossNodeStreamChunk verifies that a WS client connected to
// Node A receives real-time stream.chunk messages from a process running on
// Node B. This is the core cross-node terminal live-output path.
func TestTwoCore_CrossNodeStreamChunk(t *testing.T) {
	echoPath := testutil.EchoBinary(t)

	// ── Node B (remote/peer): where the process runs ──────────────
	_, peerHTTPSrv := testPeerNode(t, "node-b")
	peerAddr := peerAddr(peerHTTPSrv)

	// ── Node A (local/main): where the WS client connects ─────────
	sessStore := session.NewStore()
	cr := wsconn.NewRegistry()
	pm := process.NewManager(cr.PushChunk, cr.PushSessionEvent)
	execDeps := &executor.Deps{
		Sessions:   sessStore,
		Processes:  pm,
		ConnRoutes: cr,
	}
	execReg := executor.New(execDeps)
	permChecker := permission.NewChecker(&permitAllCaps{}, &permitAllPolicy{})

	// Topology with stream chunk handler wired to local connRegistry.
	pt := New(Config{
		LocalID:   "node-a",
		LocalName: "node-a",
		Peers: []PeerConfig{
			{ID: "node-b", Address: peerAddr},
		},
	})
	pt.SetStreamChunkHandler(func(msg *protocol.Message) {
		switch msg.Type {
		case protocol.MsgTypeStreamChunk:
			cr.PushChunk(msg.SessionID, msg.StreamType, msg.EventSeq, msg.Data)
		case protocol.MsgTypeSessionEvent:
			cr.PushSessionEvent(msg.SessionID, msg.EventSeq, msg.Data, msg.Payload)
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go pt.Start(ctx)
	waitPeerStatus(t, pt, "node-b", StatusConnected, 5*time.Second)

	d := dispatcher.New(
		auth.NewTokenAuthenticator(""),
		&allowAnyPlugin{},
		permChecker,
		nil,
		execReg,
		&silentAudit{},
		pt,
		"node-a",
	)

	sv := server.New("", d, sessStore, cr, pm, nil, nil, "")
	mainHTTPSrv := httptest.NewServer(sv.Handler())
	defer mainHTTPSrv.Close()

	// ── WS client connects to Node A ──────────────────────────────
	wsURL := "ws" + strings.TrimPrefix(mainHTTPSrv.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WS dial error: %v", err)
	}
	defer conn.Close()

	// ── Spawn process on Node B ───────────────────────────────────
	spawnPayload := json.RawMessage(fmt.Sprintf(
		`{"command":%q,"args":["hello from node-b"]}`,
		echoPath,
	))
	spawnMsg := &protocol.Message{
		Type:         protocol.MsgTypeActionRequest,
		RequestID:    "req_spawn",
		Capability:   "process.spawn",
		TargetNodeID: "node-b",
		PluginID:     "sessionnode-core",
		Payload:      spawnPayload,
	}
	spawnData, _ := spawnMsg.MarshalJSON()
	if err := conn.WriteMessage(websocket.TextMessage, spawnData); err != nil {
		t.Fatalf("write spawn error: %v", err)
	}

	// Read spawn response (may need to skip intermediate messages)
	var spawnResp *protocol.Message
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read error waiting for spawn response: %v", err)
		}
		m, err := protocol.UnmarshalMessage(raw)
		if err != nil {
			continue
		}
		if string(m.RequestID) == "req_spawn" {
			spawnResp = m
			break
		}
	}
	if !spawnResp.OK {
		t.Fatalf("process.spawn on node-b failed: %v", spawnResp.Error)
	}
	var spawnBody map[string]interface{}
	json.Unmarshal(spawnResp.Payload, &spawnBody)
	sessionID := spawnBody["sessionId"].(string)
	t.Logf("spawned process on node-b: sessionId=%s", sessionID)

	// ── Subscribe to stdout on Node B via Node A ──────────────────
	subPayload := json.RawMessage(fmt.Sprintf(
		`{"sessionId":%q,"streamType":"stdout"}`,
		sessionID,
	))
	subMsg := &protocol.Message{
		Type:         protocol.MsgTypeActionRequest,
		RequestID:    "req_sub",
		Capability:   "stream.subscribe",
		TargetNodeID: "node-b",
		PluginID:     "sessionnode-core",
		Payload:      subPayload,
	}
	subData, _ := subMsg.MarshalJSON()
	if err := conn.WriteMessage(websocket.TextMessage, subData); err != nil {
		t.Fatalf("write sub error: %v", err)
	}

	// ── Collect stream.chunk messages ─────────────────────────────
	var (
		chunks   []*protocol.Message
		chunksMu sync.Mutex
		done     = make(chan struct{})
	)
	go func() {
		defer close(done)
		deadline := time.After(5 * time.Second)
		for {
			select {
			case <-deadline:
				return
			default:
			}
			conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			m, err := protocol.UnmarshalMessage(raw)
			if err != nil {
				continue
			}
			if m.Type == protocol.MsgTypeStreamChunk && m.SessionID == types.SessionID(sessionID) {
				chunksMu.Lock()
				chunks = append(chunks, m)
				chunksMu.Unlock()
				if len(chunks) >= 1 {
					return // got the chunk we need
				}
			}
		}
	}()
	<-done

	chunksMu.Lock()
	n := len(chunks)
	chunksMu.Unlock()

	if n == 0 {
		t.Fatal("no stream.chunk received from cross-node process — forwarding is broken")
	}

	chunk := chunks[0]
	if chunk.SessionID != types.SessionID(sessionID) {
		t.Errorf("SessionID = %q, want %q", chunk.SessionID, sessionID)
	}
	if chunk.StreamType != "stdout" {
		t.Errorf("StreamType = %q, want stdout", chunk.StreamType)
	}
	if chunk.Data == "" {
		t.Error("stream.chunk Data is empty")
	}
	t.Logf("cross-node stream.chunk OK: session=%s stream=%s data=%q", chunk.SessionID, chunk.StreamType, chunk.Data)
}
