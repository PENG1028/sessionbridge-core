package executor

import (
	"github.com/user/sessionnode/go-core/pkg/types"
)

const corePluginID = "sessionnode-core"

func pluginList(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	// Phase 1: return only the core plugin itself.
	// Phase 2+: query a real plugin registry.
	return map[string]interface{}{
		"plugins": []map[string]interface{}{
			{
				"id":      corePluginID,
				"version": "0.1.0",
				"enabled": true,
			},
		},
	}, nil
}

func pluginInfo(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	return map[string]interface{}{
		"id":      corePluginID,
		"version": "0.1.0",
		"name":    "SessionNode Go Core",
		"enabled": true,
	}, nil
}
