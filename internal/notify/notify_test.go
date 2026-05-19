package notify

import (
	"testing"
	"time"

	"github.com/user/sessionnode/go-core/pkg/protocol"
)

// capturePush creates a Manager that records all pushed messages into the returned slice.
func capturePush(t *testing.T) (*Manager, *[]*protocol.Message) {
	t.Helper()
	var msgs []*protocol.Message
	push := func(msg *protocol.Message) {
		msgs = append(msgs, msg)
	}
	m := NewManager(push)
	return m, &msgs
}

func TestSendNotification_PushesMessage(t *testing.T) {
	m, msgs := capturePush(t)

	n, err := m.SendNotification("plugin.test", TypeInfo, "Test Title", "Test Body", 0)
	if err != nil {
		t.Fatalf("SendNotification failed: %v", err)
	}
	if n.ID == "" {
		t.Fatal("expected non-empty notification ID")
	}
	if n.Title != "Test Title" {
		t.Fatalf("expected title %q, got %q", "Test Title", n.Title)
	}
	if n.Type != TypeInfo {
		t.Fatalf("expected type %q, got %q", TypeInfo, n.Type)
	}
	if n.Body != "Test Body" {
		t.Fatalf("expected body %q, got %q", "Test Body", n.Body)
	}
	if len(*msgs) != 1 {
		t.Fatalf("expected 1 pushed message, got %d", len(*msgs))
	}
	msg := (*msgs)[0]
	if msg.Type != protocol.MsgTypeNotifyRequest {
		t.Fatalf("expected message type %q, got %q", protocol.MsgTypeNotifyRequest, msg.Type)
	}
	if msg.PluginID != "plugin.test" {
		t.Fatalf("expected pluginId %q, got %q", "plugin.test", msg.PluginID)
	}
}

func TestSendNotification_EmptyTitleError(t *testing.T) {
	m, _ := capturePush(t)

	_, err := m.SendNotification("plugin.test", TypeInfo, "", "body", 0)
	if err == nil {
		t.Fatal("expected error for empty title")
	}
}

func TestCreateApproval_StoresPendingRequest(t *testing.T) {
	m, msgs := capturePush(t)

	actions := []ApprovalAction{
		{ID: "yes", Label: "Yes"},
		{ID: "no", Label: "No"},
	}
	apr, err := m.CreateApproval(
		"plugin.test",
		"Approve?",
		"Do you approve this?",
		"Detail about the request",
		actions,
		30,
	)
	if err != nil {
		t.Fatalf("CreateApproval failed: %v", err)
	}
	if !apr.RequestID.Valid() {
		t.Fatal("expected valid request ID")
	}
	if apr.Responded {
		t.Fatal("expected pending approval (not responded)")
	}
	if apr.Title != "Approve?" {
		t.Fatalf("expected title %q, got %q", "Approve?", apr.Title)
	}
	if len(apr.Actions) != 2 {
		t.Fatalf("expected 2 actions, got %d", len(apr.Actions))
	}
	if len(*msgs) != 1 {
		t.Fatalf("expected 1 pushed message, got %d", len(*msgs))
	}
	msg := (*msgs)[0]
	if msg.Type != protocol.MsgTypeNotifyApprovalReq {
		t.Fatalf("expected message type %q, got %q", protocol.MsgTypeNotifyApprovalReq, msg.Type)
	}
	if msg.PluginID != "plugin.test" {
		t.Fatalf("expected pluginId %q, got %q", "plugin.test", msg.PluginID)
	}
}

func TestRespond_ResolvesPendingRequest(t *testing.T) {
	m, _ := capturePush(t)

	apr, err := m.CreateApproval("plugin.test", "Approve?", "body", "", nil, 30)
	if err != nil {
		t.Fatalf("CreateApproval failed: %v", err)
	}

	resp, err := m.Respond(apr.RequestID, "allow", "user")
	if err != nil {
		t.Fatalf("Respond failed: %v", err)
	}
	if resp.Action != "allow" {
		t.Fatalf("expected action %q, got %q", "allow", resp.Action)
	}
	if resp.RespondedBy != "user" {
		t.Fatalf("expected respondedBy %q, got %q", "user", resp.RespondedBy)
	}
	if !apr.Responded {
		t.Fatal("expected approval to be marked responded")
	}
}

func TestDoubleRespond_ReturnsError(t *testing.T) {
	m, _ := capturePush(t)

	apr, err := m.CreateApproval("plugin.test", "Approve?", "body", "", nil, 30)
	if err != nil {
		t.Fatalf("CreateApproval failed: %v", err)
	}

	_, err = m.Respond(apr.RequestID, "allow", "user")
	if err != nil {
		t.Fatalf("first Respond failed: %v", err)
	}

	_, err = m.Respond(apr.RequestID, "deny", "user2")
	if err == nil {
		t.Fatal("expected error on double respond")
	}
}

func TestApproval_AutoExpires(t *testing.T) {
	m, _ := capturePush(t)

	// Use a 1-second timeout so the test completes reasonably quickly.
	apr, err := m.CreateApproval("plugin.test", "Expire?", "body", "", nil, 1)
	if err != nil {
		t.Fatalf("CreateApproval failed: %v", err)
	}

	resp := apr.WaitForResponse()
	if resp == nil {
		t.Fatal("expected non-nil response after timeout")
	}
	if resp.Action != "timeout" {
		t.Fatalf("expected action %q, got %q", "timeout", resp.Action)
	}
	if resp.RespondedBy != "system" {
		t.Fatalf("expected respondedBy %q, got %q", "system", resp.RespondedBy)
	}
}

func TestWaitForResponse_ReturnsWhenResponded(t *testing.T) {
	m, _ := capturePush(t)

	apr, err := m.CreateApproval("plugin.test", "Approve?", "body", "", nil, 60)
	if err != nil {
		t.Fatalf("CreateApproval failed: %v", err)
	}

	// Respond from a separate goroutine after a short delay.
	go func() {
		time.Sleep(10 * time.Millisecond)
		m.Respond(apr.RequestID, "allow", "user")
	}()

	resp := apr.WaitForResponse()
	if resp == nil {
		t.Fatal("expected non-nil response")
	}
	if resp.Action != "allow" {
		t.Fatalf("expected action %q, got %q", "allow", resp.Action)
	}
}

func TestDefaultActions(t *testing.T) {
	m, _ := capturePush(t)

	apr, err := m.CreateApproval("plugin.test", "Approve?", "body", "", nil, 30)
	if err != nil {
		t.Fatalf("CreateApproval failed: %v", err)
	}
	if len(apr.Actions) != 2 {
		t.Fatalf("expected 2 default actions, got %d", len(apr.Actions))
	}
	if apr.Actions[0].ID != "allow" || apr.Actions[1].ID != "deny" {
		t.Fatalf("expected default actions [allow, deny], got %+v", apr.Actions)
	}
}

func TestDefaultTimeout(t *testing.T) {
	m, _ := capturePush(t)

	apr, err := m.CreateApproval("plugin.test", "Approve?", "body", "", nil, 0)
	if err != nil {
		t.Fatalf("CreateApproval failed: %v", err)
	}
	if apr.Timeout != 60 {
		t.Fatalf("expected default timeout 60, got %d", apr.Timeout)
	}
}
