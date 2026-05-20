package executor

import (
	"encoding/json"
	"fmt"

	"github.com/user/sessionnode/go-core/internal/config"
	"github.com/user/sessionnode/go-core/pkg/types"
)

// pluginConfigGet returns configuration values for a plugin from the config manager.
func pluginConfigGet(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	pluginID := extractPluginID(req, corePluginID)
	cfg := deps.Config.Get()
	values := make(map[string]interface{})

	if cfg.Plugin.Permissions != nil {
		if grants, ok := cfg.Plugin.Permissions[pluginID]; ok {
			for k, v := range grants {
				values[k] = v
			}
		}
	}

	return map[string]interface{}{
		"pluginId": pluginID,
		"config":   values,
		"revision": cfg.Revision,
	}, nil
}

// pluginConfigSchema returns the configuration schema from a plugin manifest if declared.
func pluginConfigSchema(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	pluginID := extractPluginID(req, corePluginID)
	schema := map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	}

	if deps.Manifests != nil {
		if m, err := deps.Manifests.LoadManifest(pluginID); err == nil && m != nil {
			// Check for system-ui settings adapter
			if m.Adapters.SystemUI != nil && m.Adapters.SystemUI.Settings != nil {
				s := m.Adapters.SystemUI.Settings
				schema["schema"] = s.Schema
				if s.Properties != nil {
					schema["properties"] = s.Properties
				}
			}
		}
	}

	return map[string]interface{}{
		"pluginId": pluginID,
		"schema":   schema,
	}, nil
}

// ---------------------------------------------------------------------------
// plugin.config.set
// ---------------------------------------------------------------------------

// pluginConfigSet sets a plugin configuration value.
func pluginConfigSet(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	pluginID := extractPluginID(req, corePluginID)

	var payload struct {
		Key              string      `json:"key"`
		Value            interface{} `json:"value"`
		ExpectedRevision int64       `json:"expectedRevision,omitempty"`
	}
	if req.Payload != nil {
		if err := json.Unmarshal(req.Payload, &payload); err != nil {
			return nil, fmt.Errorf("invalid payload: %w", err)
		}
	}
	if payload.Key == "" {
		return nil, fmt.Errorf("key is required")
	}

	key := "plugin." + payload.Key // scope to plugin config namespace

	if payload.ExpectedRevision != 0 {
		err := deps.Config.SetWithRevision(key, payload.Value, payload.ExpectedRevision)
		if err != nil {
			if conflictErr, ok := err.(*config.ConfigConflictError); ok {
				return map[string]interface{}{
					"status":           "conflict",
					"expectedRevision": conflictErr.ExpectedRevision,
					"actualRevision":   conflictErr.ActualRevision,
				}, nil
			}

			return nil, fmt.Errorf("config set with revision: %w", err)
		}
	} else {
		if err := deps.Config.Set(key, payload.Value); err != nil {
			return nil, fmt.Errorf("config set: %w", err)
		}
	}

	// Broadcast notification
	if deps.Notifier != nil {
		deps.Notifier.SendNotification(types.PluginID(corePluginID), "info",
			"Config Changed", "Config key "+payload.Key+" was updated", 5)
	}

	// Record event
	if deps.History != nil {
		deps.History.RecordPluginEvent(pluginID, "config.set", map[string]interface{}{
			"key":      payload.Key,
			"revision": deps.Config.Get().Revision,
			"actor":    req.Actor,
		})
	}

	return map[string]interface{}{
		"status":   "ok",
		"revision": deps.Config.Get().Revision,
	}, nil
}
