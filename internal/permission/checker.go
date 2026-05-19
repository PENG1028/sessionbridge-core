package permission

import (
	"encoding/json"

	"github.com/user/sessionnode/go-core/pkg/protocol"
	"github.com/user/sessionnode/go-core/pkg/types"
)

// PermissionGrant is the effective runtime grant for a capability.
type PermissionGrant struct {
	Mode        string                       `json:"mode"`     // "allow" | "deny" | "ask"
	Constraints *types.PermissionConstraints `json:"constraints,omitempty"`
	GrantedAt   int64                        `json:"grantedAt"`
	GrantedBy   string                       `json:"grantedBy"`
	ExpiresAt   *int64                       `json:"expiresAt,omitempty"`
}

// PluginCapRegistry checks whether a plugin has declared a capability.
type PluginCapRegistry interface {
	HasCapability(pluginID types.PluginID, capability string) bool
}

// PolicyStore provides the effective grant for a plugin capability.
type PolicyStore interface {
	GetGrant(pluginID types.PluginID, capability string) (*PermissionGrant, error)
}

// PluginPermissionError is a structured error for permission failures.
type PluginPermissionError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *PluginPermissionError) Error() string {
	if e.Message == "" {
		return e.Code
	}
	return e.Code + ": " + e.Message
}

// Checker implements the 4-step permission check:
// capability declared? → grant exists? → mode check → constraints check.
type Checker struct {
	registry PluginCapRegistry
	policy   PolicyStore
}

// NewChecker creates a Checker with the given registry and policy store.
func NewChecker(registry PluginCapRegistry, policy PolicyStore) *Checker {
	return &Checker{
		registry: registry,
		policy:   policy,
	}
}

// Check runs the permission check for a capability request.
func (c *Checker) Check(req *types.CapabilityRequest) error {
	// Step 1: Does the plugin declare this capability?
	if !c.registry.HasCapability(req.PluginID, req.Capability) {
		return &PluginPermissionError{
			Code:    protocol.ErrCodeCapNotDeclared,
			Message: "plugin " + string(req.PluginID) + " does not declare capability " + req.Capability,
		}
	}

	// Step 2: Is there a grant?
	grant, err := c.policy.GetGrant(req.PluginID, req.Capability)
	if err != nil {
		return &PluginPermissionError{
			Code:    protocol.ErrCodeNotGranted,
			Message: "no grant found for " + string(req.PluginID) + "." + req.Capability,
		}
	}

	// Step 3: Check grant mode
	switch grant.Mode {
	case "deny":
		return &PluginPermissionError{Code: protocol.ErrCodePermissionDenied, Message: "permission denied by policy"}
	case "ask":
		return &PluginPermissionError{Code: protocol.ErrCodeNeedApproval, Message: "approval required"}
	case "allow":
		// continue
	default:
		return &PluginPermissionError{Code: protocol.ErrCodePermissionDenied, Message: "unknown grant mode: " + grant.Mode}
	}

	// Step 4: Check constraints
	if grant.Constraints != nil {
		if err := c.checkConstraints(grant.Constraints, req); err != nil {
			return err
		}
	}

	return nil
}

// checkConstraints validates path/key constraints for the request.
// Phase 0: basic prefix matching. Phase 1+: full glob support.
func (c *Checker) checkConstraints(cons *types.PermissionConstraints, req *types.CapabilityRequest) error {
	// TODO: Phase 1 — full glob matching with **, *, ${workspace} resolution.
	// Phase 0: if Allow is non-empty, require at least one prefix match
	// of the payload path against the allow list.
	if cons.Allow != nil {
		// Extract path from payload — in Phase 0, this is a stub.
		// Real implementation will parse the payload struct per capability.
		path := extractPath(req.Payload)
		if path != "" && !matchesAny(path, cons.Allow) {
			return &PluginPermissionError{Code: protocol.ErrCodePathNotAllowed, Message: "path not in allow list: " + path}
		}
	}
	return nil
}

func matchesAny(s string, patterns []string) bool {
	for _, p := range patterns {
		if simpleMatch(p, s) {
			return true
		}
	}
	return false
}

// simpleMatch is a placeholder for real glob matching.
// Phase 0: exact string match and ** prefix/suffix only.
func simpleMatch(pattern, s string) bool {
	if pattern == s {
		return true
	}
	if pattern == "**" {
		return true
	}
	// **/ prefix
	if len(pattern) > 3 && pattern[:3] == "**/" && len(s) >= len(pattern)-3 {
		return s[len(s)-len(pattern)+3:] == pattern[3:]
	}
	// /** suffix
	if len(pattern) > 3 && pattern[len(pattern)-3:] == "/**" && len(s) >= len(pattern)-3 {
		return s[:len(pattern)-3] == pattern[:len(pattern)-3]
	}
	return false
}

// extractPath extracts a "path" field from a JSON payload.
// Returns empty string if payload is nil or path is missing.
func extractPath(payload []byte) string {
	if len(payload) == 0 {
		return ""
	}
	var m map[string]interface{}
	if err := json.Unmarshal(payload, &m); err != nil {
		return ""
	}
	if p, ok := m["path"]; ok {
		if s, ok := p.(string); ok {
			return s
		}
	}
	return ""
}
