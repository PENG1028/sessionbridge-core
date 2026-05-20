package types

import "encoding/json"

// Actor represents the requester in a capability call.
type Actor struct {
	Type  string `json:"type"`            // "web" | "cli" | "plugin" | "node" | "relay"
	ID    string `json:"id"`              // human-readable or opaque identifier
	Token string `json:"token,omitempty"` // authentication token
}

// CapabilityRequest is the universal request envelope for all capability calls.
type CapabilityRequest struct {
	RequestID    RequestID        `json:"requestId"`
	Actor        Actor            `json:"actor"`
	PluginID     PluginID         `json:"pluginId"`
	TargetNodeID NodeID           `json:"targetNodeId,omitempty"` // empty = local node
	Capability   string           `json:"capability"`             // e.g. "fs.list", "process.spawn"
	Payload      json.RawMessage  `json:"payload,omitempty"`      // deferred deserialization
	Timestamp    int64            `json:"timestamp"`              // unix millis
	PlanID       string           `json:"planId,omitempty"`       // approved plan ID for high-risk capabilities
	ConnID       string           `json:"-"`                      // connection ID of the requesting WS client (not serialized)
}

// CapabilityResponse is the universal response envelope for all capability calls.
type CapabilityResponse struct {
	RequestID RequestID    `json:"requestId"`
	OK        bool         `json:"ok"`
	Payload   interface{}  `json:"payload,omitempty"`
	Error     *CoreError   `json:"error,omitempty"`
	PlanID    string       `json:"planId,omitempty"`   // plan ID if a plan was created
	PlanState string       `json:"planState,omitempty"` // plan state for pending plans
}

// CoreError is a structured error returned by the Core.
type CoreError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *CoreError) Error() string {
	if e.Message == "" {
		return e.Code
	}
	return e.Code + ": " + e.Message
}
