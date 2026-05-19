package types

// PluginFileEntry is a runtime-registered file location owned by a plugin.
type PluginFileEntry struct {
	ID           string   `json:"id"`
	PluginID     PluginID `json:"pluginId"`
	NodeID       NodeID   `json:"nodeId"`
	Path         string   `json:"path"`
	FileType     string   `json:"fileType"`
	// "history" | "config" | "state" | "cache" | "log" | "workspace-context" | "artifact" | "external"
	Description  string   `json:"description,omitempty"`
	Source       string   `json:"source"`       // "manifest" | "runtime"
	Visibility   string   `json:"visibility"`
	// "system" | "settings" | "workspace" | "user"
	Clearable    bool     `json:"clearable"`
	Size         int64    `json:"size,omitempty"`
	ModifiedAt   *int64   `json:"modifiedAt,omitempty"`
	RegisteredAt int64    `json:"registeredAt"`
	DefaultPanel string   `json:"defaultPanel,omitempty"`
}

// PluginCacheEntry represents a cache location that may be scattered across multiple paths.
type PluginCacheEntry struct {
	ID           string   `json:"id"`
	PluginID     PluginID `json:"pluginId"`
	NodeID       NodeID   `json:"nodeId"`
	Paths        []string `json:"paths"`                  // multiple scattered locations
	Description  string   `json:"description,omitempty"`
	Source       string   `json:"source"`
	// "manifest" | "install-plan" | "install-scan" | "runtime-register" | "known-detector"
	Owner        string   `json:"owner"`
	// "plugin" | "dependency" | "package-manager" | "shared"
	Clearable    bool     `json:"clearable"`
	ClearMode    string   `json:"clearMode"`
	// "delete-path" | "plugin-action" | "package-manager-command" | "manual-only"
	Risk         string   `json:"risk"`          // "low" | "medium" | "high"
	Size         int64    `json:"size,omitempty"`
	LastAccessAt *int64   `json:"lastAccessAt,omitempty"`
	CreatedAt    int64    `json:"createdAt"`
	DefaultPanel string   `json:"defaultPanel,omitempty"`
}

// FileAccessRecord is a single file operation audit entry.
type FileAccessRecord struct {
	PluginID  PluginID  `json:"pluginId"`
	NodeID    NodeID    `json:"nodeId"`
	Path      string    `json:"path"`
	Action    string    `json:"action"`    // "read" | "write" | "delete" | "list"
	Timestamp int64     `json:"timestamp"`
	SessionID SessionID `json:"sessionId,omitempty"`
	RequestID RequestID `json:"requestId,omitempty"`
	Allowed   bool      `json:"allowed"`
}
