// Package run provides a lightweight, core-managed index for long-lived
// execution resources (processes, terminal sessions, workflows, etc.).
//
// Run sits above process/session/stream — those remain the low-level execution
// APIs. Run assigns a stable runId that callers (UI, scheduler) can project onto.
//
// Run is intentionally NOT a UI tab, team permission, or business object.
// Core does not interpret metadata — callers provide it, core stores it.
package run

import "github.com/user/sessionnode/go-core/pkg/types"

// Kind constants for the types of long-lived resources a run represents.
const (
	KindTerminal = "terminal"
	KindProcess  = "process"
	KindService  = "service"
	KindWorkflow = "workflow"
)

// State constants.
const (
	StateRunning  = "running"
	StateExited   = "exited"
	StateStopped  = "stopped"
	StateFailed   = "failed"
	StateArchived = "archived"
)

// Policy on-disconnect / shutdown values supported in this round.
const (
	OnDisconnectKeepRunning = "keep_running"
	OnCoreShutdownTerminate = "terminate"
)

// Policy describes what happens to the underlying process on certain events.
type Policy struct {
	OnDisconnect   string `json:"onDisconnect"`
	OnCoreShutdown string `json:"onCoreShutdown"`
	PersistHistory bool   `json:"persistHistory"`
	RestartRestore bool   `json:"restartRestore"`
}

// Run is the stable index entry for a long-lived execution resource.
type Run struct {
	RunID     string            `json:"runId"`
	NodeID    string            `json:"nodeId"`
	Kind      string            `json:"kind"`
	Label     string            `json:"label"`
	PluginID  types.PluginID    `json:"pluginId"`
	State     string            `json:"state"`
	SessionID types.SessionID   `json:"sessionId"`
	ProcessID types.SessionID   `json:"processId"`
	CreatedAt int64             `json:"createdAt"`
	UpdatedAt int64             `json:"updatedAt"`
	Policy    Policy            `json:"policy"`
	Metadata  map[string]string `json:"metadata"`
}

// DefaultPolicy returns the safe-default policy for a new run.
func DefaultPolicy() Policy {
	return Policy{
		OnDisconnect:   OnDisconnectKeepRunning,
		OnCoreShutdown: OnCoreShutdownTerminate,
		PersistHistory: true,
		RestartRestore: false,
	}
}

// ValidatePolicy returns an error string if the policy contains unsupported values.
// Returns empty string when valid.
func ValidatePolicy(p Policy) string {
	if p.OnDisconnect != "" && p.OnDisconnect != OnDisconnectKeepRunning {
		return "unsupported onDisconnect value: " + p.OnDisconnect
	}
	if p.OnCoreShutdown != "" && p.OnCoreShutdown != OnCoreShutdownTerminate {
		return "unsupported onCoreShutdown value: " + p.OnCoreShutdown
	}
	if p.RestartRestore {
		return "restartRestore is not supported in this round"
	}
	return ""
}
