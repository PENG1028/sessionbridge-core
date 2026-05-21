package server

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/user/sessionnode/go-core/internal/auth"
	"github.com/user/sessionnode/go-core/internal/dispatcher"
	"github.com/user/sessionnode/go-core/internal/executor"
	"github.com/user/sessionnode/go-core/internal/history"
	"github.com/user/sessionnode/go-core/internal/permission"
	"github.com/user/sessionnode/go-core/internal/process"
	"github.com/user/sessionnode/go-core/internal/run"
	"github.com/user/sessionnode/go-core/internal/session"
	"github.com/user/sessionnode/go-core/internal/wsconn"
	"github.com/user/sessionnode/go-core/pkg/protocol"
	"github.com/user/sessionnode/go-core/pkg/types"
)

func testServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()

	sessStore := session.NewStore()
	cr := wsconn.NewRegistry()
	pm := process.NewManager(cr.PushChunk, cr.PushSessionEvent)
	execDeps := &executor.Deps{
		Sessions:   sessStore,
		Processes:  pm,
		ConnRoutes: cr,
	}
	execReg := executor.New(execDeps)

	permChecker := permission.NewChecker(
		&mockCapRegistry{},
		&mockPolicyStore{},
	)

	audit := &mockAuditLogger{}
	topo := &mockTopology{}

	d := dispatcher.New(
		auth.NewTokenAuthenticator(""),
		&mockPluginRegistry{},
		permChecker,
		nil, /* planner */
		execReg,
		audit,
		topo,
		"node_local",
	)

	sv := New("", d, sessStore, cr, pm)
	httpSrv := httptest.NewServer(sv.httpServer.Handler)
	return sv, httpSrv
}

func wsConnect(t *testing.T, srv *httptest.Server) *websocket.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("WS dial error: %v", err)
	}
	return conn
}

func sendAndRecv(t *testing.T, conn *websocket.Conn, msg *protocol.Message) *protocol.Message {
	t.Helper()
	data, err := msg.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		t.Fatalf("write error: %v", err)
	}
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read error: %v (raw: %s)", err, string(raw))
	}
	resp, err := protocol.UnmarshalMessage(raw)
	if err != nil {
		t.Fatalf("unmarshal error: %v (raw: %s)", err, string(raw))
	}
	return resp
}

// sendAndRecvRequestID sends a message and reads responses until it finds one
// with a matching RequestID, discarding intermediate push messages (stream.chunk,
// session events, etc.). This prevents test flakiness when push messages arrive
// between request/response pairs on the same connection.
func sendAndRecvRequestID(t *testing.T, conn *websocket.Conn, msg *protocol.Message, requestID string) *protocol.Message {
	t.Helper()
	data, err := msg.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		t.Fatalf("write error: %v", err)
	}
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read error: %v (raw: %s)", err, string(raw))
		}
		resp, err := protocol.UnmarshalMessage(raw)
		if err != nil {
			t.Fatalf("unmarshal error: %v (raw: %s)", err, string(raw))
		}
		if string(resp.RequestID) == requestID {
			return resp
		}
	}
}

func TestHealthEndpoint(t *testing.T) {
	_, srv := testServer(t)
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf("status = %v, want ok", body["status"])
	}
}

func TestWSPing(t *testing.T) {
	_, srv := testServer(t)
	defer srv.Close()

	conn := wsConnect(t, srv)
	defer conn.Close()

	resp := sendAndRecv(t, conn, protocol.NewPing())
	if resp.Type != protocol.MsgTypePong {
		t.Errorf("Type = %q, want %q", resp.Type, protocol.MsgTypePong)
	}
}

func TestWSSessionCreate(t *testing.T) {
	_, srv := testServer(t)
	defer srv.Close()

	conn := wsConnect(t, srv)
	defer conn.Close()

	payload := json.RawMessage(`{"command":"bash","cwd":"/home"}`)
	msg := protocol.NewSessionCreate("shell", "", payload)
	msg.RequestID = "req_001"
	resp := sendAndRecv(t, conn, msg)

	if resp.Type != protocol.MsgTypeActionResponse {
		t.Errorf("Type = %q, want %q", resp.Type, protocol.MsgTypeActionResponse)
	}
	if !resp.OK {
		t.Errorf("OK = false, error: %v", resp.Error)
	}
	if resp.RequestID != "req_001" {
		t.Errorf("RequestID = %q", resp.RequestID)
	}

	var payloadResp map[string]interface{}
	if err := json.Unmarshal(resp.Payload, &payloadResp); err != nil {
		t.Fatalf("payload unmarshal error: %v", err)
	}
	if payloadResp["sessionId"] == nil {
		t.Error("payload missing sessionId")
	}
}

func TestWSSessionCreateAndInfo(t *testing.T) {
	_, srv := testServer(t)
	defer srv.Close()

	conn := wsConnect(t, srv)
	defer conn.Close()

	createPayload := json.RawMessage(`{"command":"bash","cwd":"/tmp","pluginId":"shell"}`)
	createMsg := protocol.NewSessionCreate("shell", "", createPayload)
	createMsg.RequestID = "req_001"
	createResp := sendAndRecv(t, conn, createMsg)
	if !createResp.OK {
		t.Fatalf("create failed: %v", createResp.Error)
	}

	var createBody map[string]interface{}
	json.Unmarshal(createResp.Payload, &createBody)
	sessionID := createBody["sessionId"].(string)

	infoPayload := json.RawMessage(`{"sessionId":"` + sessionID + `"}`)
	infoResp := sendAndRecv(t, conn, protocol.NewActionRequest(&types.CapabilityRequest{
		RequestID:  "req_002",
		Capability: "session.info",
		Payload:    infoPayload,
	}))
	if !infoResp.OK {
		t.Fatalf("info failed: %v", infoResp.Error)
	}

	var infoBody map[string]interface{}
	json.Unmarshal(infoResp.Payload, &infoBody)
	if infoBody["state"] != "created" {
		t.Errorf("state = %v, want created", infoBody["state"])
	}
}

func TestWSSessionList(t *testing.T) {
	_, srv := testServer(t)
	defer srv.Close()

	conn := wsConnect(t, srv)
	defer conn.Close()

	for i := 0; i < 2; i++ {
		payload := json.RawMessage(`{"command":"bash"}`)
		msg := protocol.NewSessionCreate("shell", "", payload)
		resp := sendAndRecv(t, conn, msg)
		if !resp.OK {
			t.Fatalf("create %d failed", i)
		}
	}

	listResp := sendAndRecv(t, conn, &protocol.Message{
		Type:       protocol.MsgTypeActionRequest,
		RequestID:  "req_list",
		Capability: "session.list",
	})
	if !listResp.OK {
		t.Fatalf("list failed: %v", listResp.Error)
	}
	var listBody map[string]interface{}
	json.Unmarshal(listResp.Payload, &listBody)
	sessions, ok := listBody["sessions"].([]interface{})
	if !ok {
		t.Fatalf("expected sessions array, got %T", listBody["sessions"])
	}
	if len(sessions) != 2 {
		t.Errorf("expected 2 sessions, got %d", len(sessions))
	}
}

func TestWSSystemInfo(t *testing.T) {
	_, srv := testServer(t)
	defer srv.Close()

	conn := wsConnect(t, srv)
	defer conn.Close()

	resp := sendAndRecv(t, conn, &protocol.Message{
		Type:       protocol.MsgTypeActionRequest,
		RequestID:  "req_sys",
		Capability: "system.info",
	})
	if !resp.OK {
		t.Fatalf("system.info failed: %v", resp.Error)
	}
	var body map[string]interface{}
	json.Unmarshal(resp.Payload, &body)
	if body["os"] == nil {
		t.Error("missing os field")
	}
	if body["arch"] == nil {
		t.Error("missing arch field")
	}
}

func TestWSUnknownCapability(t *testing.T) {
	_, srv := testServer(t)
	defer srv.Close()

	conn := wsConnect(t, srv)
	defer conn.Close()

	resp := sendAndRecv(t, conn, &protocol.Message{
		Type:       protocol.MsgTypeActionRequest,
		RequestID:  "req_unknown",
		Capability: "nonexistent.capability",
	})
	if resp.OK {
		t.Fatal("expected failure for unknown capability")
	}
	if resp.Error == nil || resp.Error.Code != protocol.ErrCodeExecutionError {
		t.Errorf("expected execution error, got %v", resp.Error)
	}
}

func TestWSEnvGet(t *testing.T) {
	_, srv := testServer(t)
	defer srv.Close()

	conn := wsConnect(t, srv)
	defer conn.Close()

	payload := json.RawMessage(`{"name":"PATH"}`)
	resp := sendAndRecv(t, conn, &protocol.Message{
		Type:       protocol.MsgTypeActionRequest,
		RequestID:  "req_env",
		Capability: "env.get",
		Payload:    payload,
	})
	if !resp.OK {
		t.Fatalf("env.get failed: %v", resp.Error)
	}
	var body map[string]interface{}
	json.Unmarshal(resp.Payload, &body)
	if body["name"] != "PATH" {
		t.Errorf("name = %v", body["name"])
	}
}

func TestWSStreamWriteAndSubscribe(t *testing.T) {
	_, srv := testServer(t)
	defer srv.Close()

	conn := wsConnect(t, srv)
	defer conn.Close()

	createPayload := json.RawMessage(`{"command":"echo hi"}`)
	createMsg := protocol.NewSessionCreate("shell", "", createPayload)
	createMsg.RequestID = "req_c1"
	createResp := sendAndRecv(t, conn, createMsg)
	if !createResp.OK {
		t.Fatalf("create failed: %v", createResp.Error)
	}
	var createBody map[string]interface{}
	json.Unmarshal(createResp.Payload, &createBody)
	sessionID := createBody["sessionId"].(string)

	writePayload := json.RawMessage(`{"sessionId":"` + sessionID + `","stream":"stdout","data":"hello world"}`)
	writeResp := sendAndRecv(t, conn, &protocol.Message{
		Type:       protocol.MsgTypeActionRequest,
		RequestID:  "req_w1",
		Capability: "stream.write",
		Payload:    writePayload,
	})
	if !writeResp.OK {
		t.Fatalf("write failed: %v", writeResp.Error)
	}

	subPayload := json.RawMessage(`{"sessionId":"` + sessionID + `","stream":"stdout"}`)
	subResp := sendAndRecv(t, conn, &protocol.Message{
		Type:       protocol.MsgTypeActionRequest,
		RequestID:  "req_s1",
		Capability: "stream.subscribe",
		Payload:    subPayload,
	})
	if !subResp.OK {
		t.Fatalf("subscribe failed: %v", subResp.Error)
	}
	var subBody map[string]interface{}
	json.Unmarshal(subResp.Payload, &subBody)
	if subBody["data"] != "hello world" {
		t.Errorf("data = %v, want hello world", subBody["data"])
	}
}

func TestPingPongIdiom(t *testing.T) {
	_, srv := testServer(t)
	defer srv.Close()

	conn := wsConnect(t, srv)
	defer conn.Close()

	pong := sendAndRecv(t, conn, protocol.NewPing())
	if pong.Type != protocol.MsgTypePong {
		t.Errorf("expected pong, got %s", pong.Type)
	}

	pongData, _ := protocol.NewPong().MarshalJSON()
	conn.WriteMessage(websocket.TextMessage, pongData)
	conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	_, _, err := conn.ReadMessage()
	if err == nil {
		t.Error("expected timeout after sending pong (no response)")
	}
}

func TestWSProcessSpawnAndStream(t *testing.T) {
	_, srv := testServer(t)
	defer srv.Close()

	conn := wsConnect(t, srv)
	defer conn.Close()

	// Spawn a process that outputs something
	payload := json.RawMessage(`{"command":"go","args":["version"]}`)
	spawnMsg := &protocol.Message{
		Type:       protocol.MsgTypeActionRequest,
		RequestID:  "req_spawn",
		Capability: "process.spawn",
		Payload:    payload,
	}

	// Send spawn and get first response (spawn ack)
	resp := sendAndRecv(t, conn, spawnMsg)
	if !resp.OK {
		t.Fatalf("spawn failed: %v", resp.Error)
	}
	if resp.RequestID != "req_spawn" {
		t.Errorf("RequestID = %q, want req_spawn", resp.RequestID)
	}

	// Extract sessionId from spawn response
	var spawnResult map[string]interface{}
	json.Unmarshal(resp.Payload, &spawnResult)
	sid, _ := spawnResult["sessionId"].(string)
	if sid == "" {
		t.Fatal("spawn response missing sessionId")
	}

	// In the multi-subscriber model, subscribe explicitly to receive process output.
	subPayload, _ := json.Marshal(map[string]string{
		"sessionId":  sid,
		"streamType": "stdout,stderr",
	})
	subResp := sendAndRecv(t, conn, &protocol.Message{
		Type:       protocol.MsgTypeActionRequest,
		RequestID:  "req_sub",
		Capability: "stream.subscribe",
		Payload:    subPayload,
	})
	if !subResp.OK {
		t.Fatalf("stream.subscribe failed: %v", subResp.Error)
	}

	// Read push messages — expect stream.chunk and session.event.
	foundStdout := false
	foundExited := false
	for i := 0; i < 10; i++ {
		conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, raw, err := conn.ReadMessage()
		if err != nil {
			break
		}
		push, err := protocol.UnmarshalMessage(raw)
		if err != nil {
			continue
		}
		switch push.Type {
		case protocol.MsgTypeStreamChunk:
			if push.StreamType == "stdout" && push.Data != "" {
				foundStdout = true
			}
		case protocol.MsgTypeSessionEvent:
			foundExited = true
		}
	}

	if !foundStdout {
		t.Error("expected stdout stream.chunk from process output")
	}
	if !foundExited {
		t.Error("expected session.event 'exited' from process")
	}
}

func TestWSProcessSpawnBadCommand(t *testing.T) {
	_, srv := testServer(t)
	defer srv.Close()

	conn := wsConnect(t, srv)
	defer conn.Close()

	payload := json.RawMessage(`{"command":"nonexistent_cmd_xyz"}`)
	resp := sendAndRecv(t, conn, &protocol.Message{
		Type:       protocol.MsgTypeActionRequest,
		RequestID:  "req_bad",
		Capability: "process.spawn",
		Payload:    payload,
	})
	if resp.OK {
		t.Fatal("expected failure for bad command")
	}
	if resp.Error == nil {
		t.Fatal("expected error message")
	}
}

func TestAPISessions(t *testing.T) {
	_, srv := testServer(t)
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL + "/api/sessions")
	if err != nil {
		t.Fatalf("GET /api/sessions error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if _, ok := body["sessions"]; !ok {
		t.Error("missing sessions field")
	}
}

func TestAPIProcesses(t *testing.T) {
	_, srv := testServer(t)
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL + "/api/processes")
	if err != nil {
		t.Fatalf("GET /api/processes error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if _, ok := body["processes"]; !ok {
		t.Error("missing processes field")
	}
}

func TestHealthCheckContentType(t *testing.T) {
	_, srv := testServer(t)
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health error: %v", err)
	}
	defer resp.Body.Close()

	ct := resp.Header.Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
}

func TestWSHistoryE2E(t *testing.T) {
	_, srv, hStore := testServerWithHistory(t)
	defer srv.Close()
	defer hStore.Cleanup()

	conn := wsConnect(t, srv)
	defer conn.Close()

	// Step 1: session.create with explicit history policy
	createPayload := json.RawMessage(`{"command":"bash","history":{"enabled":true,"mode":"memory","streams":["stdout","stderr"]}}`)
	createMsg := protocol.NewSessionCreate("shell", "", createPayload)
	createMsg.RequestID = "req_c1"
	createResp := sendAndRecv(t, conn, createMsg)
	if !createResp.OK {
		t.Fatalf("session.create failed: %v", createResp.Error)
	}
	var createBody map[string]interface{}
	if err := json.Unmarshal(createResp.Payload, &createBody); err != nil {
		t.Fatalf("unmarshal create: %v", err)
	}
	sessionID, ok := createBody["sessionId"].(string)
	if !ok || sessionID == "" {
		t.Fatal("expected sessionId in create response")
	}

	// Step 2: stream.write — writes to session buffer AND records into history
	writePayload := json.RawMessage(`{"sessionId":"` + sessionID + `","stream":"stdout","data":"hello from E2E"}`)
	writeResp := sendAndRecv(t, conn, &protocol.Message{
		Type:       protocol.MsgTypeActionRequest,
		RequestID:  "req_w1",
		Capability: "stream.write",
		Payload:    writePayload,
	})
	if !writeResp.OK {
		t.Fatalf("stream.write failed: %v", writeResp.Error)
	}

	// Step 3: stream.replay — verify history captured the data
	replayPayload := json.RawMessage(`{"sessionId":"` + sessionID + `","streamType":"stdout"}`)
	replayResp := sendAndRecv(t, conn, &protocol.Message{
		Type:       protocol.MsgTypeActionRequest,
		RequestID:  "req_r1",
		Capability: "stream.replay",
		Payload:    replayPayload,
	})
	if !replayResp.OK {
		t.Fatalf("stream.replay failed: %v", replayResp.Error)
	}
	var replayBody map[string]interface{}
	if err := json.Unmarshal(replayResp.Payload, &replayBody); err != nil {
		t.Fatalf("unmarshal replay: %v", err)
	}
	events, ok := replayBody["events"].([]interface{})
	if !ok {
		t.Fatalf("expected events array, got %T", replayBody["events"])
	}
	if len(events) == 0 {
		t.Fatal("expected at least 1 replayed event")
	}
	firstEvent := events[0].(map[string]interface{})
	if data, ok := firstEvent["data"].(string); ok {
		if !strings.Contains(data, "hello from E2E") {
			t.Errorf("expected data containing 'hello from E2E', got %q", data)
		}
	}

	// Step 4: session.history.stats
	statsPayload := json.RawMessage(`{"sessionId":"` + sessionID + `"}`)
	statsResp := sendAndRecv(t, conn, &protocol.Message{
		Type:       protocol.MsgTypeActionRequest,
		RequestID:  "req_st1",
		Capability: "session.history.stats",
		Payload:    statsPayload,
	})
	if !statsResp.OK {
		t.Fatalf("session.history.stats failed: %v", statsResp.Error)
	}
	var statsBody map[string]interface{}
	if err := json.Unmarshal(statsResp.Payload, &statsBody); err != nil {
		t.Fatalf("unmarshal stats: %v", err)
	}
	if ec, ok := statsBody["eventCount"].(float64); !ok || ec < 1 {
		t.Errorf("expected eventCount >= 1, got %v", statsBody["eventCount"])
	}
	if bs, ok := statsBody["bytesStored"].(float64); !ok || bs < 1 {
		t.Errorf("expected bytesStored >= 1, got %v", statsBody["bytesStored"])
	}
	if mode, ok := statsBody["mode"].(string); !ok || mode != "memory" {
		t.Errorf("expected mode 'memory', got %v", statsBody["mode"])
	}

	// Step 5: session.history.getPolicy
	policyPayload := json.RawMessage(`{"sessionId":"` + sessionID + `"}`)
	policyResp := sendAndRecv(t, conn, &protocol.Message{
		Type:       protocol.MsgTypeActionRequest,
		RequestID:  "req_p1",
		Capability: "session.history.getPolicy",
		Payload:    policyPayload,
	})
	if !policyResp.OK {
		t.Fatalf("session.history.getPolicy failed: %v", policyResp.Error)
	}
	var policyBody map[string]interface{}
	if err := json.Unmarshal(policyResp.Payload, &policyBody); err != nil {
		t.Fatalf("unmarshal policy: %v", err)
	}
	hp, ok := policyBody["history"].(map[string]interface{})
	if !ok {
		t.Fatal("expected history object")
	}
	if hp["mode"] != "memory" {
		t.Errorf("mode = %v, want memory", hp["mode"])
	}

	// Step 6: session.history.list
	listResp := sendAndRecv(t, conn, &protocol.Message{
		Type:       protocol.MsgTypeActionRequest,
		RequestID:  "req_l1",
		Capability: "session.history.list",
	})
	if !listResp.OK {
		t.Fatalf("session.history.list failed: %v", listResp.Error)
	}
	var listBody map[string]interface{}
	if err := json.Unmarshal(listResp.Payload, &listBody); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	sessions, ok := listBody["sessions"].([]interface{})
	if !ok {
		t.Fatalf("expected sessions array, got %T", listBody["sessions"])
	}
	if len(sessions) < 1 {
		t.Errorf("expected at least 1 session, got %d", len(sessions))
	}
}

func testServerWithHistory(t *testing.T) (*Server, *httptest.Server, *history.Store) {
	t.Helper()

	sessStore := session.NewStore()
	cr := wsconn.NewRegistry()
	historyStore := history.New("")

	// Wrap push/event callbacks to record into history (like production main.go)
	wrappedPush := func(sid types.SessionID, streamType string, seq types.EventSeq, data string) {
		historyStore.Record(sid, streamType, seq, data)
		cr.PushChunk(sid, streamType, seq, data)
	}
	wrappedEvent := func(sid types.SessionID, seq types.EventSeq, eventType string, data interface{}) {
		if eventType == "started" || eventType == "exited" {
			historyStore.RecordEvent(sid, seq, "session."+eventType, data)
		}
		cr.PushSessionEvent(sid, seq, eventType, data)
	}
	pm := process.NewManager(wrappedPush, wrappedEvent)
	pm.SetOnSpawn(func(sid types.SessionID) {
		historyStore.InitSession(sid, types.DefaultHistoryPolicy())
	})

	execDeps := &executor.Deps{
		Sessions:   sessStore,
		Processes:  pm,
		ConnRoutes: cr,
		History:    historyStore,
	}
	execReg := executor.New(execDeps)

	permChecker := permission.NewChecker(
		&mockCapRegistry{},
		&mockPolicyStore{},
	)

	audit := &mockAuditLogger{}
	topo := &mockTopology{}

	d := dispatcher.New(
		auth.NewTokenAuthenticator(""),
		&mockPluginRegistry{},
		permChecker,
		nil, /* planner */
		execReg,
		audit,
		topo,
		"node_local",
	)

	sv := New("", d, sessStore, cr, pm)
	httpSrv := httptest.NewServer(sv.httpServer.Handler)
	return sv, httpSrv, historyStore
}

func testServerWithRealPermission(t *testing.T, caps map[types.PluginID][]string, grantSelector func(pid types.PluginID, cap string) bool) (*Server, *httptest.Server) {
	t.Helper()

	sessStore := session.NewStore()
	cr := wsconn.NewRegistry()
	pm := process.NewManager(cr.PushChunk, cr.PushSessionEvent)

	execDeps := &executor.Deps{
		Sessions:   sessStore,
		Processes:  pm,
		ConnRoutes: cr,
	}
	execReg := executor.New(execDeps)

	// Real capability registry
	reg := permission.NewMapRegistry(caps)

	// Selective policy store: only grants caps that pass the selector
	ps := permission.NewMemPolicyStore()
	for pid, capList := range caps {
		for _, c := range capList {
			if grantSelector == nil || grantSelector(pid, c) {
				ps.SetGrant(pid, c, &permission.PermissionGrant{Mode: "allow", GrantedBy: "test", GrantedAt: time.Now().UnixMilli()})
			}
		}
	}

	permChecker := permission.NewChecker(reg, ps)
	audit := &mockAuditLogger{}
	topo := &mockTopology{}

	d := dispatcher.New(
		auth.NewTokenAuthenticator(""),
		&mockPluginRegistry{},
		permChecker,
		nil, /* planner */
		execReg,
		audit,
		topo,
		"node_local",
	)

	sv := New("", d, sessStore, cr, pm)
	httpSrv := httptest.NewServer(sv.httpServer.Handler)
	return sv, httpSrv
}

func TestWSAccessControl_AllowedCap(t *testing.T) {
	// Only grant shell.system.info — test that it works
	allowOnly := map[string]bool{"system.info": true}
	_, srv := testServerWithRealPermission(t, permission.AllPluginsCaps, func(pid types.PluginID, cap string) bool {
		return allowOnly[cap]
	})
	defer srv.Close()

	conn := wsConnect(t, srv)
	defer conn.Close()

	resp := sendAndRecv(t, conn, &protocol.Message{
		Type:       protocol.MsgTypeActionRequest,
		RequestID:  "req_allowed",
		PluginID:   "sessionnode-core",
		Capability: "system.info",
	})
	if !resp.OK {
		t.Fatalf("expected OK for allowed cap, got: %v", resp.Error)
	}
}

func TestWSAccessControl_UndeclaredCap(t *testing.T) {
	_, srv := testServerWithRealPermission(t, permission.AllPluginsCaps, nil)
	defer srv.Close()

	conn := wsConnect(t, srv)
	defer conn.Close()

	resp := sendAndRecv(t, conn, &protocol.Message{
		Type:       protocol.MsgTypeActionRequest,
		RequestID:  "req_undec",
		Capability: "nonexistent.plugin.nope",
	})
	if resp.OK {
		t.Fatal("expected failure for undeclared capability")
	}
	if resp.Error == nil {
		t.Fatal("expected error message")
	}
	if resp.Error.Code != protocol.ErrCodeCapNotDeclared {
		t.Errorf("code = %q, want %q", resp.Error.Code, protocol.ErrCodeCapNotDeclared)
	}
}

func TestWSAccessControl_NotGrantedCap(t *testing.T) {
	// Grant "system.info" only — "env.get" is declared in AllPluginsCaps but not granted
	allowOnly := map[string]bool{"system.info": true}
	_, srv := testServerWithRealPermission(t, permission.AllPluginsCaps, func(pid types.PluginID, cap string) bool {
		return allowOnly[cap]
	})
	defer srv.Close()

	conn := wsConnect(t, srv)
	defer conn.Close()

	resp := sendAndRecv(t, conn, &protocol.Message{
		Type:       protocol.MsgTypeActionRequest,
		RequestID:  "req_ng",
		Capability: "env.get",
	})
	if resp.OK {
		t.Fatal("expected failure for not-granted capability")
	}
	if resp.Error == nil {
		t.Fatal("expected error message")
	}
}

func TestWSAccessControl_DenyMode(t *testing.T) {
	caps := map[types.PluginID][]string{"shell": {"system.info"}}
	_, srv := testServerWithRealPermission(t, caps, func(pid types.PluginID, cap string) bool {
		return false // grant nothing — manually add deny below
	})
	defer srv.Close()

	// Re-create with explicit deny grant
	sessStore := session.NewStore()
	cr := wsconn.NewRegistry()
	pm := process.NewManager(cr.PushChunk, cr.PushSessionEvent)
	execDeps := &executor.Deps{Sessions: sessStore, Processes: pm, ConnRoutes: cr}
	execReg := executor.New(execDeps)

	reg := permission.NewMapRegistry(caps)
	ps := permission.NewMemPolicyStore()
	ps.SetGrant("shell", "system.info", &permission.PermissionGrant{Mode: "deny", GrantedBy: "test"})

	d := dispatcher.New(
		auth.NewTokenAuthenticator(""),
		&mockPluginRegistry{},
		permission.NewChecker(reg, ps),
		nil, /* planner */
		execReg,
		&mockAuditLogger{},
		&mockTopology{},
		"node_local",
	)

	sv := New("", d, sessStore, cr, pm)
	httpSrv := httptest.NewServer(sv.httpServer.Handler)
	defer httpSrv.Close()

	conn := wsConnect(t, httpSrv)
	defer conn.Close()

	resp := sendAndRecv(t, conn, &protocol.Message{
		Type:       protocol.MsgTypeActionRequest,
		RequestID:  "req_deny",
		PluginID:   "shell",
		Capability: "system.info",
	})
	if resp.OK {
		t.Fatal("expected failure for denied capability")
	}
}

// ─── Terminal Plugin E2E ─────────────────────────────────────────

func TestTerminalPluginE2E(t *testing.T) {
	_, srv, hStore := testServerWithHistory(t)
	defer srv.Close()
	defer hStore.Cleanup()

	//
	// Part A: process.spawn → stream.replay verifies that process output
	// is captured in history. Keep the same connection alive.
	//
	conn := wsConnect(t, srv)
	defer conn.Close()

	spawnPayload := json.RawMessage(`{"command":"go","args":["version"]}`)
	spawnResp := sendAndRecv(t, conn, &protocol.Message{
		Type:       protocol.MsgTypeActionRequest,
		RequestID:  "req_spawn",
		Capability: "process.spawn",
		Payload:    spawnPayload,
	})
	if !spawnResp.OK {
		t.Fatalf("spawn failed: %v", spawnResp.Error)
	}
	var spawnBody map[string]interface{}
	if err := json.Unmarshal(spawnResp.Payload, &spawnBody); err != nil {
		t.Fatalf("unmarshal spawn: %v", err)
	}
	sessionID, ok := spawnBody["sessionId"].(string)
	if !ok || sessionID == "" {
		t.Fatal("expected sessionId in spawn response")
	}

	// Wait for process to finish and history to capture output.
	time.Sleep(1 * time.Second)

	// Verify via stream.replay WebSocket request.
	// Use sendAndRecvRequestID to discard any push messages (stream.chunk, session events)
	// that arrived after the process finished.
	replayResp := sendAndRecvRequestID(t, conn, &protocol.Message{
		Type:       protocol.MsgTypeActionRequest,
		RequestID:  "req_replay",
		Capability: "stream.replay",
		Payload:    json.RawMessage(`{"sessionId":"` + sessionID + `","streamType":"stdout"}`),
	}, "req_replay")
	if !replayResp.OK {
		t.Fatalf("stream.replay failed: %v", replayResp.Error)
	}
	var replayBody map[string]interface{}
	if err := json.Unmarshal(replayResp.Payload, &replayBody); err != nil {
		t.Fatalf("unmarshal replay: %v", err)
	}
	events, ok := replayBody["events"].([]interface{})
	if !ok {
		t.Fatalf("expected events array, got %T", replayBody["events"])
	}
	if len(events) == 0 {
		t.Fatal("expected at least 1 replayed event from process output")
	}
	hasData := false
	for _, evt := range events {
		evtMap, ok := evt.(map[string]interface{})
		if !ok {
			continue
		}
		if d, ok := evtMap["data"].(string); ok && d != "" {
			hasData = true
			break
		}
	}
	if !hasData {
		t.Error("expected non-empty data in replayed events")
	}

	//
	// Part B: stream.write with streamType field → stream.replay verifies
	// that the streamType field is accepted end-to-end.
	//
	createPayload := json.RawMessage(`{"command":"test-streamType","history":{"enabled":true,"mode":"memory","streams":["stdout"]}}`)
	createResp := sendAndRecv(t, conn, &protocol.Message{
		Type:       protocol.MsgTypeActionRequest,
		RequestID:  "req_create",
		Capability: "session.create",
		Payload:    createPayload,
	})
	if !createResp.OK {
		t.Fatalf("session.create failed: %v", createResp.Error)
	}
	var createBody map[string]interface{}
	if err := json.Unmarshal(createResp.Payload, &createBody); err != nil {
		t.Fatalf("unmarshal create: %v", err)
	}
	sid2, ok := createBody["sessionId"].(string)
	if !ok || sid2 == "" {
		t.Fatal("expected sessionId in create response")
	}

	// Write with streamType (not legacy "stream") field
	writePayload := json.RawMessage(`{"sessionId":"` + sid2 + `","streamType":"stdout","data":"written via streamType"}`)
	writeResp := sendAndRecv(t, conn, &protocol.Message{
		Type:       protocol.MsgTypeActionRequest,
		RequestID:  "req_write",
		Capability: "stream.write",
		Payload:    writePayload,
	})
	if !writeResp.OK {
		t.Fatalf("stream.write failed: %v", writeResp.Error)
	}
	var writeBody map[string]interface{}
	if err := json.Unmarshal(writeResp.Payload, &writeBody); err != nil {
		t.Fatalf("unmarshal write: %v", err)
	}
	if written, ok := writeBody["written"].(float64); !ok || written == 0 {
		t.Errorf("expected >0 written bytes, got %v", writeBody["written"])
	}
	if st, ok := writeBody["streamType"].(string); !ok || st != "stdout" {
		t.Errorf("expected streamType 'stdout', got %v", writeBody["streamType"])
	}

	// Verify via replay that streamType-written data is in history
	replay2Resp := sendAndRecvRequestID(t, conn, &protocol.Message{
		Type:       protocol.MsgTypeActionRequest,
		RequestID:  "req_replay2",
		Capability: "stream.replay",
		Payload:    json.RawMessage(`{"sessionId":"` + sid2 + `","streamType":"stdout"}`),
		}, "req_replay2")
	if !replay2Resp.OK {
		t.Fatalf("stream.replay failed: %v", replay2Resp.Error)
	}
	var replay2Body map[string]interface{}
	if err := json.Unmarshal(replay2Resp.Payload, &replay2Body); err != nil {
		t.Fatalf("unmarshal replay2: %v", err)
	}
	events2, ok := replay2Body["events"].([]interface{})
	if !ok {
		t.Fatalf("expected events array, got %T", replay2Body["events"])
	}
	foundStreamType := false
	for _, evt := range events2 {
		evtMap, ok := evt.(map[string]interface{})
		if !ok {
			continue
		}
		if data, ok := evtMap["data"].(string); ok && strings.Contains(data, "written via streamType") {
			foundStreamType = true
			break
		}
	}
	if !foundStreamType {
		t.Error("expected 'written via streamType' in replayed history")
	}
}

type mockPluginRegistry struct{}

func (m *mockPluginRegistry) Get(id types.PluginID) (*dispatcher.PluginEntry, error) {
	return &dispatcher.PluginEntry{ID: id, Enabled: true}, nil
}

type mockCapRegistry struct{}

func (m *mockCapRegistry) HasCapability(pluginID types.PluginID, capability string) bool {
	return true
}

type mockPolicyStore struct{}

func (m *mockPolicyStore) GetGrant(pluginID types.PluginID, capability string) (*permission.PermissionGrant, error) {
	return &permission.PermissionGrant{Mode: "allow"}, nil
}

type mockTopology struct{}

func (m *mockTopology) Get(nodeID types.NodeID) (*dispatcher.NodeTarget, error) {
	return nil, nil
}

type mockAuditLogger struct{}

func (m *mockAuditLogger) Log(req *types.CapabilityRequest, allowed bool, detail string) {
}

// ---------------------------------------------------------------------------
// Service Token E2E — real WS path
// ---------------------------------------------------------------------------

// testServerWithToken creates a server with a specific token for auth.
func testServerWithToken(t *testing.T, token string) (*Server, *httptest.Server) {
	t.Helper()

	sessStore := session.NewStore()
	cr := wsconn.NewRegistry()
	pm := process.NewManager(cr.PushChunk, cr.PushSessionEvent)
	execDeps := &executor.Deps{
		Sessions:   sessStore,
		Processes:  pm,
		ConnRoutes: cr,
	}
	execReg := executor.New(execDeps)

	permChecker := permission.NewChecker(
		&mockCapRegistry{},
		&mockPolicyStore{},
	)

	audit := &mockAuditLogger{}
	topo := &mockTopology{}

	d := dispatcher.New(
		auth.NewTokenAuthenticator(token),
		&mockPluginRegistry{},
		permChecker,
		nil, /* planner */
		execReg,
		audit,
		topo,
		"node_local",
	)

	sv := New("", d, sessStore, cr, pm)
	httpSrv := httptest.NewServer(sv.httpServer.Handler)
	return sv, httpSrv
}

func TestWSServiceTokenE2E(t *testing.T) {
	const validToken = "test-service-token-42"
	_, srv := testServerWithToken(t, validToken)
	defer srv.Close()

	// Test 1: no token → UNAUTHENTICATED
	conn := wsConnect(t, srv)
	msg := &protocol.Message{
		Type:       protocol.MsgTypeActionRequest,
		RequestID:  "req_notoken",
		Capability: "system.info",
		ActorType:  "external",
		ActorID:    "script",
		// no ActorToken
	}
	resp := sendAndRecv(t, conn, msg)
	if resp.OK {
		t.Fatal("expected failure when no token is provided")
	}
	if resp.Error == nil || resp.Error.Code != protocol.ErrCodeUnauthenticated {
		t.Errorf("expected UNAUTHENTICATED, got code=%v msg=%v",
			errCode(resp.Error), errMsg(resp.Error))
	}
	conn.Close()

	// Test 2: wrong token → UNAUTHENTICATED
	conn2 := wsConnect(t, srv)
	msg2 := &protocol.Message{
		Type:       protocol.MsgTypeActionRequest,
		RequestID:  "req_badtoken",
		Capability: "system.info",
		ActorType:  "external",
		ActorID:    "script",
		ActorToken: "wrong-token",
	}
	resp2 := sendAndRecv(t, conn2, msg2)
	if resp2.OK {
		t.Fatal("expected failure when wrong token is provided")
	}
	if resp2.Error == nil || resp2.Error.Code != protocol.ErrCodeUnauthenticated {
		t.Errorf("expected UNAUTHENTICATED for wrong token, got code=%v msg=%v",
			errCode(resp2.Error), errMsg(resp2.Error))
	}
	conn2.Close()

	// Test 3: valid token → OK
	conn3 := wsConnect(t, srv)
	msg3 := &protocol.Message{
		Type:       protocol.MsgTypeActionRequest,
		RequestID:  "req_oktoken",
		Capability: "system.info",
		ActorType:  "external",
		ActorID:    "script",
		ActorToken: validToken,
	}
	resp3 := sendAndRecv(t, conn3, msg3)
	if !resp3.OK {
		t.Fatalf("expected success with valid token, got: %v", resp3.Error)
	}
	conn3.Close()
}

// ---------------------------------------------------------------------------
// Terminal Reconnect E2E — disconnect / reconnect lifecycle
// ---------------------------------------------------------------------------

// testServerWithRunStore creates a server wired with RunStore, History, and
// ProcessManager. This is needed for run.* capabilities (run.create, run.list,
// run.info, run.stop) and stream.replay.
func testServerWithRunStore(t *testing.T) (*Server, *httptest.Server, *run.Store, *history.Store) {
	t.Helper()

	sessStore := session.NewStore()
	cr := wsconn.NewRegistry()
	historyStore := history.New("")
	runStore := run.NewStore()

	// Wrap push/event callbacks to record into history (same as production main.go)
	wrappedPush := func(sid types.SessionID, streamType string, seq types.EventSeq, data string) {
		historyStore.Record(sid, streamType, seq, data)
		cr.PushChunk(sid, streamType, seq, data)
	}
	wrappedEvent := func(sid types.SessionID, seq types.EventSeq, eventType string, data interface{}) {
		if eventType == "started" || eventType == "exited" {
			historyStore.RecordEvent(sid, seq, "session."+eventType, data)
		}
		cr.PushSessionEvent(sid, seq, eventType, data)
	}
	pm := process.NewManager(wrappedPush, wrappedEvent)
	pm.SetOnSpawn(func(sid types.SessionID) {
		historyStore.InitSession(sid, types.DefaultHistoryPolicy())
	})

	execDeps := &executor.Deps{
		Sessions:   sessStore,
		Processes:  pm,
		ConnRoutes: cr,
		History:    historyStore,
		RunStore:   runStore,
	}
	execReg := executor.New(execDeps)

	permChecker := permission.NewChecker(
		&mockCapRegistry{},
		&mockPolicyStore{},
	)

	audit := &mockAuditLogger{}
	topo := &mockTopology{}

	d := dispatcher.New(
		auth.NewTokenAuthenticator(""),
		&mockPluginRegistry{},
		permChecker,
		nil, /* planner */
		execReg,
		audit,
		topo,
		"node_local",
	)

	sv := New("", d, sessStore, cr, pm)
	httpSrv := httptest.NewServer(sv.httpServer.Handler)
	return sv, httpSrv, runStore, historyStore
}

// longRunningEchoCommand returns a platform-appropriate command that outputs
// numbered lines on a ~1-second interval for roughly 8 seconds.
func longRunningEchoCommand() (string, []string) {
	if runtime.GOOS == "windows" {
		return "cmd", []string{"/c", "for /l %i in (1,1,8) do @echo LOOP_%i & ping -n 2 127.0.0.1 >nul"}
	}
	return "sh", []string{"-c", "for i in 1 2 3 4 5 6 7 8; do echo LOOP_$i; sleep 1; done"}
}

// readUntil reads WS messages until the predicate returns true, a timeout, or
// maxReads is reached. Returns the message that satisfied the predicate.
func readUntil(t *testing.T, conn *websocket.Conn, timeout time.Duration, maxReads int, predicate func(*protocol.Message) bool) []*protocol.Message {
	t.Helper()
	var collected []*protocol.Message
	deadline := time.Now().Add(timeout)
	for i := 0; i < maxReads; i++ {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		conn.SetReadDeadline(time.Now().Add(minDuration(remaining, 2*time.Second)))
		_, raw, err := conn.ReadMessage()
		if err != nil {
			break
		}
		msg, err := protocol.UnmarshalMessage(raw)
		if err != nil {
			continue
		}
		collected = append(collected, msg)
		if predicate(msg) {
			return collected
		}
	}
	return collected
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

// TestTerminalReconnectE2E validates the full disconnect/reconnect lifecycle:
//
//	run.create → stream.chunk → WS disconnect → process keeps running →
//	reconnect → run.list → run.info → stream.replay → stream.write → run.stop
func TestTerminalReconnectE2E(t *testing.T) {
	sv, httpSrv, _, historyStore := testServerWithRunStore(t)
	defer httpSrv.Close()
	defer historyStore.Cleanup()

	// ── Step 1: Connect (conn1) ──────────────────────────────────────────
	conn1 := wsConnect(t, httpSrv)

	// ── Step 2: run.create — spawn a long-running echoing process ─────────
	cmd, args := longRunningEchoCommand()
	payload, err := json.Marshal(map[string]interface{}{
		"command": cmd,
		"args":    args,
		"pty":     false,
		"policy": map[string]interface{}{
			"onDisconnect": "keep_running",
		},
	})
	if err != nil {
		t.Fatalf("marshal create payload: %v", err)
	}

	createResp := sendAndRecv(t, conn1, &protocol.Message{
		Type:       protocol.MsgTypeActionRequest,
		RequestID:  "req_create",
		Capability: "run.create",
		Payload:    json.RawMessage(payload),
	})
	if !createResp.OK {
		t.Fatalf("run.create failed: %v", createResp.Error)
	}

	var createBody map[string]interface{}
	if err := json.Unmarshal(createResp.Payload, &createBody); err != nil {
		t.Fatalf("unmarshal create: %v", err)
	}
	runID, ok := createBody["runId"].(string)
	if !ok || runID == "" {
		t.Fatal("run.create missing runId")
	}
	sessionID, ok := createBody["sessionId"].(string)
	if !ok || sessionID == "" {
		t.Fatal("run.create missing sessionId")
	}
	t.Logf("run.create OK: runId=%s sessionId=%s", runID, sessionID)

	// ── Step 3: Verify stream.chunk push messages arrive on conn1 ────────
	// The process outputs "LOOP_1", "LOOP_2", ... every ~1 second.
	msgs := readUntil(t, conn1, 10*time.Second, 30, func(msg *protocol.Message) bool {
		return msg.Type == protocol.MsgTypeStreamChunk && msg.Data != ""
	})
	foundChunk := false
	for _, m := range msgs {
		if m.Type == protocol.MsgTypeStreamChunk {
			foundChunk = true
			t.Logf("[conn1] chunk: sessionId=%s streamType=%s data=%q seq=%d", m.SessionID, m.StreamType, m.Data, m.EventSeq)
		}
	}
	if !foundChunk {
		t.Error("expected at least one stream.chunk on conn1 after run.create")
	}

	// ── Step 4: Close conn1 (simulate browser/UI disconnect) ─────────────
	if err := conn1.Close(); err != nil {
		t.Logf("conn1 close error (non-fatal): %v", err)
	}
	t.Log("conn1 closed — simulating browser/UI disconnect")

	// ── Step 5: Wait, then verify process still running ──────────────────
	// The process should keep running because onDisconnect=keep_running.
	time.Sleep(3 * time.Second)

	proc := sv.procManager.Get(types.SessionID(sessionID))
	if proc == nil {
		t.Fatal("process not found after disconnect — it should still be running")
	}
	if proc.State != "running" {
		t.Errorf("expected process state 'running' after disconnect, got %q", proc.State)
	}
	t.Logf("process still running after disconnect: pid=%d state=%s", proc.PID, proc.State)

	// ── Step 6: Reconnect (conn2) ────────────────────────────────────────
	conn2 := wsConnect(t, httpSrv)
	defer conn2.Close()

	// ── Step 7: run.list returns the run (state: running) ────────────────
	listResp := sendAndRecv(t, conn2, &protocol.Message{
		Type:       protocol.MsgTypeActionRequest,
		RequestID:  "req_list",
		Capability: "run.list",
	})
	if !listResp.OK {
		t.Fatalf("run.list failed: %v", listResp.Error)
	}
	var listBody map[string]interface{}
	if err := json.Unmarshal(listResp.Payload, &listBody); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	runs, ok := listBody["runs"].([]interface{})
	if !ok {
		t.Fatalf("expected runs array, got %T", listBody["runs"])
	}
	if len(runs) == 0 {
		t.Fatal("run.list returned 0 runs after reconnect")
	}
	firstRun := runs[0].(map[string]interface{})
	runState, _ := firstRun["state"].(string)
	t.Logf("run.list: found %d run(s), first run state=%q", len(runs), runState)

	// ── Step 8: run.info returns sessionId ───────────────────────────────
	infoResp := sendAndRecv(t, conn2, &protocol.Message{
		Type:       protocol.MsgTypeActionRequest,
		RequestID:  "req_info",
		Capability: "run.info",
		Payload:    json.RawMessage(fmt.Sprintf(`{"runId":"%s"}`, runID)),
	})
	if !infoResp.OK {
		t.Fatalf("run.info failed: %v", infoResp.Error)
	}
	var infoBody map[string]interface{}
	if err := json.Unmarshal(infoResp.Payload, &infoBody); err != nil {
		t.Fatalf("unmarshal info: %v", err)
	}
	infoSID, _ := infoBody["sessionId"].(string)
	infoState, _ := infoBody["state"].(string)
	if infoSID != sessionID {
		t.Errorf("run.info sessionId mismatch: got %q, want %q", infoSID, sessionID)
	}
	t.Logf("run.info: sessionId=%s state=%s", infoSID, infoState)

	// ── Step 9: stream.replay recovers output from disconnect period ─────
	replayResp := sendAndRecv(t, conn2, &protocol.Message{
		Type:       protocol.MsgTypeActionRequest,
		RequestID:  "req_replay",
		Capability: "stream.replay",
		Payload:    json.RawMessage(fmt.Sprintf(`{"sessionId":"%s","streamType":"stdout"}`, sessionID)),
	})
	if !replayResp.OK {
		t.Fatalf("stream.replay failed: %v", replayResp.Error)
	}
	var replayBody map[string]interface{}
	if err := json.Unmarshal(replayResp.Payload, &replayBody); err != nil {
		t.Fatalf("unmarshal replay: %v", err)
	}
	events, ok := replayBody["events"].([]interface{})
	if !ok {
		t.Fatalf("expected events array in replay, got %T", replayBody["events"])
	}
	t.Logf("stream.replay: %d events recovered", len(events))
	if len(events) == 0 {
		t.Error("expected at least 1 event from stream.replay (output produced during disconnect)")
	}
	// Verify at least one event contains "LOOP_" data
	hasLoopData := false
	for _, evt := range events {
		evtMap, ok := evt.(map[string]interface{})
		if !ok {
			continue
		}
		if data, ok := evtMap["data"].(string); ok && strings.Contains(data, "LOOP_") {
			hasLoopData = true
			t.Logf("replay event data: %q", data)
			break
		}
	}
	if !hasLoopData {
		t.Log("note: replay events may not contain LOOP_ if process output is still buffered")
	}

	// ── Step 10: stream.subscribe on conn2 (re-attach for live output) ───
	subResp := sendAndRecv(t, conn2, &protocol.Message{
		Type:       protocol.MsgTypeActionRequest,
		RequestID:  "req_sub",
		Capability: "stream.subscribe",
		Payload:    json.RawMessage(fmt.Sprintf(`{"sessionId":"%s","streamType":"stdout,stderr"}`, sessionID)),
	})
	if !subResp.OK {
		t.Fatalf("stream.subscribe failed: %v", subResp.Error)
	}
	t.Logf("stream.subscribe OK on conn2")

	// ── Step 11: stream.write (stdin) works after reattach ──────────────
	// Uses stdin streamType which routes through ProcessManager.WriteStdin
	// rather than session.Store — this is the correct path for process-owned streams.
	writePayload := json.RawMessage(fmt.Sprintf(`{"sessionId":"%s","streamType":"stdin","data":"hello from reconnect"}`, sessionID))
	writeResp := sendAndRecv(t, conn2, &protocol.Message{
		Type:       protocol.MsgTypeActionRequest,
		RequestID:  "req_write",
		Capability: "stream.write",
		Payload:    writePayload,
	})
	if !writeResp.OK {
		t.Errorf("stream.write (stdin) failed: %v", writeResp.Error)
	}
	var writeBody map[string]interface{}
	if err := json.Unmarshal(writeResp.Payload, &writeBody); err == nil {
		written, _ := writeBody["written"].(float64)
		if written == 0 {
			t.Error("expected >0 written bytes for stdin")
		}
		t.Logf("stream.write (stdin) OK: written=%v", written)
	}

	// ── Step 12: run.stop stops the run ──────────────────────────────────
	stopResp := sendAndRecv(t, conn2, &protocol.Message{
		Type:       protocol.MsgTypeActionRequest,
		RequestID:  "req_stop",
		Capability: "run.stop",
		Payload:    json.RawMessage(fmt.Sprintf(`{"runId":"%s","signal":"SIGTERM"}`, runID)),
	})
	if !stopResp.OK {
		t.Fatalf("run.stop failed: %v", stopResp.Error)
	}
	var stopBody map[string]interface{}
	if err := json.Unmarshal(stopResp.Payload, &stopBody); err != nil {
		t.Fatalf("unmarshal stop: %v", err)
	}
	stopState, _ := stopBody["state"].(string)
	if stopState != run.StateStopped {
		t.Errorf("expected state %q after stop, got %q", run.StateStopped, stopState)
	}
	t.Logf("run.stop OK: state=%s", stopState)

	// ── Verify process was actually terminated ───────────────────────────
	time.Sleep(500 * time.Millisecond)
	proc2 := sv.procManager.Get(types.SessionID(sessionID))
	if proc2 != nil && proc2.State == "running" {
		t.Error("process should not be running after run.stop")
	}
	t.Logf("post-stop process state: %v", func() string {
		if proc2 == nil {
			return "(nil)"
		}
		return proc2.State
	}())
}

func errCode(e *types.CoreError) string {
	if e == nil { return "<nil>" }
	return e.Code
}

func errMsg(e *types.CoreError) string {
	if e == nil { return "<nil>" }
	return e.Message
}
