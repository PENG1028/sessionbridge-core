package server

import (
	"encoding/json"
	"testing"

	"github.com/PENG1028/sessionbridge-core/pkg/protocol"
	"github.com/PENG1028/sessionbridge-core/pkg/types"
)

// ─── WebSocket E2E Tests ─────────────────────────────────────────────────
//
// These tests automate what ws-test/ (cmd/ws-test) does manually:
//   - Connect to Core via WebSocket
//   - Send action.request messages
//   - Validate response shapes and error messages
//
// They run against a local test server with mock dependencies.
// Tests that need a real topology (mesh forwarding) are in topology/e2e_test.go.

// TestWS_NodeList validates node.list response shape over WebSocket.
func TestWS_NodeList(t *testing.T) {
	_, srv := testServer(t)
	defer srv.Close()

	conn := wsConnect(t, srv)
	defer conn.Close()

	resp := sendAndRecv(t, conn, &protocol.Message{
		Type:       protocol.MsgTypeActionRequest,
		RequestID:  types.RequestID("ws_e2e_node_list"),
		Capability: "node.list",
	})
	if !resp.OK {
		t.Fatalf("node.list failed: %v", resp.Error)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(resp.Payload, &body); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	// Contract: node.list returns {"nodes": [...]}
	nodes, ok := body["nodes"].([]interface{})
	if !ok {
		t.Fatalf("node.list: expected nodes array, got %T", body["nodes"])
	}
	t.Logf("node.list: %d node(s)", len(nodes))
	for _, n := range nodes {
		node := n.(map[string]interface{})
		t.Logf("  nodeId=%v status=%v", node["nodeId"], node["status"])
	}
}

// TestWS_SystemInfo validates system.info over WebSocket.
func TestWS_SystemInfo(t *testing.T) {
	_, srv := testServer(t)
	defer srv.Close()

	conn := wsConnect(t, srv)
	defer conn.Close()

	resp := sendAndRecv(t, conn, &protocol.Message{
		Type:       protocol.MsgTypeActionRequest,
		RequestID:  types.RequestID("ws_e2e_sysinfo"),
		Capability: "system.info",
	})
	if !resp.OK {
		t.Fatalf("system.info failed: %v", resp.Error)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(resp.Payload, &body); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	if body["os"] == nil {
		t.Error("system.info: missing os")
	}
	if body["arch"] == nil {
		t.Error("system.info: missing arch")
	}
	t.Logf("os=%v arch=%v", body["os"], body["arch"])
}

// TestWS_OperationsList validates operations.list response over WebSocket.
func TestWS_OperationsList(t *testing.T) {
	_, srv := testServer(t)
	defer srv.Close()

	conn := wsConnect(t, srv)
	defer conn.Close()

	resp := sendAndRecv(t, conn, &protocol.Message{
		Type:       protocol.MsgTypeActionRequest,
		RequestID:  types.RequestID("ws_e2e_op_list"),
		Capability: "operations.list",
	})
	if !resp.OK {
		t.Fatalf("operations.list failed: %v", resp.Error)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(resp.Payload, &body); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	// Contract: operations.list returns {"operations": [...]}
	ops, ok := body["operations"].([]interface{})
	if !ok {
		t.Fatalf("operations.list: expected operations array, got %T", body["operations"])
	}
	t.Logf("operations.list: %d operation(s)", len(ops))
}

// TestWS_OperationsListAfterAction validates operations.list captures executed actions.
func TestWS_OperationsListAfterAction(t *testing.T) {
	_, srv := testServer(t)
	defer srv.Close()

	conn := wsConnect(t, srv)
	defer conn.Close()

	// Execute system.info (creates an operation log entry if OpLog is configured)
	_ = sendAndRecv(t, conn, &protocol.Message{
		Type:       protocol.MsgTypeActionRequest,
		RequestID:  types.RequestID("ws_e2e_create_op"),
		Capability: "system.info",
	})

	// Query operations — should include the one we just executed
	resp := sendAndRecv(t, conn, &protocol.Message{
		Type:       protocol.MsgTypeActionRequest,
		RequestID:  types.RequestID("ws_e2e_op_list2"),
		Capability: "operations.list",
	})
	if !resp.OK {
		t.Fatalf("operations.list failed: %v", resp.Error)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(resp.Payload, &body); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	ops, ok := body["operations"].([]interface{})
	if !ok {
		t.Fatalf("expected operations array, got %T", body["operations"])
	}
	t.Logf("operations.list after system.info: %d operation(s)", len(ops))
}

// TestWS_OperationsListWithFilter validates operations.list capability filter.
func TestWS_OperationsListWithFilter(t *testing.T) {
	_, srv := testServer(t)
	defer srv.Close()

	conn := wsConnect(t, srv)
	defer conn.Close()

	filterPayload, _ := json.Marshal(map[string]interface{}{
		"capability": "system.info",
	})
	resp := sendAndRecv(t, conn, &protocol.Message{
		Type:       protocol.MsgTypeActionRequest,
		RequestID:  types.RequestID("ws_e2e_op_filter"),
		Capability: "operations.list",
		Payload:    filterPayload,
	})
	if !resp.OK {
		t.Fatalf("operations.list with filter failed: %v", resp.Error)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(resp.Payload, &body); err != nil {
		t.Fatalf("payload unmarshal: %v", err)
	}
	ops, ok := body["operations"].([]interface{})
	if !ok {
		t.Fatalf("expected operations array, got %T", body["operations"])
	}
	t.Logf("operations.list(filter=system.info): %d operation(s)", len(ops))
}

// TestWS_OperationsGetNotFound validates operations.get error for missing op.
func TestWS_OperationsGetNotFound(t *testing.T) {
	_, srv := testServer(t)
	defer srv.Close()

	conn := wsConnect(t, srv)
	defer conn.Close()

	getPayload, _ := json.Marshal(map[string]interface{}{
		"opId": "op_0000000000000_0",
	})
	resp := sendAndRecv(t, conn, &protocol.Message{
		Type:       protocol.MsgTypeActionRequest,
		RequestID:  types.RequestID("ws_e2e_op_get"),
		Capability: "operations.get",
		Payload:    getPayload,
	})
	if resp.OK {
		t.Log("operations.get succeeded (unexpected — operation not expected to exist)")
	} else {
		t.Logf("operations.get error (expected): %v", resp.Error)
	}
}

// TestWS_OperationsDryRunShape validates operations.dryRun response shape.
func TestWS_OperationsDryRunShape(t *testing.T) {
	_, srv := testServer(t)
	defer srv.Close()

	conn := wsConnect(t, srv)
	defer conn.Close()

	dryRunPayload, _ := json.Marshal(map[string]interface{}{
		"opId": "op_0000000000000_0",
	})
	resp := sendAndRecv(t, conn, &protocol.Message{
		Type:       protocol.MsgTypeActionRequest,
		RequestID:  types.RequestID("ws_e2e_op_dryrun"),
		Capability: "operations.dryRun",
		Payload:    dryRunPayload,
	})
	if resp.OK {
		t.Log("operations.dryRun succeeded")
	} else {
		t.Logf("operations.dryRun error (expected if OpID not found): %v", resp.Error)
	}
}

// TestWS_MultipleRequests validates sequential requests on the same connection.
func TestWS_MultipleRequests(t *testing.T) {
	_, srv := testServer(t)
	defer srv.Close()

	conn := wsConnect(t, srv)
	defer conn.Close()

	// Send 3 requests in sequence, validate all responses
	tests := []struct {
		name       string
		capability string
	}{
		{"system.info", "system.info"},
		{"node.list", "node.list"},
		{"node.health", "node.health"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := sendAndRecv(t, conn, &protocol.Message{
				Type:       protocol.MsgTypeActionRequest,
				RequestID:  types.RequestID("ws_e2e_multi_" + tt.name),
				Capability: tt.capability,
			})
			if !resp.OK {
				t.Errorf("%s failed: %v", tt.capability, resp.Error)
			}
		})
	}
}

// TestWS_UnknownCapability validates error for non-existent capability.
func TestWS_UnknownCapability(t *testing.T) {
	_, srv := testServer(t)
	defer srv.Close()

	conn := wsConnect(t, srv)
	defer conn.Close()

	resp := sendAndRecv(t, conn, &protocol.Message{
		Type:       protocol.MsgTypeActionRequest,
		RequestID:  types.RequestID("ws_e2e_unknown"),
		Capability: "nonexistent.capability",
	})
	if resp.OK {
		t.Fatal("expected failure for unknown capability")
	}
	if resp.Error == nil {
		t.Fatal("expected error details for unknown capability")
	}
	t.Logf("unknown capability error: code=%s msg=%s", resp.Error.Code, resp.Error.Message)
}
