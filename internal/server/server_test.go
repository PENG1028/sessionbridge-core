package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/user/sessionnode/go-core/internal/auth"
	"github.com/user/sessionnode/go-core/internal/dispatcher"
	"github.com/user/sessionnode/go-core/internal/executor"
	"github.com/user/sessionnode/go-core/internal/permission"
	"github.com/user/sessionnode/go-core/internal/process"
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

	// After spawn, the server pushes stream.chunk messages.
	// Read up to 10 messages looking for stdout data.
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
