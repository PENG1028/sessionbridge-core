package executor

import (
	"encoding/json"
	"time"

	"github.com/user/sessionnode/go-core/pkg/types"
)

const corePluginID = "sessionnode-core"

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

func pluginInfo(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	pluginID := extractPluginID(req, corePluginID)

	info := map[string]interface{}{
		"id":      pluginID,
		"version": "0.1.0",
		"name":    "SessionNode Go Core",
		"enabled": true,
	}

	// Enrich with manifest data if available.
	if deps.Manifests != nil {
		if m, err := deps.Manifests.LoadManifest(pluginID); err == nil && m != nil {
			info["name"] = m.Name
			info["description"] = m.Description
			info["trusted"] = m.Trusted
		}
	}

	return info, nil
}

// pluginCheck checks whether a plugin's environment dependencies are satisfied.
// Uses the manifest's environment checks when available.
func pluginCheck(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	pluginID := extractPluginID(req, "sessionnode-core")

	result := map[string]interface{}{
		"pluginId":     pluginID,
		"status":       "ok",
		"checkedAt":    nowMillis(),
		"dependencies": []map[string]interface{}{},
	}

	if deps.Manifests == nil {
		return result, nil
	}

	m, err := deps.Manifests.LoadManifest(pluginID)
	if err != nil || m == nil || m.Core == nil {
		return result, nil
	}

	depsList := make([]map[string]interface{}, 0)
	allOk := true

	for _, check := range m.Core.Environment.Checks {
		dep := map[string]interface{}{
			"id":     check.ID,
			"type":   check.Type,
			"status": "skipped",
		}
		if check.Command != "" {
			dep["command"] = check.Command
		}
		if check.Required {
			dep["required"] = true
		}

		switch check.Type {
		case "binary":
			// Phase 2+: run actual command lookup
			dep["status"] = "pending"
		case "command":
			dep["status"] = "pending"
		case "env":
			dep["status"] = "pending"
		default:
			dep["status"] = "pending"
		}

		if dep["status"] != "ok" && check.Required {
			allOk = false
		}
		depsList = append(depsList, dep)
	}

	status := "ok"
	if !allOk {
		status = "incomplete"
	}
	result["status"] = status
	result["dependencies"] = depsList

	return result, nil
}

// pluginInstallPlan generates an install plan for the given plugin.
// Phase 1: stub — returns plan-not-implemented error.
func pluginInstallPlan(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	return map[string]interface{}{
		"pluginId": req.PluginID.String(),
		"status":   "not_implemented",
		"message":  "plugin.install plan is not yet implemented in Phase 1",
	}, nil
}

// pluginCacheList lists cache entries for a plugin based on manifest declarations.
func pluginCacheList(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	pluginID := extractPluginID(req, corePluginID)
	caches := make([]map[string]interface{}, 0)

	if deps.Manifests != nil {
		m, err := deps.Manifests.LoadManifest(pluginID)
		if err == nil && m != nil && m.Core != nil {
			for _, decl := range m.Core.Files.Declarations {
				if decl.Clearable {
					caches = append(caches, map[string]interface{}{
						"id":          decl.ID,
						"path":        decl.Path,
						"description": decl.Description,
						"risk":        decl.Risk,
					})
				}
			}
		}
	}

	return map[string]interface{}{
		"pluginId": pluginID,
		"caches":   caches,
	}, nil
}

// pluginCacheClear clears the cache entries for a plugin.
// Phase 1: stub — returns not-implemented.
func pluginCacheClear(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	return map[string]interface{}{
		"pluginId": req.PluginID.String(),
		"status":   "not_implemented",
		"message":  "plugin.cache.clear is not yet implemented in Phase 1",
	}, nil
}

// pluginFilesList lists declared file locations for a plugin.
func pluginFilesList(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	pluginID := extractPluginID(req, corePluginID)
	files := make([]map[string]interface{}, 0)

	if deps.Manifests != nil {
		m, err := deps.Manifests.LoadManifest(pluginID)
		if err == nil && m != nil && m.Core != nil {
			fs := m.Core.Files
			if fs.ConfigDir != "" {
				files = append(files, map[string]interface{}{"id": "config", "path": fs.ConfigDir, "purpose": "configuration"})
			}
			if fs.DataDir != "" {
				files = append(files, map[string]interface{}{"id": "data", "path": fs.DataDir, "purpose": "data"})
			}
			if fs.CacheDir != "" {
				files = append(files, map[string]interface{}{"id": "cache", "path": fs.CacheDir, "purpose": "cache"})
			}
			if fs.LogsDir != "" {
				files = append(files, map[string]interface{}{"id": "logs", "path": fs.LogsDir, "purpose": "logs"})
			}
			for _, decl := range fs.Declarations {
				files = append(files, map[string]interface{}{
					"id":          decl.ID,
					"path":        decl.Path,
					"description": decl.Description,
					"clearable":   decl.Clearable,
				})
			}
		}
	}

	return map[string]interface{}{
		"pluginId": pluginID,
		"files":    files,
	}, nil
}

// extractPluginID extracts a plugin ID from the request payload or falls back to the caller's PluginID.
// Most plugin management capabilities target a specific plugin via a "pluginId" payload field.
func extractPluginID(req *types.CapabilityRequest, fallback string) string {
	if req.Payload != nil {
		var payload struct {
			PluginID string `json:"pluginId"`
		}
		if err := json.Unmarshal(req.Payload, &payload); err == nil && payload.PluginID != "" {
			return payload.PluginID
		}
	}
	if req.PluginID != "" {
		return req.PluginID.String()
	}
	return fallback
}

// nowMillis returns the current Unix timestamp in milliseconds.
func nowMillis() int64 {
	return time.Now().UnixMilli()
}
