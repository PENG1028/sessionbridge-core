package executor

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/user/sessionnode/go-core/internal/capability"
	"github.com/user/sessionnode/go-core/internal/pluginmanifest"
	"github.com/user/sessionnode/go-core/pkg/types"
)

// SupportedManifestVersion is the manifest format version supported.
const SupportedManifestVersion = "1"

// buildPluginDetail assembles the full plugin detail from manifest data.
func buildPluginDetail(pluginID string, deps *Deps) map[string]interface{} {
	info := map[string]interface{}{
		"id":              pluginID,
		"pluginId":        pluginID,
		"version":         "0.1.0",
		"name":            "SessionNode Go Core",
		"description":     "",
		"enabled":         true,
		"trusted":         false,
		"manifestVersion": SupportedManifestVersion,
	}

	if deps.Manifests != nil {
		m, err := deps.Manifests.LoadManifest(pluginID)
		if err == nil && m != nil {
			info["name"] = m.Name
			info["description"] = m.Description
			info["trusted"] = m.Trusted
			info["manifestVersion"] = m.ManifestVersion
			info["version"] = m.Version

			// Core section — permissions, environment, files, tasks, history
			if m.Core != nil {
				info["core"] = buildCoreSection(m.Core)
			}

			// Adapters section — system-ui, cli
			info["adapters"] = buildAdaptersSection(&m.Adapters)
		}
	}

	return info
}

// buildCoreSection serializes the CoreSpec for frontend consumption.
func buildCoreSection(core *pluginmanifest.CoreSpec) map[string]interface{} { //nolint:govet // intentional type alias
	if core == nil {
		return nil
	}
	out := map[string]interface{}{
		"permissions":  core.Permissions,
		"environment":  core.Environment,
		"files":        core.Files,
		"tasks":        core.Tasks,
		"history":      core.History,
	}
	return out
}

// buildAdaptersSection serializes the AdapterSpec for frontend consumption.
func buildAdaptersSection(adapters *pluginmanifest.AdapterSpec) map[string]interface{} { //nolint:govet // intentional type alias
	if adapters == nil {
		return nil
	}
	out := make(map[string]interface{})
	if adapters.SystemUI != nil {
		sysUI := make(map[string]interface{})
		sysUI["views"] = adapters.SystemUI.Views
		sysUI["panels"] = adapters.SystemUI.Panels
		sysUI["commands"] = adapters.SystemUI.Commands
		sysUI["status"] = adapters.SystemUI.Status
		out["system-ui"] = sysUI
	}
	if adapters.CLI != nil {
		out["cli"] = adapters.CLI
	}
	return out
}

// blockerEntry represents a single readiness blocker for a plugin.
type blockerEntry struct {
	Kind       string `json:"kind"`
	Capability string `json:"capability,omitempty"`
	Dependency string `json:"dependency,omitempty"`
	Reason     string `json:"reason"`
}

// pluginCheck checks whether a plugin's environment dependencies are satisfied
// AND whether its declared capabilities are supported on the current platform.
// Supports check types: binary, env, command, path, file, directory.
func pluginCheck(req *types.CapabilityRequest, deps *Deps) (interface{}, error) {
	pluginID := extractPluginID(req, "sessionnode-core")

	result := map[string]interface{}{
		"pluginId":     pluginID,
		"status":       "ok",
		"checkedAt":    nowMillis(),
		"dependencies": []map[string]interface{}{},
		"capabilities": []interface{}{},
		"blockers":     []interface{}{},
	}

	if deps.Manifests == nil {
		return result, nil
	}

	m, err := deps.Manifests.LoadManifest(pluginID)
	if err != nil || m == nil || m.Core == nil {
		return result, nil
	}

	// ── 1. Dependency checking (preserved exactly as-is) ──
	type depResult struct {
		entry    map[string]interface{}
		required bool
		id       string
	}
	depsList := make([]map[string]interface{}, 0)
	depResults := make([]depResult, 0)
	anyDepNotOK := false

	for _, check := range m.Core.Environment.Checks {
		dep := map[string]interface{}{
			"id":     check.ID,
			"type":   check.Type,
			"status": "skipped",
		}
		if check.Command != "" {
			dep["command"] = check.Command
		}
		if check.Required {
			dep["required"] = true
		}
		if check.VersionCommand != "" {
			dep["versionCommand"] = check.VersionCommand
		}
		if check.RequiredVersion != "" {
			dep["requiredVersion"] = check.RequiredVersion
		}
		if check.InstallHint != "" {
			dep["installHint"] = check.InstallHint
		}

		switch check.Type {
		case "binary":
			dep["status"] = checkBinary(check.Command)
		case "env":
			dep["status"] = checkEnv(check.Command)
		case "command":
			dep["status"] = checkCommand(check.Command, check.Args)
		case "path":
			dep["status"] = checkPath(check.Command, false)
		case "file":
			dep["status"] = checkFile(check.Command)
		case "directory":
			dep["status"] = checkPath(check.Command, true)
		default:
			dep["status"] = "unknown"
		}

		if dep["status"] != "ok" && dep["status"] != "skipped" {
			anyDepNotOK = true
		}
		depResults = append(depResults, depResult{
			entry:    dep,
			required: check.Required,
			id:       check.ID,
		})
		depsList = append(depsList, dep)
	}

	result["dependencies"] = depsList

	// ── 2. Capability support checking ──
	resolver := getResolver(deps)
	capList, capBlockers := buildCapabilityReport(m.Core, pluginID, deps, resolver)

	result["capabilities"] = capList

	// ── 3. Build blockers: combine dep blockers + capability blockers ──
	blockers := capBlockers
	for _, dr := range depResults {
		status := dr.entry["status"].(string)
		if status != "ok" && dr.required {
			blockers = append(blockers, blockerEntry{
				Kind:       "missing_dependency",
				Dependency: dr.id,
				Reason:     mapDepStatusToReason(status),
			})
		}
	}

	result["blockers"] = blockers

	// ── 4. Determine overall status ──
	status := "ok"
	if len(blockers) > 0 {
		status = "blocked"
	} else if anyDepNotOK {
		status = "incomplete"
	}
	result["status"] = status

	return result, nil
}

// getResolver returns the capability resolver from Deps, or nil if none is configured.
// When nil, capability support checking is skipped (graceful degradation).
func getResolver(deps *Deps) *capability.Resolver {
	if deps.CapResolver != nil {
		return deps.CapResolver
	}
	return nil
}

// buildCapabilityReport checks every capability declared in the plugin's
// permissions against the capability resolver and produces:
//   - a capability support list (one entry per capability)
//   - a blocker list for unsupported, unknown, and ungranted capabilities
func buildCapabilityReport(
	core *pluginmanifest.CoreSpec,
	pluginID string,
	deps *Deps,
	resolver *capability.Resolver,
) ([]interface{}, []interface{}) {
	capList := make([]interface{}, 0)
	blockers := make([]interface{}, 0)

	if resolver == nil {
		return capList, blockers
	}

	seen := make(map[string]bool)

	for _, perm := range core.Permissions {
		for _, capName := range perm.Capabilities {
			if seen[capName] {
				continue
			}
			seen[capName] = true

			cs := resolver.CheckCapability(capName)

			capEntry := map[string]interface{}{
				"capability": cs.Capability,
				"supported":  cs.Supported,
				"level":      string(cs.Level),
			}
			if cs.Reason != "" {
				capEntry["reason"] = cs.Reason
			}
			if cs.Detail != "" {
				capEntry["detail"] = cs.Detail
			}
			capList = append(capList, capEntry)

			// Blocker: unsupported capability
			if !cs.Supported {
				blockers = append(blockers, blockerEntry{
					Kind:       "unsupported_capability",
					Capability: capName,
					Reason:     cs.Reason,
				})
			}

			// Blocker: unknown capability (not in support matrix)
			if cs.Level == capability.SupportUnknown {
				blockers = append(blockers, blockerEntry{
					Kind:       "unknown_capability",
					Capability: capName,
					Reason:     cs.Reason,
				})
			}

			// Blocker: missing grant
			if deps.Config != nil {
				grant := deps.Config.PluginGrant(pluginID, capName)
				if grant == nil {
					// No explicit grant — check permission default
					if perm.Default == "deny" || perm.Default == "ask" {
						blockers = append(blockers, blockerEntry{
							Kind:       "missing_grant",
							Capability: capName,
							Reason:     "not_granted",
						})
					}
				} else if grant.Mode == "deny" {
					blockers = append(blockers, blockerEntry{
						Kind:       "missing_grant",
						Capability: capName,
						Reason:     "grant_denied",
					})
				}
			}
		}
	}

	return capList, blockers
}

// mapDepStatusToReason maps a dependency check status to a blocker reason string.
func mapDepStatusToReason(status string) string {
	switch status {
	case "missing":
		return "binary_missing"
	case "type_mismatch":
		return "type_mismatch"
	case "error":
		return "check_error"
	case "unknown":
		return "unknown_check_type"
	default:
		return status
	}
}

// checkBinary reports whether a binary is found on PATH.
func checkBinary(name string) string {
	if name == "" {
		return "skipped"
	}
	_, err := exec.LookPath(name)
	if err != nil {
		return "missing"
	}
	return "ok"
}

// checkEnv reports whether an environment variable is set and non-empty.
func checkEnv(name string) string {
	if name == "" {
		return "skipped"
	}
	if os.Getenv(name) == "" {
		return "missing"
	}
	return "ok"
}

// checkCommand runs a command with a 5-second timeout.
// Returns "ok" on exit code 0, "missing" on any error.
func checkCommand(cmd, args string) string {
	if cmd == "" {
		return "skipped"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c := exec.CommandContext(ctx, cmd, strings.Fields(args)...)
	if err := c.Run(); err != nil {
		return "missing"
	}
	return "ok"
}

// checkPath reports whether a filesystem path exists, optionally requiring it to be a directory.
func checkPath(path string, dir bool) string {
	if path == "" {
		return "skipped"
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "missing"
		}
		return "error"
	}
	if dir && !info.IsDir() {
		return "type_mismatch"
	}
	if !dir && info.IsDir() {
		// path type accepts both files and directories
	}
	return "ok"
}

// checkFile reports whether a file exists and is not a directory.
func checkFile(path string) string {
	if path == "" {
		return "skipped"
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "missing"
		}
		return "error"
	}
	if info.IsDir() {
		return "type_mismatch"
	}
	return "ok"
}
