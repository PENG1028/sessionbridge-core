package executor

import (
	"fmt"
	"time"

	"github.com/user/sessionnode/go-core/pkg/types"
)

type sessionCreatePayload struct {
	Command string `json:"command"`
	Cwd     string `json:"cwd"`
	Plugin  string `json:"pluginId"`
}

func sessionCreate(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p sessionCreatePayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	if p.Plugin == "" {
		p.Plugin = string(req.PluginID)
	}

	id := deps.Sessions.Create(
		types.PluginID(p.Plugin),
		p.Command,
		p.Cwd,
		time.Now().UnixMilli(),
	)
	return map[string]interface{}{
		"sessionId": string(id),
		"state":     "created",
	}, nil
}

type sessionDestroyPayload struct {
	SessionID string `json:"sessionId"`
}

func sessionDestroy(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p sessionDestroyPayload
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
	deps.Sessions.Destroy(types.SessionID(p.SessionID))
	return map[string]string{"status": "destroyed"}, nil
}

func sessionList(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	sessions := deps.Sessions.List()
	out := make([]map[string]interface{}, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, map[string]interface{}{
			"sessionId": string(s.ID),
			"pluginId":  string(s.PluginID),
			"state":     s.State,
			"command":   s.Command,
			"createdAt": s.CreatedAt,
		})
	}
	return map[string]interface{}{"sessions": out}, nil
}

type sessionInfoPayload struct {
	SessionID string `json:"sessionId"`
}

func sessionInfo(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var p sessionInfoPayload
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
