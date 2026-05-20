package executor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/user/sessionnode/go-core/internal/config"
	"github.com/user/sessionnode/go-core/internal/pluginmanifest"
	"github.com/user/sessionnode/go-core/pkg/types"
)

const corePluginID = "sessionnode-core"

// SupportedManifestVersion is the manifest format version supported.
const SupportedManifestVersion = "1"

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

// buildPluginDetail assembles the full plugin detail from manifest data.
func buildPluginDetail(pluginID string, deps *Deps) map[string]interface{} {
	info := map[string]interface{}{
		"id":             pluginID,
		"pluginId":       pluginID,
		"version":        "0.1.0",
		"name":           "SessionNode Go Core",
		"description":    "",
		"enabled":        true,
		"trusted":        false,
		"manifestVersion": SupportedManifestVersion,
	}

	if deps.Manifests != nil {
		m, err := deps.Manifests.LoadManifest(pluginID)
		if err == nil && m != nil {
			info["name"] = m.Name
			info["description"] = m.Description
			info["trusted"] = m.Trusted
			info["manifestVersion"] = m.ManifestVersion
			info["version"] = m.Version

			// Core section — permissions, environment, files, tasks, history
			if m.Core != nil {
				info["core"] = buildCoreSection(m.Core)
			}

			// Adapters section — system-ui, cli
			info["adapters"] = buildAdaptersSection(&m.Adapters)
		}
	}

	return info
}

// buildCoreSection serializes the CoreSpec for frontend consumption.
func buildCoreSection(core *pluginmanifest.CoreSpec) map[string]interface{} { //nolint:govet // intentional type alias
	if core == nil {
		return nil
	}
	out := map[string]interface{}{
		"permissions":  core.Permissions,
		"environment":  core.Environment,
		"files":        core.Files,
		"tasks":        core.Tasks,
		"history":      core.History,
	}
	return out
}

// buildAdaptersSection serializes the AdapterSpec for frontend consumption.
func buildAdaptersSection(adapters *pluginmanifest.AdapterSpec) map[string]interface{} { //nolint:govet // intentional type alias
	if adapters == nil {
		return nil
	}
	out := make(map[string]interface{})
	if adapters.SystemUI != nil {
		sysUI := make(map[string]interface{})
		sysUI["views"] = adapters.SystemUI.Views
		sysUI["panels"] = adapters.SystemUI.Panels
		sysUI["commands"] = adapters.SystemUI.Commands
		sysUI["status"] = adapters.SystemUI.Status
		out["system-ui"] = sysUI
	}
	if adapters.CLI != nil {
		out["cli"] = adapters.CLI
	}
	return out
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
			"actor":   req.Actor,
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
			"actor":   req.Actor,
			"requestId": req.RequestID,
		})
	}

	return map[string]interface{}{
		"pluginId": pluginID,
		"status":   "disabled",
	}, nil
}

// pluginCheck checks whether a plugin's environment dependencies are satisfied.
// Supports check types: binary, env, command, path, file, directory.
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
		if check.VersionCommand != "" {
			dep["versionCommand"] = check.VersionCommand
		}
		if check.RequiredVersion != "" {
			dep["requiredVersion"] = check.RequiredVersion
		}
		if check.InstallHint != "" {
			dep["installHint"] = check.InstallHint
		}

		switch check.Type {
		case "binary":
			dep["status"] = checkBinary(check.Command)
		case "env":
			dep["status"] = checkEnv(check.Command)
		case "command":
			dep["status"] = checkCommand(check.Command, check.Args)
		case "path":
			dep["status"] = checkPath(check.Command, false)
		case "file":
			dep["status"] = checkFile(check.Command)
		case "directory":
			dep["status"] = checkPath(check.Command, true)
		default:
			dep["status"] = "unknown"
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

// checkBinary reports whether a binary is found on PATH.
func checkBinary(name string) string {
	if name == "" {
		return "skipped"
	}
	_, err := exec.LookPath(name)
	if err != nil {
		return "missing"
	}
	return "ok"
}

// checkEnv reports whether an environment variable is set and non-empty.
func checkEnv(name string) string {
	if name == "" {
		return "skipped"
	}
	if os.Getenv(name) == "" {
		return "missing"
	}
	return "ok"
}

// checkCommand runs a command with a 5-second timeout.
// Returns "ok" on exit code 0, "missing" on any error.
func checkCommand(cmd, args string) string {
	if cmd == "" {
		return "skipped"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c := exec.CommandContext(ctx, cmd, strings.Fields(args)...)
	if err := c.Run(); err != nil {
		return "missing"
	}
	return "ok"
}

// checkPath reports whether a filesystem path exists, optionally requiring it to be a directory.
func checkPath(path string, dir bool) string {
	if path == "" {
		return "skipped"
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "missing"
		}
		return "error"
	}
	if dir && !info.IsDir() {
		return "type_mismatch"
	}
	if !dir && info.IsDir() {
		// path type accepts both files and directories
	}
	return "ok"
}

// checkFile reports whether a file exists and is not a directory.
func checkFile(path string) string {
	if path == "" {
		return "skipped"
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "missing"
		}
		return "error"
	}
	if info.IsDir() {
		return "type_mismatch"
	}
	return "ok"
}

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
					"id":              p.ID,
					"description":     p.Description,
					"capabilities":    p.Capabilities,
					"default":         p.Default,
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

// ---------------------------------------------------------------------------
// notImplementedStub helpers
// ---------------------------------------------------------------------------

// notImplementedStub returns an ExecFunc that returns a not_implemented response.
func notImplementedStub(capability string) ExecFunc {
	return func(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
		return map[string]interface{}{
			"status":  "not_implemented",
			"message": fmt.Sprintf("%s is not implemented in Phase 1", capability),
		}, nil
	}
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

// ---------------------------------------------------------------------------
// plugin.cache.info
// ---------------------------------------------------------------------------

// pluginCacheInfo returns detailed information about cache entries from manifest declarations.
func pluginCacheInfo(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	pluginID := extractPluginID(req, corePluginID)
	caches := make([]map[string]interface{}, 0)

	if deps.Manifests != nil {
		m, err := deps.Manifests.LoadManifest(pluginID)
		if err == nil && m != nil && m.Core != nil {
			for _, decl := range m.Core.Files.Declarations {
				if decl.Clearable {
					entry := map[string]interface{}{
						"id":          decl.ID,
						"path":        decl.Path,
						"description": decl.Description,
						"clearable":   decl.Clearable,
						"external":    decl.External,
						"risk":        decl.Risk,
					}
					caches = append(caches, entry)
				}
			}
		}
	}

	return map[string]interface{}{
		"pluginId": pluginID,
		"caches":   caches,
	}, nil
}

// ---------------------------------------------------------------------------
// plugin.cache.clear.plan
// ---------------------------------------------------------------------------

// pluginCacheClearPlan creates a clear plan for a specific cache entry.
func pluginCacheClearPlan(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	pluginID := extractPluginID(req, corePluginID)

	var payload struct {
		PluginID string `json:"pluginId"`
		CacheID  string `json:"cacheId"`
	}
	if req.Payload != nil {
		if err := json.Unmarshal(req.Payload, &payload); err != nil {
			return nil, fmt.Errorf("invalid payload: %w", err)
		}
	}
	if payload.CacheID == "" {
		return nil, fmt.Errorf("cacheId is required")
	}

	targetID := pluginID
	if payload.PluginID != "" {
		targetID = payload.PluginID
	}

	// Find the cache declaration in the manifest
	if deps.Manifests != nil {
		m, err := deps.Manifests.LoadManifest(targetID)
		if err == nil && m != nil && m.Core != nil {
			for _, decl := range m.Core.Files.Declarations {
				if decl.ID == payload.CacheID && decl.Clearable {
					planID := randomPlanID()
					paths := []string{decl.Path}
					risk := decl.Risk
					if risk == "" {
						risk = "low"
					}
					return map[string]interface{}{
						"cacheId":       decl.ID,
						"paths":         paths,
						"risk":          risk,
						"estimatedSize": "unknown",
						"planId":        planID,
					}, nil
				}
			}
		}
	}

	return nil, fmt.Errorf("clearable cache %q not found for plugin %q", payload.CacheID, targetID)
}

// ---------------------------------------------------------------------------
// plugin.cache.clear.execute
// ---------------------------------------------------------------------------

// pluginCacheClearExecute executes a clear plan for a specific cache entry.
func pluginCacheClearExecute(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	pluginID := extractPluginID(req, corePluginID)

	var payload struct {
		PluginID string `json:"pluginId"`
		CacheID  string `json:"cacheId"`
		PlanID   string `json:"planId"`
	}
	if req.Payload != nil {
		if err := json.Unmarshal(req.Payload, &payload); err != nil {
			return nil, fmt.Errorf("invalid payload: %w", err)
		}
	}

	if payload.PlanID == "" {
		return map[string]interface{}{
			"status":  "plan_required",
			"message": "planId is required for cache.clear.execute; call plugin.cache.clear.plan first",
		}, nil
	}
	if payload.CacheID == "" {
		return nil, fmt.Errorf("cacheId is required")
	}

	targetID := pluginID
	if payload.PluginID != "" {
		targetID = payload.PluginID
	}

	// Find the cache declaration and delete the path
	if deps.Manifests != nil {
		m, err := deps.Manifests.LoadManifest(targetID)
		if err == nil && m != nil && m.Core != nil {
			for _, decl := range m.Core.Files.Declarations {
				if decl.ID == payload.CacheID && decl.Clearable && decl.Path != "" {
					if err := os.RemoveAll(decl.Path); err != nil {
						return nil, fmt.Errorf("clear cache: %w", err)
					}

					// Record event
					if deps.History != nil {
						deps.History.RecordPluginEvent(targetID, "cache.clear", map[string]interface{}{
							"cacheId": decl.ID,
							"path":    decl.Path,
							"actor":   req.Actor,
						})
					}

					return map[string]interface{}{
						"status":  "ok",
						"cacheId": decl.ID,
						"deleted": true,
					}, nil
				}
			}
		}
	}

	return nil, fmt.Errorf("clearable cache %q not found for plugin %q", payload.CacheID, targetID)
}

// randomPlanID generates a random hex string for use as a plan ID.
func randomPlanID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// Fallback in case rand.Read fails (extremely unlikely)
		return fmt.Sprintf("plan_%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
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
