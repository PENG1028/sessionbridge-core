package types

// PermissionConstraints define path/key/target-node constraints for a capability grant.
type PermissionConstraints struct {
	Allow []string `json:"allow,omitempty"` // path globs
	Deny  []string `json:"deny,omitempty"`
	Keys  []string `json:"keys,omitempty"` // config key patterns

	// TargetNodes restricts which target node IDs this grant applies to.
	// If empty, all target nodes are allowed (subject to other checks).
	// Each entry can be a literal node ID or a glob pattern.
	TargetNodes []string `json:"targetNodes,omitempty"`
}
