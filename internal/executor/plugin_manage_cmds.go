package executor

import (
	"fmt"

	"github.com/user/sessionnode/go-core/pkg/types"
)

func pluginList(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	plugins := make([]map[string]interface{}, 0)

	// Always include the built-in core plugin.
	plugins = append(plugins, map[string]interface{}{
		"pluginId": corePluginID,
		"version":  "0.1.0",
		"status":   "enabled",
		"type":     "builtin",
	})

	// Enrich with manifest-discovered plugins.
	if deps.Manifests != nil {
		for _, s := range deps.Manifests.ListPlugins() {
			status := "enabled"
			if s.Error != "" {
				status = "error"
			} else if !s.Enabled {
				status = "disabled"
			}

			entry := map[string]interface{}{
				"pluginId":    s.ID,
				"version":     s.Version,
				"status":      status,
				"type":        "feature",
				"description": s.Description,
			}
			if s.Error != "" {
				entry["error"] = s.Error
			}
			plugins = append(plugins, entry)
		}
	}

	return plugins, nil
}

// pluginInfo returns plugin info enriched with manifest data.
func pluginInfo(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	pluginID := extractPluginID(req, corePluginID)
	return buildPluginDetail(pluginID, deps), nil
}

// pluginGet returns the full plugin manifest data.
func pluginGet(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	pluginID := extractPluginID(req, corePluginID)
	detail := buildPluginDetail(pluginID, deps)
	if m, err := deps.Manifests.LoadManifest(pluginID); err == nil && m != nil {
		detail["manifestVersion"] = m.ManifestVersion
	}
	return detail, nil
}

// pluginStatus returns the current status of a plugin.
func pluginStatus(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	pluginID := extractPluginID(req, corePluginID)
	enabled := true
	var errStr string

	if deps.Manifests != nil {
		enabled = deps.Manifests.PluginEnabled(pluginID)
		for _, s := range deps.Manifests.ListPlugins() {
			if s.ID == pluginID && s.Error != "" {
				errStr = s.Error
			}
		}
	}

	status := "enabled"
	if errStr != "" {
		status = "error"
	} else if !enabled {
		status = "disabled"
	}

	return map[string]interface{}{
		"pluginId": pluginID,
		"status":   status,
		"enabled":  enabled,
		"error":    errStr,
	}, nil
}

// pluginEnable enables a plugin by removing it from the DisabledPlugins list.
func pluginEnable(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	pluginID := extractPluginID(req, corePluginID)

	cfg := deps.Config.Get()
	newDisabled := make([]string, 0, len(cfg.Plugin.DisabledPlugins))
	removed := false
	for _, id := range cfg.Plugin.DisabledPlugins {
		if id == pluginID {
			removed = true
		} else {
			newDisabled = append(newDisabled, id)
		}
	}
	if !removed {
		return map[string]interface{}{
			"pluginId": pluginID,
			"status":   "already_enabled",
		}, nil
	}

	if err := deps.Config.Set("plugin.disabledPlugins", newDisabled); err != nil {
		return nil, fmt.Errorf("enable plugin: %w", err)
	}

	// Broadcast notification
	if deps.Notifier != nil {
		deps.Notifier.SendNotification(types.PluginID(corePluginID), "info",
			"Plugin Enabled", "Plugin "+pluginID+" has been enabled", 5)
	}

	// Record plugin event
	if deps.History != nil {
		deps.History.RecordPluginEvent(pluginID, "enabled", map[string]interface{}{
			"actor":     req.Actor,
			"requestId": req.RequestID,
		})
	}

	return map[string]interface{}{
		"pluginId": pluginID,
		"status":   "enabled",
	}, nil
}

// pluginDisable disables a plugin by adding it to the DisabledPlugins list.
func pluginDisable(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	pluginID := extractPluginID(req, corePluginID)

	// Reject disabling the core plugin
	if pluginID == corePluginID {
		return nil, fmt.Errorf("cannot disable built-in plugin: %s", corePluginID)
	}

	if deps.Manifests != nil {
		if !deps.Manifests.PluginEnabled(pluginID) {
			return map[string]interface{}{
				"pluginId": pluginID,
				"status":   "already_disabled",
			}, nil
		}
	}

	cfg := deps.Config.Get()
	for _, id := range cfg.Plugin.DisabledPlugins {
		if id == pluginID {
			return map[string]interface{}{
				"pluginId": pluginID,
				"status":   "already_disabled",
			}, nil
		}
	}

	newDisabled := append(cfg.Plugin.DisabledPlugins, pluginID)
	if err := deps.Config.Set("plugin.disabledPlugins", newDisabled); err != nil {
		return nil, fmt.Errorf("disable plugin: %w", err)
	}

	// Broadcast notification
	if deps.Notifier != nil {
		deps.Notifier.SendNotification(types.PluginID(corePluginID), "warn",
			"Plugin Disabled", "Plugin "+pluginID+" has been disabled", 5)
	}

	// Record plugin event
	if deps.History != nil {
		deps.History.RecordPluginEvent(pluginID, "disabled", map[string]interface{}{
			"actor":     req.Actor,
			"requestId": req.RequestID,
		})
	}

	return map[string]interface{}{
		"pluginId": pluginID,
		"status":   "disabled",
	}, nil
}

// pluginHistory returns the install/upgrade/management history for a plugin.
// Phase 1: returns real plugin events if deps.History supports them.
func pluginHistory(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	pluginID := extractPluginID(req, corePluginID)

	if deps.History != nil {
		events := deps.History.QueryPluginEvents(pluginID)
		eventMaps := make([]map[string]interface{}, 0, len(events))
		for _, evt := range events {
			eventMaps = append(eventMaps, map[string]interface{}{
				"eventType": evt.EventType,
				"data":      evt.Data,
				"timestamp": evt.Timestamp,
			})
		}
		return map[string]interface{}{
			"pluginId": pluginID,
			"events":   eventMaps,
			"status":   "ok",
		}, nil
	}

	// deps.History not initialized — treat as not_implemented
	return map[string]interface{}{
		"pluginId": pluginID,
		"events":   []map[string]interface{}{},
		"status":   "not_implemented",
		"message":  "plugin.history is not available",
	}, nil
}
