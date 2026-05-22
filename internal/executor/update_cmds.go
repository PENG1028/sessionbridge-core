package executor

import (
	"fmt"

	"github.com/user/sessionnode/go-core/internal/run"
	"github.com/user/sessionnode/go-core/internal/update"
	"github.com/user/sessionnode/go-core/pkg/types"
)

// ── update.status ────────────────────────────────────────────────────────

func updateStatus(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	if deps.UpdateManager == nil {
		return nil, fmt.Errorf("update manager not available")
	}
	s := deps.UpdateManager.Status()
	return map[string]interface{}{
		"status":          string(s.Status),
		"currentCommit":   s.CurrentCommit,
		"remoteCommit":    s.RemoteCommit,
		"behindBy":        s.BehindBy,
		"dirty":           s.Dirty,
		"source":          s.Source,
		"lastCheckedAt":   s.LastCheckedAt,
		"lastCheckError":  s.LastCheckError,
		"requiresRestart": s.RequiresRestart,
	}, nil
}

// ── update.source.get ────────────────────────────────────────────────────

func updateSourceGet(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	if deps.UpdateManager == nil {
		return nil, fmt.Errorf("update manager not available")
	}
	s := deps.UpdateManager.Source()
	return map[string]interface{}{
		"type":    s.Type,
		"remote":  s.Remote,
		"branch":  s.Branch,
		"repoUrl": s.RepoURL,
		"mode":    s.Mode,
	}, nil
}

// ── update.source.set ────────────────────────────────────────────────────

type updateSourceSetPayload struct {
	Type    string `json:"type"`
	Remote  string `json:"remote"`
	Branch  string `json:"branch"`
	RepoURL string `json:"repoUrl"`
	Mode    string `json:"mode"`
}

func updateSourceSet(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	if deps.UpdateManager == nil {
		return nil, fmt.Errorf("update manager not available")
	}

	var p updateSourceSetPayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}

	s := update.UpdateSource{
		Type:    p.Type,
		Remote:  p.Remote,
		Branch:  p.Branch,
		RepoURL: p.RepoURL,
		Mode:    p.Mode,
	}

	// Fill defaults
	if s.Type == "" {
		s.Type = "git"
	}
	if s.Remote == "" {
		s.Remote = "origin"
	}
	if s.Branch == "" {
		s.Branch = "main"
	}
	if s.Mode == "" {
		s.Mode = "manual"
	}

	if err := deps.UpdateManager.SetSource(s); err != nil {
		return nil, fmt.Errorf("invalid source: %w", err)
	}

	return map[string]interface{}{
		"type":    s.Type,
		"remote":  s.Remote,
		"branch":  s.Branch,
		"repoUrl": s.RepoURL,
		"mode":    s.Mode,
	}, nil
}

// ── update.policy.get ────────────────────────────────────────────────────

func updatePolicyGet(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	if deps.UpdateManager == nil {
		return nil, fmt.Errorf("update manager not available")
	}
	p := deps.UpdateManager.Policy()
	return map[string]interface{}{
		"autoCheck":            p.AutoCheck,
		"autoApply":            p.AutoApply,
		"checkIntervalSeconds": p.CheckIntervalSeconds,
		"allowDirtyWorktree":   p.AllowDirtyWorktree,
		"allowWhenRunsActive":  p.AllowWhenRunsActive,
		"ignoredVersions":      p.IgnoredVersions,
	}, nil
}

// ── update.policy.set ────────────────────────────────────────────────────

type updatePolicySetPayload struct {
	AutoCheck            *bool    `json:"autoCheck,omitempty"`
	AutoApply            *bool    `json:"autoApply,omitempty"`
	CheckIntervalSeconds *int     `json:"checkIntervalSeconds,omitempty"`
	AllowDirtyWorktree   *bool    `json:"allowDirtyWorktree,omitempty"`
	AllowWhenRunsActive  *bool    `json:"allowWhenRunsActive,omitempty"`
	IgnoredVersions      []string `json:"ignoredVersions,omitempty"`
}

func updatePolicySet(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	if deps.UpdateManager == nil {
		return nil, fmt.Errorf("update manager not available")
	}

	var p updatePolicySetPayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}

	// Merge with current policy
	cur := deps.UpdateManager.Policy()
	if p.AutoCheck != nil {
		cur.AutoCheck = *p.AutoCheck
	}
	if p.AutoApply != nil {
		cur.AutoApply = *p.AutoApply
	}
	if p.CheckIntervalSeconds != nil {
		cur.CheckIntervalSeconds = *p.CheckIntervalSeconds
	}
	if p.AllowDirtyWorktree != nil {
		cur.AllowDirtyWorktree = *p.AllowDirtyWorktree
	}
	if p.AllowWhenRunsActive != nil {
		cur.AllowWhenRunsActive = *p.AllowWhenRunsActive
	}
	if p.IgnoredVersions != nil {
		cur.IgnoredVersions = p.IgnoredVersions
	}

	if err := deps.UpdateManager.SetPolicy(cur); err != nil {
		return nil, fmt.Errorf("invalid policy: %w", err)
	}

	return map[string]interface{}{
		"autoCheck":            cur.AutoCheck,
		"autoApply":            cur.AutoApply,
		"checkIntervalSeconds": cur.CheckIntervalSeconds,
		"allowDirtyWorktree":   cur.AllowDirtyWorktree,
		"allowWhenRunsActive":  cur.AllowWhenRunsActive,
		"ignoredVersions":      cur.IgnoredVersions,
	}, nil
}

// ── update.check ─────────────────────────────────────────────────────────
//
// update.check is side-effect-free with respect to the git working copy
// and remote tracking refs. It uses:
//   - git rev-parse HEAD (local, read-only)
//   - git ls-remote <remote> refs/heads/<branch> (remote, read-only)
//   - git status --porcelain (local, read-only)
//
// It persists UpdateStatus to Core's dataDir, which is Core metadata,
// not a git repository side effect.

func updateCheck(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	if deps.UpdateManager == nil {
		return nil, fmt.Errorf("update manager not available")
	}
	if deps.GitRunner == nil {
		return nil, fmt.Errorf("git runner not available — update.check requires a git repository")
	}

	src := deps.UpdateManager.Source()
	if src.Type != "git" {
		return nil, fmt.Errorf("unsupported source type: %s", src.Type)
	}

	// Mark status as checking
	deps.UpdateManager.SetStatus(update.UpdateStatus{
		Status: update.StatusChecking,
		Source: src,
	})

	// Get current HEAD (read-only)
	currentCommit, err := deps.GitRunner.HeadCommit()
	if err != nil {
		deps.UpdateManager.SetStatus(update.UpdateStatus{
			Status:         update.StatusError,
			Source:         src,
			LastCheckError: fmt.Sprintf("rev-parse HEAD: %v", err),
		})
		return nil, fmt.Errorf("git rev-parse HEAD: %w", err)
	}

	// Get remote HEAD via ls-remote (read-only, no fetch)
	remoteCommit, err := deps.GitRunner.RemoteHead(src.Remote, src.Branch)
	if err != nil {
		deps.UpdateManager.SetStatus(update.UpdateStatus{
			Status:         update.StatusError,
			Source:         src,
			CurrentCommit:  currentCommit,
			LastCheckError: fmt.Sprintf("ls-remote %s refs/heads/%s: %v", src.Remote, src.Branch, err),
		})
		return nil, fmt.Errorf("git ls-remote %s: %w", src.Remote, err)
	}

	// Check dirty (read-only)
	dirty, err := deps.GitRunner.IsDirty()
	if err != nil {
		deps.UpdateManager.SetStatus(update.UpdateStatus{
			Status:         update.StatusError,
			Source:         src,
			CurrentCommit:  currentCommit,
			RemoteCommit:   remoteCommit,
			LastCheckError: fmt.Sprintf("dirty check: %v", err),
		})
		return nil, fmt.Errorf("dirty check: %w", err)
	}

	// Determine status by comparing local HEAD vs remote HEAD directly.
	// behindBy is kept for display compatibility but is 1 when behind, 0 otherwise
	// since we no longer compute an exact count (that requires fetch + rev-list).
	behindBy := 0
	st := update.UpdateStatus{
		Status:        update.StatusUpToDate,
		Source:        src,
		CurrentCommit: currentCommit,
		RemoteCommit:  remoteCommit,
		BehindBy:      behindBy,
		Dirty:         dirty,
		LastCheckedAt: nowMillis(),
	}

	if currentCommit != remoteCommit {
		behindBy = 1
		st.BehindBy = 1
		// Check if remote commit is in ignored versions
		policy := deps.UpdateManager.Policy()
		ignored := false
		for _, v := range policy.IgnoredVersions {
			if v == remoteCommit {
				ignored = true
				break
			}
		}
		if ignored {
			st.Status = update.StatusUpToDate
		} else {
			st.Status = update.StatusUpdateAvail
			st.RequiresRestart = true
		}
	}

	deps.UpdateManager.SetStatus(st)
	return statusToMap(st), nil
}

// ── update.plan ──────────────────────────────────────────────────────────
//
// update.plan is side-effect-free. It reads the last update.status and
// enumerates blockers. It does NOT call git fetch, git pull, or git merge.
// The returned steps are informational only — Core never executes them.

type planBlocker struct {
	Type    string `json:"type"`
	Message string `json:"message"`
	Detail  string `json:"detail,omitempty"`
}

func updatePlan(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	if deps.UpdateManager == nil {
		return nil, fmt.Errorf("update manager not available")
	}

	st := deps.UpdateManager.Status()
	policy := deps.UpdateManager.Policy()

	// If status has never been checked, run a side-effect-free check first.
	if st.Status == update.StatusUnknown {
		result, err := updateCheck(req, deps)
		if err != nil {
			return nil, fmt.Errorf("pre-flight check failed: %w", err)
		}
		// Re-read status after check
		st = deps.UpdateManager.Status()
		_ = result
	}

	var blockers []planBlocker

	// Blocker: worktree is dirty
	if st.Dirty && !policy.AllowDirtyWorktree {
		blockers = append(blockers, planBlocker{
			Type:    "dirty_worktree",
			Message: "Worktree has uncommitted changes. Commit or stash before updating.",
			Detail:  "Set allowDirtyWorktree: true in update policy to override.",
		})
	}

	// Blocker: active runs
	if deps.RunStore != nil && !policy.AllowWhenRunsActive {
		runs := deps.RunStore.List("", "", run.StateRunning)
		if len(runs) > 0 {
			runIDs := make([]string, len(runs))
			for i, r := range runs {
				runIDs[i] = r.RunID
			}
			blockers = append(blockers, planBlocker{
				Type:    "active_runs",
				Message: fmt.Sprintf("%d active run(s) would be interrupted.", len(runs)),
				Detail:  fmt.Sprintf("Active run IDs: %v. Set allowWhenRunsActive: true in update policy to override.", runIDs),
			})
		}
	}

	// Blocker: no git runner available (can't actually update without it)
	if deps.GitRunner == nil {
		blockers = append(blockers, planBlocker{
			Type:    "no_git_runner",
			Message: "Git runner not available — cannot execute update.",
		})
	}

	canUpdate := len(blockers) == 0 && st.Status == update.StatusUpdateAvail

	// Plan steps are informational only — Core never executes them.
	steps := []map[string]interface{}{}
	if canUpdate {
		steps = append(steps, map[string]interface{}{
			"order":       1,
			"action":      "git_pull",
			"description": fmt.Sprintf("Run: git pull --ff-only %s %s", st.Source.Remote, st.Source.Branch),
		})
		steps = append(steps, map[string]interface{}{
			"order":       2,
			"action":      "restart_core",
			"description": "Restart sessionBridge Core to apply update.",
		})
	}

	blockerMaps := make([]map[string]interface{}, len(blockers))
	for i, b := range blockers {
		blockerMaps[i] = map[string]interface{}{
			"type":    b.Type,
			"message": b.Message,
			"detail":  b.Detail,
		}
	}

	return map[string]interface{}{
		"canUpdate":     canUpdate,
		"status":        string(st.Status),
		"currentCommit": st.CurrentCommit,
		"remoteCommit":  st.RemoteCommit,
		"behindBy":      st.BehindBy,
		"dirty":         st.Dirty,
		"blockers":      blockerMaps,
		"steps":         steps,
	}, nil
}

// ── update.ignore ────────────────────────────────────────────────────────

type updateIgnorePayload struct {
	Version string `json:"version"` // commit hash to ignore
}

func updateIgnore(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	if deps.UpdateManager == nil {
		return nil, fmt.Errorf("update manager not available")
	}

	var p updateIgnorePayload
	if err := decodePayload(req.Payload, &p); err != nil {
		return nil, fmt.Errorf("invalid payload: %w", err)
	}
	if p.Version == "" {
		return nil, fmt.Errorf("version is required (commit hash)")
	}

	policy := deps.UpdateManager.Policy()

	// Deduplicate
	for _, v := range policy.IgnoredVersions {
		if v == p.Version {
			return map[string]interface{}{
				"ignoredVersions": policy.IgnoredVersions,
				"alreadyIgnored":  true,
			}, nil
		}
	}

	policy.IgnoredVersions = append(policy.IgnoredVersions, p.Version)
	if err := deps.UpdateManager.SetPolicy(policy); err != nil {
		return nil, fmt.Errorf("save policy: %w", err)
	}

	// If the ignored version is the current remote commit, flip status back to up-to-date
	st := deps.UpdateManager.Status()
	if st.RemoteCommit == p.Version && st.Status == update.StatusUpdateAvail {
		deps.UpdateManager.SetStatus(update.UpdateStatus{
			Status:         update.StatusUpToDate,
			CurrentCommit:  st.CurrentCommit,
			RemoteCommit:   st.RemoteCommit,
			BehindBy:       st.BehindBy,
			Dirty:          st.Dirty,
			Source:         st.Source,
			LastCheckedAt:  st.LastCheckedAt,
			LastCheckError: st.LastCheckError,
		})
	}

	return map[string]interface{}{
		"ignoredVersions": policy.IgnoredVersions,
		"ignoredVersion":  p.Version,
	}, nil
}

// ── helpers ─────────────────────────────────────────────────────────────

func statusToMap(s update.UpdateStatus) map[string]interface{} {
	return map[string]interface{}{
		"status":          string(s.Status),
		"currentCommit":   s.CurrentCommit,
		"remoteCommit":    s.RemoteCommit,
		"behindBy":        s.BehindBy,
		"dirty":           s.Dirty,
		"source":          s.Source,
		"lastCheckedAt":   s.LastCheckedAt,
		"lastCheckError":  s.LastCheckError,
		"requiresRestart": s.RequiresRestart,
	}
}
