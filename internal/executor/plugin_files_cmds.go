package executor

import (
	"encoding/json"
	"fmt"

	"github.com/user/sessionnode/go-core/pkg/types"
)

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
// plugin.files.register
// ---------------------------------------------------------------------------

// pluginFilesRegister registers file paths for a plugin in the PlanStore.
// These registered files are used during uninstall to report what would be
// removed, and by other lifecycle operations to track plugin artifacts.
func pluginFilesRegister(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var payload struct {
		PluginID string   `json:"pluginId"`
		Files    []string `json:"files"`
	}
	if req.Payload != nil {
		if err := json.Unmarshal(req.Payload, &payload); err != nil {
			return nil, fmt.Errorf("invalid payload: %w", err)
		}
	}

	pluginID := payload.PluginID
	if pluginID == "" {
		// Fall back to caller's plugin ID from the request envelope.
		if req.PluginID != "" {
			pluginID = req.PluginID.String()
		}
	}
	if pluginID == "" {
		return nil, fmt.Errorf("pluginId is required")
	}

	if deps.Store == nil {
		return nil, fmt.Errorf("plan store not initialized")
	}

	// Store the files, appending to any already registered for this plugin.
	deps.Store.mu.Lock()
	deps.Store.PluginFiles[pluginID] = append(deps.Store.PluginFiles[pluginID], payload.Files...)
	registered := make([]string, len(deps.Store.PluginFiles[pluginID]))
	copy(registered, deps.Store.PluginFiles[pluginID])
	deps.Store.mu.Unlock()

	return map[string]interface{}{
		"status":   "registered",
		"pluginId": pluginID,
		"files":    registered,
		"count":    len(registered),
	}, nil
}
