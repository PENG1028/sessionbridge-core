package executor

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/user/sessionnode/go-core/pkg/types"
)

const corePluginID = "sessionnode-core"

// notImplementedStub returns an ExecFunc that returns a not_implemented response.
func notImplementedStub(capability string) ExecFunc {
	return func(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
		return map[string]interface{}{
			"status":  "not_implemented",
			"message": fmt.Sprintf("%s is not implemented in Phase 1", capability),
		}, nil
	}
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

// randomPlanID generates a random hex string for use as a plan ID.
func randomPlanID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		// Fallback in case rand.Read fails (extremely unlikely)
		return fmt.Sprintf("plan_%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
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
