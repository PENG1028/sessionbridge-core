package protocol

import (
	"encoding/json"

	"github.com/user/sessionnode/go-core/pkg/types"
)

// WebSocket message type constants.
const (
	MsgTypeActionRequest        = "action.request"
	MsgTypeActionResponse       = "action.response"
	MsgTypeSessionCreate        = "session.create"
	MsgTypeSessionCreated       = "session.created"
	MsgTypeSessionEvent         = "session.event"
	MsgTypeSessionStop          = "session.stop"
	MsgTypeStreamSubscribe      = "stream.subscribe"
	MsgTypeStreamSubscribed     = "stream.subscribed"
	MsgTypeStreamChunk          = "stream.chunk"
	MsgTypeStreamWrite          = "stream.write"
	MsgTypeStreamReplay         = "stream.replay"
	MsgTypeNotifyRequest        = "notify.request"
	MsgTypeNotifyRespond        = "notify.respond"
	MsgTypeNotifyApprovalReq    = "notify.approval.request"
	MsgTypeNotifyApprovalResult = "notify.approval.result"
	MsgTypeError                = "error"
	MsgTypeHello                = "hello"
	MsgTypeWelcome              = "welcome"
	MsgTypePluginList           = "plugin.list"
	MsgTypePluginCheck          = "plugin.check"
	MsgTypePing                 = "ping"
	MsgTypePong                 = "pong"

	// Peer handshake message types.
	MsgTypePeerHello     = "peer.hello"
	MsgTypePeerChallenge = "peer.challenge"
	MsgTypePeerResponse  = "peer.response"
	MsgTypePeerWelcome   = "peer.welcome"
	MsgTypePeerError     = "peer.error"

	// Mesh call — execute a capability on a trusted peer and return the result.
	MsgTypeMeshCall   = "mesh.call"
	MsgTypeMeshResult = "mesh.result"

	// SSE events — broadcast to all web clients via /ws connections.
	MsgTypeNodeConnected    = "node.connected"
	MsgTypeNodeDisconnected = "node.disconnected"
)

// Message is the universal WebSocket message envelope.
// Type is the discriminator; all other fields are optional per message type.
type Message struct {
	Type         string           `json:"type"`
	RequestID    types.RequestID  `json:"requestId,omitempty"`
	PluginID     types.PluginID   `json:"pluginId,omitempty"`
	SessionID    types.SessionID  `json:"sessionId,omitempty"`
	StreamType   string           `json:"streamType,omitempty"`
	EventSeq     types.EventSeq   `json:"eventSeq,omitempty"`
	NodeID       types.NodeID     `json:"nodeId,omitempty"`
	Capability   string           `json:"capability,omitempty"`
	TargetNodeID types.NodeID     `json:"targetNodeId,omitempty"`
	OK           bool             `json:"ok"` // no omitempty — false must appear in error responses
	Data         string           `json:"data,omitempty"`
	Action       string           `json:"action,omitempty"`
	RespondedBy  string           `json:"respondedBy,omitempty"`
	ActorType    string           `json:"actorType,omitempty"`  // for forwarded requests: "node"
	ActorID      string           `json:"actorId,omitempty"`    // for forwarded requests: forwarding node ID
	ActorToken   string           `json:"actorToken,omitempty"` // auth token for external clients
	Payload      json.RawMessage  `json:"payload,omitempty"`
	Error        *types.CoreError `json:"error,omitempty"`
	Timestamp    int64            `json:"timestamp,omitempty"`
}

// --- Action messages ---

func NewActionRequest(req *types.CapabilityRequest) *Message {
	var payload json.RawMessage
	if req.Payload != nil {
		payload = req.Payload
	}
	return &Message{
		Type:         MsgTypeActionRequest,
		RequestID:    req.RequestID,
		PluginID:     req.PluginID,
		Capability:   req.Capability,
		TargetNodeID: req.TargetNodeID,
		Payload:      payload,
	}
}

func NewActionResponse(resp *types.CapabilityResponse) *Message {
	m := &Message{
		Type:      MsgTypeActionResponse,
		RequestID: resp.RequestID,
		OK:        resp.OK,
	}
	if resp.Error != nil {
		m.Error = resp.Error
	}
	if resp.Payload != nil {
		data, _ := json.Marshal(resp.Payload)
		m.Payload = data
	}
	return m
}

// --- Session messages ---

func NewSessionCreate(pluginID types.PluginID, targetNodeID types.NodeID, payload json.RawMessage) *Message {
	return &Message{
		Type:         MsgTypeSessionCreate,
		PluginID:     pluginID,
		TargetNodeID: targetNodeID,
		Payload:      payload,
	}
}

func NewSessionCreated(requestID types.RequestID, sessionID types.SessionID) *Message {
	return &Message{
		Type:      MsgTypeSessionCreated,
		RequestID: requestID,
		SessionID: sessionID,
	}
}

func NewSessionEvent(sessionID types.SessionID, seq types.EventSeq, eventType string, payload json.RawMessage) *Message {
	return &Message{
		Type:      MsgTypeSessionEvent,
		SessionID: sessionID,
		EventSeq:  seq,
		Data:      eventType,
		Payload:   payload,
	}
}

func NewSessionStop(requestID types.RequestID, sessionID types.SessionID) *Message {
	return &Message{
		Type:      MsgTypeSessionStop,
		RequestID: requestID,
		SessionID: sessionID,
	}
}

// --- Stream messages ---

func NewStreamSubscribe(requestID types.RequestID, sessionID types.SessionID, streamType string) *Message {
	return &Message{
		Type:       MsgTypeStreamSubscribe,
		RequestID:  requestID,
		SessionID:  sessionID,
		StreamType: streamType,
	}
}

func NewStreamSubscribed(requestID types.RequestID, sessionID types.SessionID, streamType string) *Message {
	return &Message{
		Type:       MsgTypeStreamSubscribed,
		RequestID:  requestID,
		SessionID:  sessionID,
		StreamType: streamType,
	}
}

func NewStreamChunk(sessionID types.SessionID, streamType string, seq types.EventSeq, data string) *Message {
	return &Message{
		Type:       MsgTypeStreamChunk,
		SessionID:  sessionID,
		StreamType: streamType,
		EventSeq:   seq,
		Data:       data,
	}
}

func NewStreamWrite(requestID types.RequestID, sessionID types.SessionID, data string) *Message {
	return &Message{
		Type:      MsgTypeStreamWrite,
		RequestID: requestID,
		SessionID: sessionID,
		Data:      data,
	}
}

func NewStreamReplay(requestID types.RequestID, sessionID types.SessionID, streamType string, fromSeq, toSeq types.EventSeq) *Message {
	return &Message{
		Type:       MsgTypeStreamReplay,
		RequestID:  requestID,
		SessionID:  sessionID,
		StreamType: streamType,
		EventSeq:   fromSeq,
		Timestamp:  int64(toSeq),
	}
}

// --- Notify messages ---

func NewNotifyRequest(requestID types.RequestID, pluginID types.PluginID, payload json.RawMessage) *Message {
	return &Message{
		Type:      MsgTypeNotifyRequest,
		RequestID: requestID,
		PluginID:  pluginID,
		Payload:   payload,
	}
}

func NewNotifyApprovalRequest(requestID types.RequestID, pluginID types.PluginID, payload json.RawMessage) *Message {
	return &Message{
		Type:      MsgTypeNotifyApprovalReq,
		RequestID: requestID,
		PluginID:  pluginID,
		Payload:   payload,
	}
}

func NewNotifyRespond(requestID types.RequestID, action, respondedBy string) *Message {
	return &Message{
		Type:        MsgTypeNotifyRespond,
		RequestID:   requestID,
		Action:      action,
		RespondedBy: respondedBy,
	}
}

func NewNotifyApprovalResult(requestID types.RequestID, action, respondedBy string) *Message {
	return &Message{
		Type:        MsgTypeNotifyApprovalResult,
		RequestID:   requestID,
		Action:      action,
		RespondedBy: respondedBy,
	}
}

// --- Error / Connection messages ---

func NewError(requestID types.RequestID, code, message string) *Message {
	return &Message{
		Type:      MsgTypeError,
		RequestID: requestID,
		OK:        false,
		Error: &types.CoreError{
			Code:    code,
			Message: message,
		},
	}
}

func NewHello(nodeID types.NodeID, version string) *Message {
	return &Message{
		Type:   MsgTypeHello,
		NodeID: nodeID,
		Data:   version,
	}
}

func NewWelcome(nodeID types.NodeID) *Message {
	return &Message{
		Type:   MsgTypeWelcome,
		NodeID: nodeID,
	}
}

func NewPing() *Message {
	return &Message{Type: MsgTypePing}
}

func NewPong() *Message {
	return &Message{Type: MsgTypePong}
}

// --- Peer handshake messages ---

// peerHelloPayload is the payload for peer.hello messages.
type peerHelloPayload struct {
	PublicKey   string `json:"publicKey"`   // base64
	Fingerprint string `json:"fingerprint"` // hex
	Timestamp   int64  `json:"timestamp"`
}

// peerChallengePayload is the payload for peer.challenge messages.
type peerChallengePayload struct {
	Nonce string `json:"nonce"` // base64
}

// peerResponsePayload is the payload for peer.response messages.
type peerResponsePayload struct {
	Signature string `json:"signature"` // base64
}

// NewPeerHello creates a peer.hello message for the peer handshake.
func NewPeerHello(nodeID types.NodeID, publicKeyBase64, fingerprint string, timestamp int64) *Message {
	payload, _ := json.Marshal(peerHelloPayload{
		PublicKey:   publicKeyBase64,
		Fingerprint: fingerprint,
		Timestamp:   timestamp,
	})
	return &Message{
		Type:      MsgTypePeerHello,
		NodeID:    nodeID,
		Payload:   payload,
		Timestamp: timestamp,
	}
}

// NewPeerChallenge creates a peer.challenge message with a random nonce.
func NewPeerChallenge(requestID types.RequestID, nonceBase64 string) *Message {
	payload, _ := json.Marshal(peerChallengePayload{Nonce: nonceBase64})
	return &Message{
		Type:      MsgTypePeerChallenge,
		RequestID: requestID,
		Payload:   payload,
	}
}

// NewPeerResponse creates a peer.response message with a signature.
func NewPeerResponse(requestID types.RequestID, signatureBase64 string) *Message {
	payload, _ := json.Marshal(peerResponsePayload{Signature: signatureBase64})
	return &Message{
		Type:      MsgTypePeerResponse,
		RequestID: requestID,
		Payload:   payload,
	}
}

// NewPeerWelcome creates a peer.welcome message.
func NewPeerWelcome(nodeID types.NodeID) *Message {
	return &Message{
		Type:   MsgTypePeerWelcome,
		NodeID: nodeID,
	}
}

// NewPeerError creates a peer.error message.
func NewPeerError(requestID types.RequestID, code, message string) *Message {
	return &Message{
		Type:      MsgTypePeerError,
		RequestID: requestID,
		OK:        false,
		Error:     &types.CoreError{Code: code, Message: message},
	}
}

// --- Mesh call messages ---

// NewMeshCall creates a mesh.call message that instructs a trusted peer to
// execute a capability and return the result via mesh.result.
func NewMeshCall(req *types.CapabilityRequest) *Message {
	var payload json.RawMessage
	if req.Payload != nil {
		payload = req.Payload
	}
	return &Message{
		Type:         MsgTypeMeshCall,
		RequestID:    req.RequestID,
		PluginID:     req.PluginID,
		Capability:   req.Capability,
		TargetNodeID: req.TargetNodeID,
		Payload:      payload,
		ActorType:    "node",
		ActorID:      req.Actor.ID,
	}
}

// NewNodeConnected creates a node.connected SSE event for web clients.
func NewNodeConnected(nodeID types.NodeID) *Message {
	return &Message{
		Type:   MsgTypeNodeConnected,
		NodeID: nodeID,
	}
}

// NewNodeDisconnected creates a node.disconnected SSE event for web clients.
func NewNodeDisconnected(nodeID types.NodeID) *Message {
	return &Message{
		Type:   MsgTypeNodeDisconnected,
		NodeID: nodeID,
	}
}

// NewMeshResult creates a mesh.result message carrying the response from a
// mesh.call execution.
func NewMeshResult(resp *types.CapabilityResponse) *Message {
	m := &Message{
		Type:      MsgTypeMeshResult,
		RequestID: resp.RequestID,
		OK:        resp.OK,
	}
	if resp.Error != nil {
		m.Error = resp.Error
	}
	if resp.Payload != nil {
		data, _ := json.Marshal(resp.Payload)
		m.Payload = data
	}
	return m
}
