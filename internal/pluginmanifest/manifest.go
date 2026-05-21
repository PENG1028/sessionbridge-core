package pluginmanifest

// Manifest is the root plugin manifest structure (plugin.yaml).
// Fields align with PLUGIN_MANIFEST_SPEC.md.
type Manifest struct {
	ManifestVersion string      `yaml:"manifestVersion" json:"manifestVersion"`
	ID              string      `yaml:"id" json:"id"`
	Name            string      `yaml:"name" json:"name"`
	Version         string      `yaml:"version" json:"version"`
	Type            string      `yaml:"type" json:"type"` // "plugin" | "system"
	Trusted         bool        `yaml:"trusted" json:"trusted"`
	Description     string      `yaml:"description" json:"description,omitempty"`
	Author          string      `yaml:"author" json:"author,omitempty"`
	Homepage        string      `yaml:"homepage" json:"homepage,omitempty"`
	License         string      `yaml:"license" json:"license,omitempty"`
	Core            *CoreSpec   `yaml:"core" json:"core"`
	Adapters        AdapterSpec `yaml:"adapters" json:"adapters"`
}

// CoreSpec defines the core contract — the only required section.
type CoreSpec struct {
	Permissions []PermissionSpec  `yaml:"permissions" json:"permissions"`
	Environment EnvironmentSpec    `yaml:"environment" json:"environment"`
	Files       FilesSpec         `yaml:"files" json:"files"`
	Tasks       []TaskSpec        `yaml:"tasks" json:"tasks"`
	History     HistorySpec       `yaml:"history" json:"history"`
}

// PermissionSpec declares a permission that the plugin requires.
type PermissionSpec struct {
	ID           string             `yaml:"id" json:"id"`
	Description  string             `yaml:"description" json:"description"`
	Capabilities []string           `yaml:"capabilities" json:"capabilities"`
	Default      string             `yaml:"default" json:"default"` // "ask" | "deny" | "allow"
	Constraints  *PermissionConstrainsSpec `yaml:"constraints" json:"constraints,omitempty"`
}

// PermissionConstrainsSpec defines constraints for a permission.
type PermissionConstrainsSpec struct {
	Paths       *PathConstraints `yaml:"paths" json:"paths,omitempty"`
	TargetNodes []string         `yaml:"targetNodes" json:"targetNodes,omitempty"`
	Env         []string         `yaml:"env" json:"env,omitempty"`
	Network     *NetworkConstraints `yaml:"network" json:"network,omitempty"`
	Resources   *ResourceLimit   `yaml:"resources" json:"resources,omitempty"`
}

// PathConstraints defines allow/deny path globs.
type PathConstraints struct {
	Allow []string `yaml:"allow" json:"allow"`
	Deny  []string `yaml:"deny" json:"deny"`
}

// NetworkConstraints defines network access constraints for a permission.
type NetworkConstraints struct {
	Hosts   []string `yaml:"hosts" json:"hosts,omitempty"`
	Ports   []int    `yaml:"ports" json:"ports,omitempty"`
	Schemes []string `yaml:"schemes" json:"schemes,omitempty"`
}

// ResourceLimit defines resource constraints.
type ResourceLimit struct {
	MaxMemory  string `yaml:"maxMemory" json:"maxMemory,omitempty"`
	MaxCPU     string `yaml:"maxCPU" json:"maxCPU,omitempty"`
	MaxDisk    string `yaml:"maxDisk" json:"maxDisk,omitempty"`
	MaxProcess int    `yaml:"maxProcess" json:"maxProcess,omitempty"`
}

// EnvironmentSpec defines environment checks.
type EnvironmentSpec struct {
	Checks []EnvCheckSpec `yaml:"checks" json:"checks"`
}

// EnvCheckSpec declares a single environment check.
type EnvCheckSpec struct {
	ID              string `yaml:"id" json:"id"`
	Type            string `yaml:"type" json:"type"` // "binary" | "env" | "path" | "file" | "directory" | "command"
	Required        bool   `yaml:"required" json:"required"`
	Command         string `yaml:"command" json:"command,omitempty"`
	Args            string `yaml:"args" json:"args,omitempty"`
	VersionCommand  string `yaml:"versionCommand" json:"versionCommand,omitempty"`
	RequiredVersion string `yaml:"requiredVersion" json:"requiredVersion,omitempty"`
	InstallHint     string `yaml:"installHint" json:"installHint,omitempty"`
}

// FilesSpec declares plugin file/cache/log locations.
type FilesSpec struct {
	ConfigDir  string         `yaml:"config" json:"config,omitempty"`
	DataDir    string         `yaml:"data" json:"data,omitempty"`
	CacheDir   string         `yaml:"cache" json:"cache,omitempty"`
	LogsDir    string         `yaml:"logs" json:"logs,omitempty"`
	Artifacts  string         `yaml:"artifacts" json:"artifacts,omitempty"`
	Declarations []FileDecl   `yaml:"declarations" json:"declarations,omitempty"`
}

// FileDecl declares a specific file or directory used by the plugin.
type FileDecl struct {
	ID          string `yaml:"id" json:"id"`
	Path        string `yaml:"path" json:"path"`
	Description string `yaml:"description" json:"description,omitempty"`
	Clearable   bool   `yaml:"clearable" json:"clearable"`
	External    bool   `yaml:"external" json:"external,omitempty"`
	Risk        string `yaml:"risk" json:"risk,omitempty"` // "low" | "medium" | "high"
}

// TaskSpec declares a task that the plugin can perform.
type TaskSpec struct {
	ID           string `yaml:"id" json:"id"`
	Capability   string `yaml:"capability" json:"capability"`
	PlanRequired bool   `yaml:"planRequired" json:"planRequired"`
	Risk         string `yaml:"risk" json:"risk"` // "low" | "medium" | "high"
}

// HistorySpec defines the plugin's history/stream persistence policy.
type HistorySpec struct {
	DefaultPolicy string `yaml:"defaultPolicy" json:"defaultPolicy,omitempty"` // "memory" | "disk" | "none"
}

// AdapterSpec holds all adapter declarations. All are optional.
type AdapterSpec struct {
	SystemUI *SystemUIAdapter `yaml:"system-ui" json:"system-ui,omitempty"`
	CLI      *CLIAdapter      `yaml:"cli" json:"cli,omitempty"`
}

// SystemUIAdapter declares UI contributions for the system-ui host.
type SystemUIAdapter struct {
	Views      []UIViewSpec      `yaml:"views" json:"views,omitempty"`
	Panels     []UIPanelSpec     `yaml:"panels" json:"panels,omitempty"`
	Settings   *SettingsSchema   `yaml:"settings" json:"settings,omitempty"`
	Commands   []UICommandSpec   `yaml:"commands" json:"commands,omitempty"`
	Status     []UIStatusSpec    `yaml:"status" json:"status,omitempty"`
}

// UIViewSpec declares a view for system-ui.
type UIViewSpec struct {
	ID      string `yaml:"id" json:"id"`
	Surface string `yaml:"surface" json:"surface"`
	Type    string `yaml:"type" json:"type"` // "custom-react" | "host-rendered"
	Entry   string `yaml:"entry" json:"entry,omitempty"`
	Title   string `yaml:"title" json:"title,omitempty"`
	Icon    string `yaml:"icon" json:"icon,omitempty"`
}

// UIPanelSpec declares a panel for system-ui.
type UIPanelSpec struct {
	ID      string `yaml:"id" json:"id"`
	Surface string `yaml:"surface" json:"surface"`
	Type    string `yaml:"type" json:"type"`
	Entry   string `yaml:"entry" json:"entry,omitempty"`
	Title   string `yaml:"title" json:"title,omitempty"`
}

// SettingsSchema declares configuration JSON schema for system-ui.
type SettingsSchema struct {
	Schema     string                 `yaml:"schema" json:"schema,omitempty"`
	Properties map[string]interface{} `yaml:"properties" json:"properties,omitempty"`
}

// UICommandSpec declares a command for system-ui.
type UICommandSpec struct {
	ID          string `yaml:"id" json:"id"`
	Title       string `yaml:"title" json:"title"`
	Command     string `yaml:"command" json:"command,omitempty"`
}

// UIStatusSpec declares a status bar item for system-ui.
type UIStatusSpec struct {
	ID      string `yaml:"id" json:"id"`
	Label   string `yaml:"label" json:"label"`
	Icon    string `yaml:"icon" json:"icon,omitempty"`
	Command string `yaml:"command" json:"command,omitempty"`
}

// CLIAdapter declares CLI command contributions.
type CLIAdapter struct {
	Commands []CLICommandSpec `yaml:"commands" json:"commands"`
}

// CLICommandSpec declares a single CLI command.
type CLICommandSpec struct {
	ID          string   `yaml:"id" json:"id"`
	Name        string   `yaml:"name" json:"name"`
	Description string   `yaml:"description" json:"description"`
	Entry       string   `yaml:"entry" json:"entry,omitempty"`
	Handler     string   `yaml:"handler" json:"handler,omitempty"`
	Permissions []string `yaml:"permissions" json:"permissions,omitempty"`
	Args        string   `yaml:"args" json:"args,omitempty"`
	Examples    []string `yaml:"examples" json:"examples,omitempty"`
}

// PathVars provides variable substitution for path expressions.
type PathVars struct {
	Home         string
	Workspace    string
	PluginDir    string
	PluginConfig string
	PluginData   string
	PluginCache  string
	PluginLogs   string
	PluginArtifacts string
	NodeDataDir  string
	Temp         string
}
