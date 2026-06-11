package executor

import (
	"strings"

	"github.com/PENG1028/sessionbridge-core/pkg/types"
)

// ClassifyCapability maps a capability string to its OpClass for OpLog recording.
//
// Classification rules:
//   - A (Content): operations that write/modify content where the old value
//     should be backed up for rollback (config writes, file writes, env set)
//   - B (Artifact): operations that produce files but don't need content backup
//     (downloads, installs, removals)
//   - C (State): operations that change system state (session/process/peer lifecycle)
//   - D (Noop): read-only operations that should not be recorded
//
// Unknown capabilities default to C (conservative: assume state change).
func ClassifyCapability(cap string) types.OpClass {
	switch {
	// A — Content (backup before content)
	case strings.HasPrefix(cap, "fs.write"):
		return types.OpClassContent
	case strings.HasPrefix(cap, "fs.rename"):
		return types.OpClassContent
	case strings.HasPrefix(cap, "config.set"):
		return types.OpClassContent
	case strings.HasPrefix(cap, "config.reset"):
		return types.OpClassContent
	case strings.HasPrefix(cap, "env.set"):
		return types.OpClassContent
	case strings.HasPrefix(cap, "env.unset"):
		return types.OpClassContent
	case strings.HasPrefix(cap, "permission."):
		return types.OpClassContent

	// B — Artifact (metadata only)
	case strings.HasPrefix(cap, "fs.remove"):
		return types.OpClassArtifact
	case strings.HasPrefix(cap, "fs.mkdir"):
		return types.OpClassArtifact
	case strings.HasPrefix(cap, "sync."):
		return types.OpClassArtifact

	// C — State (reversible)
	case strings.HasPrefix(cap, "session."):
		return types.OpClassState
	case strings.HasPrefix(cap, "run."):
		return types.OpClassState
	case strings.HasPrefix(cap, "process."):
		return types.OpClassState
	case strings.HasPrefix(cap, "peer."):
		return types.OpClassState
	case strings.HasPrefix(cap, "node.peer."):
		return types.OpClassState
	case strings.HasPrefix(cap, "node.invite."):
		return types.OpClassState
	case strings.HasPrefix(cap, "node.identity."):
		return types.OpClassState
	case strings.HasPrefix(cap, "task."):
		return types.OpClassState
	case strings.HasPrefix(cap, "plan."):
		return types.OpClassState
	case strings.HasPrefix(cap, "approval."):
		return types.OpClassState
	case strings.HasPrefix(cap, "notify."):
		return types.OpClassState
	case strings.HasPrefix(cap, "admin."):
		return types.OpClassState
	case strings.HasPrefix(cap, "update.source."):
		return types.OpClassState
	case strings.HasPrefix(cap, "update.policy."):
		return types.OpClassState
	case strings.HasPrefix(cap, "update.ignore"):
		return types.OpClassState

	// D — Noop (read-only, not recorded)
	case isReadOnlyCap(cap):
		return types.OpClassNoop

	// Default: conservative — assume state change
	default:
		return types.OpClassState
	}
}

// isReadOnlyCap returns true for capabilities that only read state.
func isReadOnlyCap(cap string) bool {
	switch {
	case strings.HasPrefix(cap, "fs.read"),
		strings.HasPrefix(cap, "fs.list"),
		strings.HasPrefix(cap, "fs.stat"),
		strings.HasPrefix(cap, "sys."),
		strings.HasPrefix(cap, "stream."),
		strings.HasPrefix(cap, "logs."),
		strings.HasPrefix(cap, "audit."),
		strings.HasPrefix(cap, "node.list"),
		strings.HasPrefix(cap, "node.info"),
		strings.HasPrefix(cap, "node.health"),
		strings.HasPrefix(cap, "node.reachability."),
		strings.HasPrefix(cap, "env.get"),
		strings.HasPrefix(cap, "env.list"),
		strings.HasPrefix(cap, "env.which"),
		strings.HasPrefix(cap, "env.checkBinary"),
		strings.HasPrefix(cap, "env.home"),
		strings.HasPrefix(cap, "env.cwd"),
		strings.HasPrefix(cap, "config.list"),
		strings.HasPrefix(cap, "config.get"),
		strings.HasPrefix(cap, "session.get"),
		strings.HasPrefix(cap, "session.info"),
		strings.HasPrefix(cap, "session.list"),
		strings.HasPrefix(cap, "session.history.stats"),
		strings.HasPrefix(cap, "session.history.list"),
		strings.HasPrefix(cap, "session.history.getPolicy"),
		strings.HasPrefix(cap, "update.status"),
		strings.HasPrefix(cap, "update.check"),
		strings.HasPrefix(cap, "update.plan"):
		return true
	}
	return false
}
