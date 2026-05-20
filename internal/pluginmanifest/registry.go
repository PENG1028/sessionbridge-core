package pluginmanifest

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// PluginSummary is a lightweight description of a discovered plugin.
type PluginSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Type        string `json:"type"`
	Trusted     bool   `json:"trusted"`
	Enabled     bool   `json:"enabled"`
	Description string `json:"description,omitempty"`
	Error       string `json:"error,omitempty"`
}

// pluginEntry holds the manifest and any validation errors for a discovered plugin.
type pluginEntry struct {
	manifest *Manifest
	errs     []ValidationError
}

// PluginRegistry scans directories for plugin manifests and provides
// manifest data to capability handlers.  Implements executor.ManifestLoader.
type PluginRegistry struct {
	plugins   map[string]*pluginEntry
	enabled   map[string]bool
	conflicts []Conflict
}

// NewPluginRegistry creates a registry by scanning the given directories
// for plugin manifests.  disabled lists plugin IDs that are administratively
// disabled — their manifests are loaded but reported as not enabled.
func NewPluginRegistry(dirs []string, disabled []string) *PluginRegistry {
	r := &PluginRegistry{
		plugins: make(map[string]*pluginEntry),
		enabled: make(map[string]bool),
	}
	r.scan(dirs, disabled)
	return r
}

// scan walks all plugin directories, discovers and validates manifests.
func (r *PluginRegistry) scan(dirs []string, disabled []string) {
	disabledSet := make(map[string]bool, len(disabled))
	for _, id := range disabled {
		disabledSet[id] = true
	}

	var allManifests []*Manifest

	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			log.Printf("[plugin] skipping plugin dir %s: %v", dir, err)
			continue
		}

		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			pluginID := entry.Name()

			// Check for duplicate ID from a previous directory.
			if _, exists := r.plugins[pluginID]; exists {
				continue // first directory wins
			}

			manifestPath := filepath.Join(dir, pluginID, "plugin.yaml")
			manifest, errs := r.loadPluginManifest(manifestPath, pluginID)
			// Fallback to plugin.json
			if manifest == nil {
				manifestPath = filepath.Join(dir, pluginID, "plugin.json")
				manifest, errs = r.loadPluginManifest(manifestPath, pluginID)
			}

			if manifest == nil {
				// No manifest file found in this directory — skip.
				continue
			}

			// If the manifest loaded but its ID doesn't match the directory
			// name, use the directory name as the canonical ID.
			if manifest.ID == "" {
				manifest.ID = pluginID
			}

			// First directory wins: skip if already registered via another path.
			if _, exists := r.plugins[manifest.ID]; exists {
				continue
			}

			r.plugins[manifest.ID] = &pluginEntry{
				manifest: manifest,
				errs:     errs,
			}
			r.enabled[manifest.ID] = !disabledSet[manifest.ID]
			allManifests = append(allManifests, manifest)
		}
	}

	// Detect cross-plugin conflicts.
	r.conflicts = DetectConflicts(allManifests)
	for _, c := range r.conflicts {
		log.Printf("[plugin] conflict: %s", c.Message)
	}
}

// loadPluginManifest loads and validates a single manifest file.
// Returns the manifest and any validation errors.  If the file doesn't
// exist both returns are nil (caller should try another path).
func (r *PluginRegistry) loadPluginManifest(path, pluginID string) (*Manifest, []ValidationError) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, nil
	}

	manifest, err := LoadFile(path)
	if err != nil {
		log.Printf("[plugin] %s: load error: %v (plugin %q will not be available)", path, err, pluginID)
		// Record a synthetic validation error and return a minimal manifest
		// so the plugin appears in listings with an error status.
		minimal := &Manifest{ID: pluginID, Name: pluginID, Version: "0.0.0"}
		errs := []ValidationError{
			{Field: "file", Code: "LOAD_ERROR", Message: fmt.Sprintf("load %s: %v", path, err)},
		}
		return minimal, errs
	}

	errs := Validate(manifest)
	if len(errs) > 0 {
		log.Printf("[plugin] %s: %d validation error(s)", path, len(errs))
		for _, ve := range errs {
			log.Printf("[plugin]   - [%s] %s: %s", ve.Code, ve.Field, ve.Message)
		}
	}

	return manifest, errs
}

// LoadManifest returns the manifest for a plugin, or an error if the
// plugin was not found or had fatal validation errors.
func (r *PluginRegistry) LoadManifest(pluginID string) (*Manifest, error) {
	entry, ok := r.plugins[pluginID]
	if !ok {
		return nil, fmt.Errorf("plugin not found: %s", pluginID)
	}
	if len(entry.errs) > 0 && entry.manifest.Version == "0.0.0" && entry.manifest.Name == pluginID {
		// Synthetic minimal manifest from a load error — don't return it.
		return nil, fmt.Errorf("plugin %s has load errors: %s", pluginID, entry.errs[0].Message)
	}
	return entry.manifest, nil
}

// ListPlugins returns a summary of all discovered plugins.
func (r *PluginRegistry) ListPlugins() []PluginSummary {
	out := make([]PluginSummary, 0, len(r.plugins))
	for id, entry := range r.plugins {
		s := PluginSummary{
			ID:          id,
			Name:        entry.manifest.Name,
			Version:     entry.manifest.Version,
			Type:        entry.manifest.Type,
			Trusted:     entry.manifest.Trusted,
			Enabled:     r.enabled[id],
			Description: entry.manifest.Description,
		}
		if len(entry.errs) > 0 {
			s.Error = entry.errs[0].Message
		}
		out = append(out, s)
	}
	return out
}

// PluginEnabled returns whether the given plugin is administratively enabled.
// Unknown plugins return false.
func (r *PluginRegistry) PluginEnabled(pluginID string) bool {
	return r.enabled[pluginID]
}

// ValidationErrors returns the validation errors for a plugin, if any.
func (r *PluginRegistry) ValidationErrors(pluginID string) []ValidationError {
	entry, ok := r.plugins[pluginID]
	if !ok {
		return nil
	}
	return entry.errs
}

// AllConflicts returns all cross-plugin conflicts detected during scanning.
func (r *PluginRegistry) AllConflicts() []Conflict {
	return r.conflicts
}

// CapabilityMap builds a map of pluginID → capability list from all
// registered manifests.  This can be used to build the permission
// checker's capability registry dynamically.
func (r *PluginRegistry) CapabilityMap() map[string][]string {
	out := make(map[string][]string)
	for id, entry := range r.plugins {
		if len(entry.errs) > 0 {
			continue // skip errored plugins
		}
		if !r.enabled[id] {
			continue // skip disabled plugins
		}
		if entry.manifest.Core == nil {
			continue
		}
		var caps []string
		seen := make(map[string]bool)
		for _, perm := range entry.manifest.Core.Permissions {
			for _, c := range perm.Capabilities {
				if !seen[c] {
					caps = append(caps, c)
					seen[c] = true
				}
			}
		}
		if len(caps) > 0 {
			out[id] = caps
		}
	}
	return out
}

// ScanDirs returns the default plugin directories from environment.
// Order: SESSIONNODE_PLUGIN_DIRS env var (colon-separated),
// then ~/.sessionnode/plugins.
func ScanDirs(configured []string) []string {
	if len(configured) > 0 {
		return configured
	}
	// Check env var.
	env := os.Getenv("SESSIONNODE_PLUGIN_DIRS")
	if env != "" {
		return strings.Split(env, string(os.PathListSeparator))
	}
	// Fall back to DefaultPluginDirs from paths.go.
	return DefaultPluginDirs()
}
