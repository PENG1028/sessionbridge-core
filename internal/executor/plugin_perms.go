package executor

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/user/sessionnode/go-core/internal/config"
	"github.com/user/sessionnode/go-core/pkg/types"
)

// pluginPermissionsList returns the declared permissions from a plugin manifest,
// enriched with current grant state from config.
func pluginPermissionsList(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	pluginID := extractPluginID(req, corePluginID)
	perms := make([]map[string]interface{}, 0)

	// Load current grants from config
	grants := make(map[string]config.PermissionGrant)
	cfg := deps.Config.Get()
	if cfg.Plugin.Permissions != nil {
		if g, ok := cfg.Plugin.Permissions[pluginID]; ok {
			grants = g
		}
	}

	if deps.Manifests != nil {
		if m, err := deps.Manifests.LoadManifest(pluginID); err == nil && m != nil && m.Core != nil {
			for _, p := range m.Core.Permissions {
				entry := map[string]interface{}{
					"id":           p.ID,
					"description":  p.Description,
					"capabilities": p.Capabilities,
					"default":      p.Default,
				}
				if p.Constraints != nil {
					entry["constraints"] = p.Constraints
				}

				// Add current grant state for each declared capability
				for _, capName := range p.Capabilities {
					if g, ok := grants[capName]; ok {
						entry["grant"] = map[string]interface{}{
							"mode": g.Mode,
						}
						if g.Constraints != nil {
							entry["grant"].(map[string]interface{})["constraints"] = g.Constraints
						}
						break // use first capability match
					}
				}

				perms = append(perms, entry)
			}
		}
	}

	return map[string]interface{}{
		"pluginId":    pluginID,
		"permissions": perms,
	}, nil
}

// ---------------------------------------------------------------------------
// plugin.permissions.grant
// ---------------------------------------------------------------------------

// pluginPermissionsGrant grants a permission to a plugin.
func pluginPermissionsGrant(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	pluginID := extractPluginID(req, corePluginID)

	var payload struct {
		PluginID    string                 `json:"pluginId"`
		Capability  string                 `json:"capability"`
		Mode        string                 `json:"mode"`
		Constraints map[string]interface{} `json:"constraints,omitempty"`
	}
	if req.Payload != nil {
		if err := json.Unmarshal(req.Payload, &payload); err != nil {
			return nil, fmt.Errorf("invalid payload: %w", err)
		}
	}
	if payload.Capability == "" {
		return nil, fmt.Errorf("capability is required")
	}
	if payload.Mode == "" {
		payload.Mode = "allow"
	}
	if payload.Mode != "allow" && payload.Mode != "deny" && payload.Mode != "ask" {
		return nil, fmt.Errorf("invalid mode: %q (must be allow, deny, or ask)", payload.Mode)
	}

	// High-risk capability check
	if isHighRiskCapability(payload.Capability) {
		return map[string]interface{}{
			"status":  "requires_approval",
			"message": "High-risk operation requires approval",
		}, nil
	}

	targetID := pluginID
	if payload.PluginID != "" {
		targetID = payload.PluginID
	}

	if err := deps.Config.SetPermissionGrant(targetID, payload.Capability, payload.Mode, payload.Constraints); err != nil {
		return nil, fmt.Errorf("grant permission: %w", err)
	}

	// Record event
	if deps.History != nil {
		deps.History.RecordPluginEvent(targetID, "permission.grant", map[string]interface{}{
			"capability": payload.Capability,
			"mode":       payload.Mode,
			"actor":      req.Actor,
		})
	}

	return map[string]interface{}{
		"status":     "ok",
		"pluginId":   targetID,
		"capability": payload.Capability,
		"mode":       payload.Mode,
	}, nil
}

// isHighRiskCapability returns true if the capability string involves high-risk operations.
func isHighRiskCapability(capability string) bool {
	terms := []string{"grant", "revoke", "delete", "clear", "execute", "install", "uninstall"}
	for _, t := range terms {
		if strings.Contains(capability, t) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// plugin.permissions.revoke
// ---------------------------------------------------------------------------

// pluginPermissionsRevoke revokes a permission grant for a plugin.
func pluginPermissionsRevoke(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	pluginID := extractPluginID(req, corePluginID)

	var payload struct {
		PluginID   string `json:"pluginId"`
		Capability string `json:"capability"`
	}
	if req.Payload != nil {
		if err := json.Unmarshal(req.Payload, &payload); err != nil {
			return nil, fmt.Errorf("invalid payload: %w", err)
		}
	}
	if payload.Capability == "" {
		return nil, fmt.Errorf("capability is required")
	}

	targetID := pluginID
	if payload.PluginID != "" {
		targetID = payload.PluginID
	}

	if err := deps.Config.RemovePermissionGrant(targetID, payload.Capability); err != nil {
		return nil, fmt.Errorf("revoke permission: %w", err)
	}

	// Record event
	if deps.History != nil {
		deps.History.RecordPluginEvent(targetID, "permission.revoke", map[string]interface{}{
			"capability": payload.Capability,
			"actor":      req.Actor,
		})
	}

	return map[string]interface{}{
		"status": "ok",
	}, nil
}
