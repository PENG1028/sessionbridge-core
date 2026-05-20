package types

// PluginDefinition is the canonical plugin declaration from plugin.yaml.
type PluginDefinition struct {
	ID          PluginID              `json:"id"`
	Title       string                `json:"title"`
	Version     string                `json:"version"`
	Kind        string                `json:"kind"` // "web" | "cli" | "web+cli" | "headless"
	Description string                `json:"description,omitempty"`
	Author      string                `json:"author,omitempty"`
	Homepage    string                `json:"homepage,omitempty"`
	Requires    struct {
		Capabilities []string           `json:"capabilities"`
		Dependencies []PluginDependency `json:"dependencies"`
	} `json:"requires"`
	Permissions []PluginPermissionDecl `json:"permissions"`
	Files       []PluginFileDecl       `json:"files,omitempty"`
	Caches      []PluginCacheDecl      `json:"caches,omitempty"`
	Web         *WebManifest           `json:"web,omitempty"`
	CLI         *CLIManifest           `json:"cli,omitempty"`
}

// PluginDependency describes a dependency of a plugin (binary, npm package, etc.).
type PluginDependency struct {
	ID          string         `json:"id"`
	Type        string         `json:"type"`     // "binary" | "npm" | "file" | "env"
	Name        string         `json:"name"`
	Required    bool           `json:"required"`
	Description string         `json:"description,omitempty"`
	Detect      *DetectConfig  `json:"detect,omitempty"`
	Install     *InstallConfig `json:"install,omitempty"`
}

// PluginPermissionDecl is a capability permission declaration in the manifest.
type PluginPermissionDecl struct {
	Capability  string   `json:"capability"`
	Description string   `json:"description,omitempty"`
	Allow       []string `json:"allow,omitempty"`
	Deny        []string `json:"deny,omitempty"`
}

// PluginInstallation is the installation state of a plugin on a specific node.
type PluginInstallation struct {
	PluginID    PluginID `json:"pluginId"`
	NodeID      NodeID   `json:"nodeId"`
	Status      string   `json:"status"`
	// "installed" | "not_installed" | "missing_dependency" | "failed" | "needs_permission" | "needs_config"
	Enabled     bool     `json:"enabled"`
	Version     string   `json:"version"`
	InstalledAt *int64   `json:"installedAt,omitempty"`
	UpdatedAt   *int64   `json:"updatedAt,omitempty"`
	Error       string   `json:"error,omitempty"`
}

// PluginEnvironment is the dependency check result for a plugin on a node.
type PluginEnvironment struct {
	PluginID     PluginID              `json:"pluginId"`
	NodeID       NodeID                `json:"nodeId"`
	CheckedAt    int64                 `json:"checkedAt"`
	Status       string                `json:"status"` // "ok" | "missing" | "partial" | "error"
	Dependencies []DependencyCheckResult `json:"dependencies"`
}

// DependencyCheckResult is the check result for a single dependency.
type DependencyCheckResult struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	Found    bool   `json:"found"`
	Version  string `json:"version,omitempty"`
	Required string `json:"required,omitempty"`
	Path     string `json:"path,omitempty"`
	Error    string `json:"error,omitempty"`
	Optional bool   `json:"optional,omitempty"`
}

// PluginPermissionGrant is a granted permission for a plugin on a specific node.
type PluginPermissionGrant struct {
	PluginID    PluginID              `json:"pluginId"`
	NodeID      NodeID                `json:"nodeId"`
	Capability  string                `json:"capability"`
	Mode        string                `json:"mode"`     // "allow" | "deny" | "ask"
	Constraints *PermissionConstraints `json:"constraints,omitempty"`
	GrantedAt   int64                 `json:"grantedAt"`
	GrantedBy   string                `json:"grantedBy"`
	ExpiresAt   *int64                `json:"expiresAt,omitempty"`
}

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

// PluginInstallHistory records one install/upgrade/repair/uninstall action.
type PluginInstallHistory struct {
	InstallID  string   `json:"installId"`
	PluginID   PluginID `json:"pluginId"`
	NodeID     NodeID   `json:"nodeId"`
	Action     string   `json:"action"`
	// "install" | "upgrade" | "repair" | "uninstall" | "uninstall_dependency"
	DependencyID string `json:"dependencyId,omitempty"`
	Method       string `json:"method,omitempty"`
	Command      string `json:"command,omitempty"`
	Status       string `json:"status"`
	// "pending" | "running" | "success" | "failed" | "cancelled"
	StartedAt  int64   `json:"startedAt"`
	FinishedAt *int64  `json:"finishedAt,omitempty"`
	StdoutLog  string  `json:"stdoutLog,omitempty"`
	StderrLog  string  `json:"stderrLog,omitempty"`
	Result     string  `json:"result,omitempty"`
	Actor      string  `json:"actor"`
	ApprovalID string  `json:"approvalId,omitempty"`
	Error      string  `json:"error,omitempty"`
}

// --- Manifest supporting types (minimal for Phase 0) ---

type WebManifest struct{}
type CLIManifest struct{}

type PluginFileDecl struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Path    string `json:"path"`
	Description string `json:"description,omitempty"`
}

type PluginCacheDecl struct {
	ID          string `json:"id"`
	Path        string `json:"path"`
	Description string `json:"description,omitempty"`
	Clearable   bool   `json:"clearable"`
}

type DetectConfig struct {
	Command string `json:"command"`
	Parse   string `json:"parse,omitempty"`
}

type InstallConfig struct {
	Windows *PlatformInstall `json:"windows,omitempty"`
	Darwin  *PlatformInstall `json:"darwin,omitempty"`
	Linux   *PlatformInstall `json:"linux,omitempty"`
}

type PlatformInstall struct {
	Method  string `json:"method"`
	Command string `json:"command"`
}
