package protocol

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/user/sessionnode/go-core/pkg/types"
)

func TestMessageConstants(t *testing.T) {
	if MsgTypeActionRequest != "action.request" {
		t.Errorf("MsgTypeActionRequest = %q, want action.request", MsgTypeActionRequest)
	}
	if MsgTypePing != "ping" {
		t.Errorf("MsgTypePing = %q, want ping", MsgTypePing)
	}
	if MsgTypePong != "pong" {
		t.Errorf("MsgTypePong = %q, want pong", MsgTypePong)
	}
}

func TestErrorConstants(t *testing.T) {
	if ErrCodeUnauthenticated != "UNAUTHENTICATED" {
		t.Errorf("ErrCodeUnauthenticated = %q, want UNAUTHENTICATED", ErrCodeUnauthenticated)
	}
	if ErrCodePermissionDenied != "PERMISSION_DENIED" {
		t.Errorf("ErrCodePermissionDenied = %q, want PERMISSION_DENIED", ErrCodePermissionDenied)
	}
}

func TestNewError(t *testing.T) {
	m := NewError("req_001", ErrCodePermissionDenied, "no access")
	if m.Type != MsgTypeError {
		t.Errorf("Type = %q, want %q", m.Type, MsgTypeError)
	}
	if m.RequestID != "req_001" {
		t.Errorf("RequestID = %q, want req_001", m.RequestID)
	}
	if m.OK {
		t.Error("OK should be false for error messages")
	}
	if m.Error == nil || m.Error.Code != ErrCodePermissionDenied {
		t.Errorf("Error.Code = %v, want %s", m.Error, ErrCodePermissionDenied)
	}
}

func TestNewPingPong(t *testing.T) {
	ping := NewPing()
	if ping.Type != MsgTypePing {
		t.Errorf("Ping type = %q", ping.Type)
	}
	pong := NewPong()
	if pong.Type != MsgTypePong {
		t.Errorf("Pong type = %q", pong.Type)
	}
}

func TestNewHello(t *testing.T) {
	m := NewHello("node_abc", "1.0.0")
	if m.Type != MsgTypeHello {
		t.Errorf("Type = %q", m.Type)
	}
	if m.NodeID != "node_abc" {
		t.Errorf("NodeID = %q", m.NodeID)
	}
	if m.Data != "1.0.0" {
		t.Errorf("version = %q", m.Data)
	}
}

func TestNewActionRequest(t *testing.T) {
	req := createTestCapabilityRequest()
	m := NewActionRequest(req)
	if m.Type != MsgTypeActionRequest {
		t.Errorf("Type = %q", m.Type)
	}
	if m.RequestID != req.RequestID {
		t.Errorf("RequestID = %q", m.RequestID)
	}
	if m.PluginID != req.PluginID {
		t.Errorf("PluginID = %q", m.PluginID)
	}
	if m.Capability != req.Capability {
		t.Errorf("Capability = %q", m.Capability)
	}
	if len(m.Payload) == 0 {
		t.Error("Payload should not be empty")
	}
}

func TestNewActionResponse_Success(t *testing.T) {
	resp := createTestCapabilityResponse(true, nil)
	m := NewActionResponse(resp)
	if m.Type != MsgTypeActionResponse {
		t.Errorf("Type = %q", m.Type)
	}
	if !m.OK {
		t.Error("OK should be true for success response")
	}
	if m.Error != nil {
		t.Errorf("Error should be nil for success, got %v", m.Error)
	}
	if len(m.Payload) == 0 {
		t.Error("Payload should be marshaled for success with data")
	}
}

func TestNewActionResponse_Error(t *testing.T) {
	resp := createTestCapabilityResponse(false, &types.CoreError{Code: ErrCodePermissionDenied, Message: "denied"})
	m := NewActionResponse(resp)
	if m.Type != MsgTypeActionResponse {
		t.Errorf("Type = %q", m.Type)
	}
	if m.OK {
		t.Error("OK should be false for error response")
	}
	if m.Error == nil || m.Error.Code != ErrCodePermissionDenied {
		t.Errorf("Error code = %v", m.Error)
	}
}

func TestMarshalUnmarshal_RoundTrip(t *testing.T) {
	tests := []struct {
		name string
		msg  *Message
	}{
		{"ping", NewPing()},
		{"pong", NewPong()},
		{"hello", NewHello("node_abc", "1.0.0")},
		{"welcome", NewWelcome("node_abc")},
		{"error", NewError("req_001", ErrCodeInternalError, "something broke")},
		{"session.create", NewSessionCreate("shell", "", json.RawMessage(`{"command":"bash"}`))},
		{"session.created", NewSessionCreated("req_001", "sess_abc")},
		{"stream.subscribe", NewStreamSubscribe("req_001", "sess_abc", "stdout")},
		{"stream.chunk", NewStreamChunk("sess_abc", "stdout", 42, "base64data")},
		{"stream.write", NewStreamWrite("req_001", "sess_abc", "input data")},
		{"notify.request", NewNotifyRequest("req_001", "claude-code", json.RawMessage(`{"title":"Confirm"}`))},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := tt.msg.MarshalJSON()
			if err != nil {
				t.Fatalf("marshal error: %v", err)
			}
			got, err := UnmarshalMessage(data)
			if err != nil {
				t.Fatalf("unmarshal error: %v (json: %s)", err, string(data))
			}
			if got.Type != tt.msg.Type {
				t.Errorf("Type mismatch: got %q, want %q", got.Type, tt.msg.Type)
			}
		})
	}
}

func TestDecodePayload(t *testing.T) {
	payload := json.RawMessage(`{"path":"/home","recursive":true}`)
	m := NewActionRequest(createTestCapabilityRequestWithPayload(payload))

	var decoded struct {
		Path      string `json:"path"`
		Recursive bool   `json:"recursive"`
	}
	if err := m.DecodePayload(&decoded); err != nil {
		t.Fatalf("DecodePayload error: %v", err)
	}
	if decoded.Path != "/home" {
		t.Errorf("Path = %q, want /home", decoded.Path)
	}
	if !decoded.Recursive {
		t.Error("Recursive should be true")
	}
}

func TestDecodePayload_Nil(t *testing.T) {
	m := &Message{Type: MsgTypePing}
	var v interface{}
	if err := m.DecodePayload(&v); err != nil {
		t.Errorf("DecodePayload on nil payload should not error: %v", err)
	}
}

func TestMarshalUnmarshal_EdgeCases(t *testing.T) {
	// Empty message
	data, err := (&Message{}).MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	got, err := UnmarshalMessage(data)
	if err != nil {
		t.Fatalf("unmarshal empty message error: %v", err)
	}
	if got.Type != "" {
		t.Errorf("empty message Type = %q", got.Type)
	}

	// Message with ok:false
	m := &Message{Type: MsgTypeActionResponse, OK: false}
	data, _ = m.MarshalJSON()
	if !strings.Contains(string(data), `"ok":false`) {
		t.Errorf("ok:false should appear in JSON: %s", string(data))
	}
}

func TestMarshalUnmarshal_StreamReplay(t *testing.T) {
	m := NewStreamReplay("req_001", "sess_abc", "stdout", 10, 100)
	data, err := m.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	got, err := UnmarshalMessage(data)
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != MsgTypeStreamReplay {
		t.Errorf("Type = %q", got.Type)
	}
	if got.EventSeq != 10 {
		t.Errorf("EventSeq (fromSeq) = %d", got.EventSeq)
	}
	if got.Timestamp != 100 {
		t.Errorf("Timestamp (toSeq) = %d", got.Timestamp)
	}
}

func TestNewCoreError(t *testing.T) {
	ce := NewCoreError(ErrCodeInternalError, "test error")
	if ce.Code != ErrCodeInternalError {
		t.Errorf("Code = %q", ce.Code)
	}
	if ce.Message != "test error" {
		t.Errorf("Message = %q", ce.Message)
	}
}

// --- Helpers ---

func createTestCapabilityRequest() *types.CapabilityRequest {
	payload, _ := json.Marshal(map[string]string{"command": "bash"})
	return &types.CapabilityRequest{
		RequestID:    "req_001",
		PluginID:     "shell",
		Capability:   "session.create",
		TargetNodeID: "",
		Payload:      payload,
					Actor: types.Actor{Type: "web", ID: "browser_abc"},
		Timestamp:    1712345678000,
	}
}

func createTestCapabilityRequestWithPayload(payload json.RawMessage) *types.CapabilityRequest {
	return &types.CapabilityRequest{
		RequestID:  "req_001",
		PluginID:   "file-explorer",
		Capability: "fs.list",
		Payload:    payload,
					Actor: types.Actor{Type: "cli", ID: "user_zhp"},
		Timestamp:  1712345678000,
	}
}

func createTestCapabilityResponse(ok bool, err *types.CoreError) *types.CapabilityResponse {
	resp := &types.CapabilityResponse{
		RequestID: "req_001",
		OK:        ok,
	}
	if ok {
		resp.Payload = map[string]interface{}{"sessionId": "sess_abc", "status": "running"}
	}
	if err != nil {
		resp.Error = err
	}
	return resp
}
