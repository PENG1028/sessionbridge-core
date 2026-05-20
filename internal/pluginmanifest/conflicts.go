package pluginmanifest

import (
	"fmt"
	"strings"
)

// DetectConflicts checks for conflicts across multiple plugin manifests.
func DetectConflicts(manifests []*Manifest) []Conflict {
	var conflicts []Conflict

	// Track IDs for conflict detection
	type idTracker struct {
		pluginID   string
		pluginName string
	}

	pluginIDs := make(map[string]idTracker)
	cliCommands := make(map[string]idTracker)
	permIDs := make(map[string]idTracker)
	viewIDs := make(map[string]idTracker)
	panelIDs := make(map[string]idTracker)

	for _, m := range manifests {
		if m == nil {
			continue
		}

		// Duplicate plugin ID
		if existing, ok := pluginIDs[m.ID]; ok {
			conflicts = append(conflicts, Conflict{
				Type:     "duplicate-id",
				PluginID: m.ID,
				Field:    "id",
				Value:    m.ID,
				Message:  fmt.Sprintf("duplicate plugin id %q (declared by %q and %q)", m.ID, existing.pluginName, m.Name),
			})
		}
		pluginIDs[m.ID] = idTracker{pluginID: m.ID, pluginName: m.Name}

		// Check adapter commands
		if m.Adapters.CLI != nil {
			for _, cmd := range m.Adapters.CLI.Commands {
				if existing, ok := cliCommands[cmd.ID]; ok {
					conflicts = append(conflicts, Conflict{
						Type:     "duplicate-command",
						PluginID: m.ID,
						Field:    fmt.Sprintf("adapters.cli.commands.%s", cmd.ID),
						Value:    cmd.ID,
						Message:  fmt.Sprintf("duplicate cli command id %q (declared by %q and %q)", cmd.ID, existing.pluginID, m.ID),
					})
				}
				cliCommands[cmd.ID] = idTracker{pluginID: m.ID}
			}
		}

		// Check permissions
		if m.Core != nil {
			for _, p := range m.Core.Permissions {
				if existing, ok := permIDs[p.ID]; ok {
					conflicts = append(conflicts, Conflict{
						Type:     "duplicate-permission",
						PluginID: m.ID,
						Field:    fmt.Sprintf("core.permissions.%s", p.ID),
						Value:    p.ID,
						Message:  fmt.Sprintf("duplicate permission id %q (declared by %q and %q)", p.ID, existing.pluginID, m.ID),
					})
				}
				permIDs[p.ID] = idTracker{pluginID: m.ID}
			}
		}

		// Check system-ui views and panels
		if m.Adapters.SystemUI != nil {
			for _, v := range m.Adapters.SystemUI.Views {
				if existing, ok := viewIDs[v.ID]; ok {
					conflicts = append(conflicts, Conflict{
						Type:     "duplicate-view",
						PluginID: m.ID,
						Field:    fmt.Sprintf("adapters.system-ui.views.%s", v.ID),
						Value:    v.ID,
						Message:  fmt.Sprintf("duplicate view id %q (declared by %q and %q)", v.ID, existing.pluginID, m.ID),
					})
				}
				viewIDs[v.ID] = idTracker{pluginID: m.ID}
			}
			for _, p := range m.Adapters.SystemUI.Panels {
				if existing, ok := panelIDs[p.ID]; ok {
					conflicts = append(conflicts, Conflict{
						Type:     "duplicate-panel",
						PluginID: m.ID,
						Field:    fmt.Sprintf("adapters.system-ui.panels.%s", p.ID),
						Value:    p.ID,
						Message:  fmt.Sprintf("duplicate panel id %q (declared by %q and %q)", p.ID, existing.pluginID, m.ID),
					})
				}
				panelIDs[p.ID] = idTracker{pluginID: m.ID}
			}
		}

		// Plugin declaring reserved namespace
		for reserved := range ReservedPluginIDs {
			if m.ID != reserved && m.Adapters.CLI != nil {
				for _, cmd := range m.Adapters.CLI.Commands {
					if strings.HasPrefix(cmd.ID, reserved+".") {
						conflicts = append(conflicts, Conflict{
							Type:     "namespace-violation",
							PluginID: m.ID,
							Field:    fmt.Sprintf("adapters.cli.commands.%s", cmd.ID),
							Value:    cmd.ID,
							Message:  fmt.Sprintf("plugin %q declares command %q which uses reserved namespace %q", m.ID, cmd.ID, reserved),
						})
					}
				}
			}
			if m.ID != reserved && m.Adapters.SystemUI != nil {
				for _, v := range m.Adapters.SystemUI.Views {
					if strings.HasPrefix(v.ID, reserved+".") {
						conflicts = append(conflicts, Conflict{
							Type:     "namespace-violation",
							PluginID: m.ID,
							Field:    fmt.Sprintf("adapters.system-ui.views.%s", v.ID),
							Value:    v.ID,
							Message:  fmt.Sprintf("plugin %q declares view %q which uses reserved namespace %q", m.ID, v.ID, reserved),
						})
					}
				}
			}
		}
	}

	return conflicts
}
