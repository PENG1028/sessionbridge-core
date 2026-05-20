package executor

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/user/sessionnode/go-core/pkg/types"
)

// InstallPlan represents a plugin installation plan.
type InstallPlan struct {
	PlanID     string        `json:"planId"`
	PluginID   string        `json:"pluginId"`
	Steps      []InstallStep `json:"steps"`
	Risk       string        `json:"risk"`   // "low", "medium", "high"
	Status     string        `json:"status"` // "pending_approval", "approved", "denied", "executing", "completed", "failed"
	Artifacts  []string      `json:"artifacts"`
	Summary    string        `json:"summary"`
	CreatedAt  int64         `json:"createdAt"`
	ApprovedAt int64         `json:"approvedAt,omitempty"`
	ExecutedAt int64         `json:"executedAt,omitempty"`
	Error      string        `json:"error,omitempty"`
}

// InstallStep represents a single step in an installation plan.
type InstallStep struct {
	Order       int      `json:"order"`
	Description string   `json:"description"`
	Commands    []string `json:"commands"` // SAFE: dry-run only in this implementation
	Risk        string   `json:"risk"`     // per-step risk
	Status      string   `json:"status"`   // "pending", "running", "completed", "failed"
}

// PlanStore provides thread-safe storage for install plans and registered plugin files.
type PlanStore struct {
	mu           sync.RWMutex
	InstallPlans map[string]*InstallPlan
	PluginFiles  map[string][]string // pluginId -> file paths
}

// NewPlanStore creates a PlanStore with empty maps.
func NewPlanStore() *PlanStore {
	return &PlanStore{
		InstallPlans: make(map[string]*InstallPlan),
		PluginFiles:  make(map[string][]string),
	}
}

// ---------------------------------------------------------------------------
// plugin.install / plugin.install.plan
// ---------------------------------------------------------------------------

// pluginInstallPlan generates an install plan for the given plugin.
// If the plugin manifest has an install section, steps are derived from it.
// Otherwise a generic default plan is generated with risk assessment.
func pluginInstallPlan(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	pluginID := extractPluginID(req, corePluginID)

	// Determine risk and build steps.
	// If manifest declares install config, derive from it; else build default plan.
	risk := "high" // default: install involves running commands
	var steps []InstallStep

	if deps.Manifests != nil {
		m, err := deps.Manifests.LoadManifest(pluginID)
		if err == nil && m != nil && m.Core != nil {
			// Collect install hints from environment checks for contextual info.
			// (Future: if a dedicated manifest.Install section is added, read it here.)
			hasInstallHints := false
			for _, check := range m.Core.Environment.Checks {
				if check.InstallHint != "" {
					hasInstallHints = true
					_ = hasInstallHints
				}
			}
		}
	}

	// Build a default install plan.
	steps = []InstallStep{
		{
			Order:       1,
			Description: "Detect binary availability via plugin.check",
			Commands:    []string{"plugin.check"},
			Risk:        "low",
			Status:      "pending",
		},
		{
			Order:       2,
			Description: "Detect package manager (apt / brew / choco)",
			Commands:    []string{"env.which apt", "env.which brew", "env.which choco"},
			Risk:        "low",
			Status:      "pending",
		},
		{
			Order:       3,
			Description: "Install command (DRY-RUN — command is logged but NOT executed)",
			Commands:    []string{"# DRY-RUN: package-manager install " + pluginID},
			Risk:        "high",
			Status:      "pending",
		},
		{
			Order:       4,
			Description: "Register plugin files via plugin.files.register",
			Commands:    []string{"plugin.files.register"},
			Risk:        "low",
			Status:      "pending",
		},
	}

	planID := randomPlanID()
	plan := &InstallPlan{
		PlanID:    planID,
		PluginID:  pluginID,
		Steps:     steps,
		Risk:      risk,
		Status:    "pending_approval",
		Artifacts: []string{},
		Summary:   "Installation plan for " + pluginID + " (dry-run mode — no system commands will be executed)",
		CreatedAt: nowMillis(),
	}

	// Store plan in PlanStore so it can be approved and executed later.
	if deps.Store != nil {
		deps.Store.mu.Lock()
		deps.Store.InstallPlans[planID] = plan
		deps.Store.mu.Unlock()
	}

	return map[string]interface{}{
		"planId":   plan.PlanID,
		"pluginId": plan.PluginID,
		"steps":    plan.Steps,
		"risk":     plan.Risk,
		"status":   plan.Status,
		"artifacts": plan.Artifacts,
		"summary":  plan.Summary,
		"createdAt": plan.CreatedAt,
	}, nil
}

// ---------------------------------------------------------------------------
// plugin.install.execute
// ---------------------------------------------------------------------------

// pluginInstallExecute executes an approved install plan.
// IMPORTANT: This is a DRY-RUN framework. Commands are logged but NOT actually
// executed on the system. The test framework verifies the lifecycle flow.
func pluginInstallExecute(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	var payload struct {
		PlanID   string `json:"planId"`
		PluginID string `json:"pluginId"`
	}
	if req.Payload != nil {
		if err := json.Unmarshal(req.Payload, &payload); err != nil {
			return nil, fmt.Errorf("invalid payload: %w", err)
		}
	}
	// Also accept planId from the request-level field (set during approval).
	if payload.PlanID == "" {
		payload.PlanID = req.PlanID
	}
	if payload.PlanID == "" {
		return nil, fmt.Errorf("planId is required for plugin.install.execute; call plugin.install first")
	}

	if deps.Store == nil {
		return nil, fmt.Errorf("plan store not initialized")
	}

	deps.Store.mu.RLock()
	plan, ok := deps.Store.InstallPlans[payload.PlanID]
	deps.Store.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("plan %q not found", payload.PlanID)
	}

	// Require the plan to be approved before execution.
	if plan.Status != "approved" {
		return map[string]interface{}{
			"status":  "plan_not_approved",
			"message": fmt.Sprintf("plan %q has status %q; must be approved before execution", payload.PlanID, plan.Status),
			"planId":  plan.PlanID,
		}, nil
	}

	// Mark plan as executing.
	deps.Store.mu.Lock()
	plan.Status = "executing"
	plan.ExecutedAt = nowMillis()
	deps.Store.mu.Unlock()

	// Execute each step — DRY-RUN: mark as completed, do NOT run real commands.
	for i := range plan.Steps {
		deps.Store.mu.Lock()
		plan.Steps[i].Status = "running"
		deps.Store.mu.Unlock()

		// DRY-RUN: the command text exists in plan.Steps[i].Commands but is not executed.
		// In a real implementation, deps.Processes.Spawn would be called here.

		deps.Store.mu.Lock()
		plan.Steps[i].Status = "completed"
		deps.Store.mu.Unlock()
	}

	// Mark plan as completed.
	deps.Store.mu.Lock()
	plan.Status = "completed"
	deps.Store.mu.Unlock()

	// Record history event.
	if deps.History != nil {
		deps.History.RecordPluginEvent(plan.PluginID, "plugin.installed", map[string]interface{}{
			"planId":  plan.PlanID,
			"steps":   len(plan.Steps),
			"dryRun":  true,
			"actor":   req.Actor,
		})
	}

	return map[string]interface{}{
		"status":   "completed",
		"planId":   plan.PlanID,
		"pluginId": plan.PluginID,
		"steps":    len(plan.Steps),
		"dryRun":   true,
	}, nil
}

// ---------------------------------------------------------------------------
// plugin.uninstall
// ---------------------------------------------------------------------------

// pluginUninstall handles plugin uninstallation.
// DRY-RUN framework — returns what would be removed; no files or system state
// are actually changed.
func pluginUninstall(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	pluginID := extractPluginID(req, corePluginID)

	// Look up registered files for this plugin.
	var registeredFiles []string
	if deps.Store != nil {
		deps.Store.mu.RLock()
		files := deps.Store.PluginFiles[pluginID]
		registeredFiles = make([]string, len(files))
		copy(registeredFiles, files)
		deps.Store.mu.RUnlock()
	}

	// Clean up stored data for the plugin.
	if deps.Store != nil {
		deps.Store.mu.Lock()
		delete(deps.Store.PluginFiles, pluginID)
		// Also remove any pending/completed install plans for this plugin.
		for id, p := range deps.Store.InstallPlans {
			if p.PluginID == pluginID {
				delete(deps.Store.InstallPlans, id)
			}
		}
		deps.Store.mu.Unlock()
	}

	// Record history event.
	if deps.History != nil {
		deps.History.RecordPluginEvent(pluginID, "plugin.uninstalled", map[string]interface{}{
			"removedFiles": len(registeredFiles),
			"dryRun":       true,
			"actor":        req.Actor,
		})
	}

	return map[string]interface{}{
		"status":          "uninstalled",
		"pluginId":        pluginID,
		"dryRun":          true,
		"registeredFiles": registeredFiles,
		"removedCount":    len(registeredFiles),
	}, nil
}
