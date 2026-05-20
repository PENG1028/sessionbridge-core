package executor

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/user/sessionnode/go-core/pkg/types"
)

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
