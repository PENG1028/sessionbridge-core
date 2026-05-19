package types

// Source constants for DeclaredLocation / PlannedArtifact / DiscoveredSideEffect.
const (
	SourceManifest      = "manifest"
	SourceInstallPlan   = "install-plan"
	SourceInstallScan   = "install-scan"
	SourcePrePostDiff   = "pre-post-diff"
	SourceCmdOutputParse = "command-output-parse"
	SourceKnownDetector = "known-detector"
	SourceRuntime       = "runtime"
)

// FileType constants shared across side-effect types.
const (
	FileTypeBinary  = "binary"
	FileTypeConfig  = "config"
	FileTypeCache   = "cache"
	FileTypeHistory = "history"
	FileTypeLog     = "log"
	FileTypePackage = "package"
	FileTypeRegistry = "registry"
	FileTypeEnv     = "env"
	FileTypeUnknown = "unknown"
)

// DeclaredLocation is a file location declared in the plugin manifest.
type DeclaredLocation struct {
	Source      string   `json:"source"`       // "manifest"
	PluginID    PluginID `json:"pluginId"`
	NodeID      NodeID   `json:"nodeId"`
	Path        string   `json:"path"`
	Description string   `json:"description,omitempty"`
	FileType    string   `json:"fileType"`
}

// PlannedArtifact is an artifact expected by the install plan.
type PlannedArtifact struct {
	Source      string   `json:"source"`       // "install-plan"
	InstallID   string   `json:"installId"`
	PluginID    PluginID `json:"pluginId"`
	NodeID      NodeID   `json:"nodeId"`
	Path        string   `json:"path"`
	Description string   `json:"description,omitempty"`
	FileType    string   `json:"fileType"`
	Clearable   bool     `json:"clearable"`
	Removable   bool     `json:"removable"`
}

// DiscoveredSideEffect is a side effect found by scanning or snapshot comparison.
type DiscoveredSideEffect struct {
	Source        string   `json:"source"`
	// "install-scan" | "pre-post-diff" | "command-output-parse" | "known-detector"
	InstallID     string   `json:"installId,omitempty"`
	PluginID      PluginID `json:"pluginId"`
	NodeID        NodeID   `json:"nodeId"`
	Path          string   `json:"path"`
	Description   string   `json:"description,omitempty"`
	FileType      string   `json:"fileType"`
	ExistedBefore bool     `json:"existedBefore"`
	Clearable     bool     `json:"clearable"`
	Removable     bool     `json:"removable"`
	UserOwned     bool     `json:"userOwned"`
	Shared        bool     `json:"shared"`
	Dangerous     bool     `json:"dangerous"`
}

// InstallSideEffect is the complete side-effect record for one install.
type InstallSideEffect struct {
	InstallID  string                 `json:"installId"`
	PluginID   PluginID               `json:"pluginId"`
	NodeID     NodeID                 `json:"nodeId"`
	Declared   []DeclaredLocation     `json:"declared,omitempty"`
	Planned    []PlannedArtifact      `json:"planned,omitempty"`
	Discovered []DiscoveredSideEffect `json:"discovered,omitempty"`
	CreatedAt  int64                  `json:"createdAt"`
}

// InstallArtifact is a registered install artifact in the cumulative registry.
type InstallArtifact struct {
	ArtifactID   string   `json:"artifactId"`
	InstallID    string   `json:"installId"`
	PluginID     PluginID `json:"pluginId"`
	NodeID       NodeID   `json:"nodeId"`
	Path         string   `json:"path"`
	ArtifactType string   `json:"artifactType"`
	// "binary" | "config" | "cache" | "history" | "log" | "package" | "registry" | "env" | "unknown"
	Source       string   `json:"source"`
	// "declared" | "planned" | "discovered"
	Clearable    bool     `json:"clearable"`
	Removable    bool     `json:"removable"`
	UserOwned    bool     `json:"userOwned"`
	Shared       bool     `json:"shared"`
	Dangerous    bool     `json:"dangerous"`
	RegisteredAt int64    `json:"registeredAt"`
}

// DependencyGraphNode is one node in a dependency chain.
type DependencyGraphNode struct {
	DependencyID string   `json:"dependencyId"`
	Reason       string   `json:"reason"`       // "required_for_npm" | "required_by_plugin"
	Status       string   `json:"status"`        // "installed" | "missing" | "failed"
	Artifacts    []string `json:"artifacts,omitempty"`
}
