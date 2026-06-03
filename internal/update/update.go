// Package update provides the update source, policy, and status models
// for sessionBridge Core self-update awareness. It is intentionally read-only
// for actual update execution — update.apply is never registered.
//
// The Manager persists source/policy/status as JSON files under dataDir and
// exposes thread-safe accessors for the executor handlers.
package update

import (
	"fmt"
	"sync"
)

// ── Source ───────────────────────────────────────────────────────────────

// UpdateSource describes where to check for updates.
type UpdateSource struct {
	Type    string `json:"type"`    // "git" (only supported type in this round)
	Remote  string `json:"remote"`  // default "origin"
	Branch  string `json:"branch"`  // default "main"
	RepoURL string `json:"repoUrl"` // remote fetch URL (required for git)
	Mode    string `json:"mode"`    // "manual" or "auto-check"
}

// DefaultSource returns the safe-default update source.
func DefaultSource() UpdateSource {
	return UpdateSource{
		Type:   "git",
		Remote: "origin",
		Branch: "main",
		Mode:   "manual",
	}
}

// ValidateSource returns an error string if the source is invalid.
func ValidateSource(s UpdateSource) string {
	if s.Type != "git" {
		return "unsupported source type: " + s.Type + " (only 'git' is supported)"
	}
	if s.Mode != "" && s.Mode != "manual" && s.Mode != "auto-check" {
		return "unsupported mode: " + s.Mode + " (must be 'manual' or 'auto-check')"
	}
	if s.Remote == "" {
		return "remote is required for git source"
	}
	if s.Branch == "" {
		return "branch is required for git source"
	}
	return ""
}

// ── Policy ───────────────────────────────────────────────────────────────

// UpdatePolicy controls update checking behaviour.
// autoApply is intentionally constrained to false — Core never auto-applies.
type UpdatePolicy struct {
	AutoCheck            bool     `json:"autoCheck"`
	AutoApply            bool     `json:"autoApply"`            // MUST be false
	CheckIntervalSeconds int      `json:"checkIntervalSeconds"` // default 86400 (24h)
	AllowDirtyWorktree   bool     `json:"allowDirtyWorktree"`
	AllowWhenRunsActive  bool     `json:"allowWhenRunsActive"`
	IgnoredVersions      []string `json:"ignoredVersions"`
}

// DefaultPolicy returns the safe-default update policy.
func DefaultPolicy() UpdatePolicy {
	return UpdatePolicy{
		AutoCheck:            false,
		AutoApply:            false,
		CheckIntervalSeconds: 86400,
		AllowDirtyWorktree:   false,
		AllowWhenRunsActive:  false,
		IgnoredVersions:      []string{},
	}
}

// ValidatePolicy returns an error string if the policy is invalid.
func ValidatePolicy(p UpdatePolicy) string {
	if p.AutoApply {
		return "autoApply is not supported — update.apply is never registered"
	}
	if p.CheckIntervalSeconds < 0 {
		return "checkIntervalSeconds must be >= 0"
	}
	return ""
}

// ── Status ───────────────────────────────────────────────────────────────

// StatusValue describes the current update status.
type StatusValue string

const (
	StatusUnknown     StatusValue = "unknown"
	StatusChecking    StatusValue = "checking"
	StatusUpToDate    StatusValue = "up-to-date"
	StatusUpdateAvail StatusValue = "update-available"
	StatusError       StatusValue = "error"
)

// UpdateStatus is the current snapshot of update state.
type UpdateStatus struct {
	Status          StatusValue  `json:"status"`
	CurrentCommit   string       `json:"currentCommit"`
	RemoteCommit    string       `json:"remoteCommit"`
	BehindBy        int          `json:"behindBy"`
	Dirty           bool         `json:"dirty"`
	Source          UpdateSource `json:"source"`
	LastCheckedAt   int64        `json:"lastCheckedAt"`
	LastCheckError  string       `json:"lastCheckError,omitempty"`
	RequiresRestart bool         `json:"requiresRestart"`
}

// DefaultStatus returns an empty status.
func DefaultStatus() UpdateStatus {
	return UpdateStatus{
		Status: StatusUnknown,
		Source: DefaultSource(),
	}
}

// ── Manager ──────────────────────────────────────────────────────────────

// Manager holds update source, policy, and status with file-backed persistence.
type Manager struct {
	mu      sync.RWMutex
	dataDir string

	source UpdateSource
	policy UpdatePolicy
	status UpdateStatus
}

// NewManager creates a Manager and loads persisted state from dataDir.
func NewManager(dataDir string) (*Manager, error) {
	m := &Manager{dataDir: dataDir}
	m.source = DefaultSource()
	m.policy = DefaultPolicy()
	m.status = DefaultStatus()

	if err := m.loadSource(); err != nil {
		return nil, fmt.Errorf("update manager: load source: %w", err)
	}
	if err := m.loadPolicy(); err != nil {
		return nil, fmt.Errorf("update manager: load policy: %w", err)
	}
	if err := m.loadStatus(); err != nil {
		return nil, fmt.Errorf("update manager: load status: %w", err)
	}

	return m, nil
}

// Source returns a copy of the current update source.
func (m *Manager) Source() UpdateSource {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.source
}

// SetSource validates and persists a new update source.
func (m *Manager) SetSource(s UpdateSource) error {
	if msg := ValidateSource(s); msg != "" {
		return fmt.Errorf("%s", msg)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.source = s
	return persistJSON(m.sourcePath(), s)
}

// Policy returns a copy of the current update policy.
func (m *Manager) Policy() UpdatePolicy {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.policy
}

// SetPolicy validates and persists a new update policy.
func (m *Manager) SetPolicy(p UpdatePolicy) error {
	if msg := ValidatePolicy(p); msg != "" {
		return fmt.Errorf("%s", msg)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.policy = p
	return persistJSON(m.policyPath(), p)
}

// Status returns a copy of the current update status.
func (m *Manager) Status() UpdateStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.status
}

// SetStatus persists a new update status.
func (m *Manager) SetStatus(s UpdateStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status = s
	return persistJSON(m.statusPath(), s)
}
