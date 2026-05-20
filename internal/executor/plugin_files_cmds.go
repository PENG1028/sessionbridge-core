package executor

import (
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
