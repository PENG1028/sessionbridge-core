package executor

import (
	"github.com/user/sessionnode/go-core/pkg/types"
)

// pluginInstallPlan generates an install plan for the given plugin.
// Phase 1: stub — returns plan-not-implemented error.
func pluginInstallPlan(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	return map[string]interface{}{
		"pluginId": req.PluginID.String(),
		"status":   "not_implemented",
		"message":  "plugin.install plan is not yet implemented in Phase 1",
	}, nil
}
