package notify

import (
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/user/sessionnode/go-core/pkg/protocol"
	"github.com/user/sessionnode/go-core/pkg/types"
)

// Notification types.
const (
	TypeInfo  = "info"
	TypeWarn  = "warn"
	TypeError = "error"
)

// Notification represents a push notification to be delivered to Web UI clients.
type Notification struct {
	ID        string `json:"notificationId"`
	Type      string `json:"type"`
	Title     string `json:"title"`
	Body      string `json:"body,omitempty"`
	PluginID  string `json:"pluginId,omitempty"`
	Timeout   int    `json:"timeout,omitempty"` // seconds
	Timestamp int64  `json:"timestamp"`
}

// ApprovalAction is a single action button in an approval request.
type ApprovalAction struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// ApprovalRequest represents a pending approval.
type ApprovalRequest struct {
	RequestID types.RequestID   `json:"requestId"`
	PluginID  types.PluginID    `json:"pluginId"`
	Title     string            `json:"title"`
	Body      string            `json:"body"`
	Detail    string            `json:"detail,omitempty"`
	Actions   []ApprovalAction  `json:"actions"`
	Timeout   int               `json:"timeout"` // seconds
	CreatedAt int64             `json:"createdAt"`
	Responded bool              `json:"-"`
	Response  *ApprovalResponse `json:"-"`
	ExpiresAt time.Time         `json:"-"`
	done      chan struct{}     // closed when responded or timed out
}

// ApprovalResponse is the user's response to an approval request.
type ApprovalResponse struct {
	RequestID   types.RequestID `json:"requestId"`
	Action      string          `json:"action"`
	RespondedBy string          `json:"respondedBy"`
	Timestamp   int64           `json:"timestamp"`
}

// PushFunc is called to deliver a message to all connected Web UI clients.
// The caller (whoever integrates this package) provides this.
type PushFunc func(msg *protocol.Message)

// Manager handles notifications and approval requests.
type Manager struct {
	mu        sync.RWMutex
	push      PushFunc
	approvals map[types.RequestID]*ApprovalRequest
	seq       int64
}

// NewManager creates a notification manager.
func NewManager(push PushFunc) *Manager {
	return &Manager{
		push:      push,
		approvals: make(map[types.RequestID]*ApprovalRequest),
	}
}

// nextID generates a unique ID with the given prefix using an atomic counter.
func (m *Manager) nextID(prefix string) types.RequestID {
	n := atomic.AddInt64(&m.seq, 1)
	return types.RequestID(fmt.Sprintf("%s_%d", prefix, n))
}

// SendNotification broadcasts a notification to all connected clients.
func (m *Manager) SendNotification(pluginID types.PluginID, notifType, title, body string, timeout int) (*Notification, error) {
	if title == "" {
		return nil, fmt.Errorf("title is required")
	}
	if notifType == "" {
		notifType = TypeInfo
	}

	n := &Notification{
		ID:        string(m.nextID("ntf")),
		Type:      notifType,
		Title:     title,
		Body:      body,
		PluginID:  string(pluginID),
		Timeout:   timeout,
		Timestamp: time.Now().UnixMilli(),
	}

	payload, err := json.Marshal(n)
	if err != nil {
		return nil, fmt.Errorf("marshal notification: %w", err)
	}

	msg := protocol.NewNotifyRequest(
		types.RequestID(n.ID),
		pluginID,
		payload,
	)

	m.push(msg)
	return n, nil
}

// CreateApproval creates a pending approval and broadcasts the request.
func (m *Manager) CreateApproval(pluginID types.PluginID, title, body, detail string, actions []ApprovalAction, timeoutSec int) (*ApprovalRequest, error) {
	if title == "" {
		return nil, fmt.Errorf("title is required")
	}
	if timeoutSec <= 0 {
		timeoutSec = 60 // default 60 seconds
	}
	if actions == nil {
		actions = []ApprovalAction{
			{ID: "allow", Label: "Allow"},
			{ID: "deny", Label: "Deny"},
		}
	}

	now := time.Now()
	apr := &ApprovalRequest{
		RequestID: m.nextID("apr"),
		PluginID:  pluginID,
		Title:     title,
		Body:      body,
		Detail:    detail,
		Actions:   actions,
		Timeout:   timeoutSec,
		CreatedAt: now.UnixMilli(),
		ExpiresAt: now.Add(time.Duration(timeoutSec) * time.Second),
		done:      make(chan struct{}),
	}

	payload, err := json.Marshal(apr)
	if err != nil {
		return nil, fmt.Errorf("marshal approval request: %w", err)
	}

	msg := protocol.NewNotifyApprovalRequest(
		apr.RequestID,
		pluginID,
		payload,
	)

	m.mu.Lock()
	m.approvals[apr.RequestID] = apr
	m.mu.Unlock()

	m.push(msg)

	// Auto-expire goroutine.
	go func() {
		timer := time.NewTimer(time.Duration(timeoutSec) * time.Second)
		defer timer.Stop()

		select {
		case <-timer.C:
			m.mu.Lock()
			if !apr.Responded {
				apr.Responded = true
				apr.Response = &ApprovalResponse{
					RequestID:   apr.RequestID,
					Action:      "timeout",
					RespondedBy: "system",
					Timestamp:   time.Now().UnixMilli(),
				}
				close(apr.done)
			}
			m.mu.Unlock()
		case <-apr.done:
			// already responded
		}
	}()

	return apr, nil
}

// Respond handles a user's response to an approval request.
func (m *Manager) Respond(requestID types.RequestID, action, respondedBy string) (*ApprovalResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	apr, ok := m.approvals[requestID]
	if !ok {
		return nil, fmt.Errorf("approval request not found: %s", requestID)
	}
	if apr.Responded {
		return nil, fmt.Errorf("approval request already responded: %s", requestID)
	}

	apr.Responded = true
	apr.Response = &ApprovalResponse{
		RequestID:   requestID,
		Action:      action,
		RespondedBy: respondedBy,
		Timestamp:   time.Now().UnixMilli(),
	}
	close(apr.done)

	resultMsg := protocol.NewNotifyApprovalResult(
		requestID,
		action,
		respondedBy,
	)
	m.push(resultMsg)

	return apr.Response, nil
}

// WaitForResponse blocks until the approval is responded to or times out.
func (a *ApprovalRequest) WaitForResponse() *ApprovalResponse {
	<-a.done
	return a.Response
}
