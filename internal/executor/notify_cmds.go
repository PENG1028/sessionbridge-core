package executor

import (
	"fmt"

	"github.com/user/sessionnode/go-core/internal/notify"
	"github.com/user/sessionnode/go-core/pkg/types"
)

// notifySendPayload is the expected JSON payload for "notify.send".
type notifySendPayload struct {
	Type    string `json:"type"`
	Title   string `json:"title"`
	Body    string `json:"body,omitempty"`
	Timeout int    `json:"timeout,omitempty"`
}

// notifySend implements the "notify.send" capability.
// Sends a push notification to all connected Web UI clients.
func notifySend(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p notifySendPayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	if p.Title == "" {
		return nil, fmt.Errorf("title is required")
	}
	if p.Type == "" {
		p.Type = "info"
	}

	n, err := deps.Notifier.SendNotification(req.PluginID, p.Type, p.Title, p.Body, p.Timeout)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"notificationId": n.ID,
		"status":         "sent",
	}, nil
}

// notifyRequestPayload is the expected JSON payload for "notify.request".
type notifyRequestPayload struct {
	Title   string                  `json:"title"`
	Body    string                  `json:"body"`
	Detail  string                  `json:"detail,omitempty"`
	Actions []notify.ApprovalAction `json:"actions"`
	Timeout int                     `json:"timeout"`
	PlanID  string                  `json:"planId,omitempty"` // linked approval plan
}

// notifyRequest implements the "notify.request" capability.
// Creates a pending approval request and broadcasts it to all connected
// Web UI clients.
func notifyRequest(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p notifyRequestPayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}

	apr, err := deps.Notifier.CreateApproval(
		req.PluginID, p.Title, p.Body, p.Detail, p.Actions, p.Timeout,
	)
	if err != nil {
		return nil, err
	}
	// Link to an approval plan if provided
	if p.PlanID != "" {
		apr.PlanID = p.PlanID
	}
	return map[string]interface{}{
		"requestId": string(apr.RequestID),
		"status":    "pending",
	}, nil
}

// notifyRespondPayload is the expected JSON payload for "notify.respond".
type notifyRespondPayload struct {
	RequestID string `json:"requestId"`
	Action    string `json:"action"`
}

// notifyRespond implements the "notify.respond" capability.
// Records the user's response to a pending approval request and delivers
// the result to the requesting plugin.
func notifyRespond(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p notifyRespondPayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}

	resp, err := deps.Notifier.Respond(types.RequestID(p.RequestID), p.Action, "user")
	if err != nil {
		return nil, err
	}

	// If the approval is linked to a plan, update the plan state.
	if deps.PlanManager != nil {
		apr := deps.Notifier.GetApproval(types.RequestID(p.RequestID))
		if apr != nil && apr.PlanID != "" {
			switch p.Action {
			case "allow":
				if err := deps.PlanManager.ApprovePlan(apr.PlanID, "user"); err != nil {
					return nil, fmt.Errorf("approve plan %s: %w", apr.PlanID, err)
				}
			case "deny":
				if err := deps.PlanManager.DenyPlan(apr.PlanID, "user", "denied via notify.respond"); err != nil {
					return nil, fmt.Errorf("deny plan %s: %w", apr.PlanID, err)
				}
			}
		}
	}

	return map[string]interface{}{
		"requestId": string(resp.RequestID),
		"action":    resp.Action,
		"status":    "responded",
	}, nil
}
