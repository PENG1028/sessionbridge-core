package executor

import (
	"fmt"

	"github.com/user/sessionnode/go-core/pkg/types"
)

// sessionGet returns detailed session info (like session.info but may include more).
// For now it's identical to session.info — can be enhanced later.
func sessionGet(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p struct {
		SessionID string `json:"sessionId"`
	}
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	if p.SessionID == "" {
		return nil, fmt.Errorf("sessionId is required")
	}
	sess := deps.Sessions.Get(types.SessionID(p.SessionID))
	if sess == nil {
		return nil, fmt.Errorf("session not found: %s", p.SessionID)
	}
	streams := make(map[string]int)
	for name, s := range sess.Streams {
		streams[name] = len(s.Buffer)
	}
	return map[string]interface{}{
		"sessionId": string(sess.ID),
		"pluginId":  string(sess.PluginID),
		"state":     sess.State,
		"command":   sess.Command,
		"cwd":       sess.Cwd,
		"createdAt": sess.CreatedAt,
		"streams":   streams,
	}, nil
}
