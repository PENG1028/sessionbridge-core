package pluginmanifest

import (
	"fmt"
	"regexp"
	"strings"
)

// versionPattern is a rough semver match.
var versionPattern = regexp.MustCompile(`^\d+\.\d+\.\d+`)

const maxCommands = 2000
const maxSettingsDepth = 20

// Validate checks a manifest for structural correctness.
func Validate(m *Manifest) []ValidationError {
	var errs []ValidationError

	if m == nil {
		errs = append(errs, ValidationError{Field: "manifest", Code: "NIL", Message: "manifest is nil"})
		return errs
	}

	// manifestVersion
	if m.ManifestVersion == "" {
		errs = append(errs, ValidationError{Field: "manifestVersion", Code: "REQUIRED", Message: "manifestVersion is required"})
	} else if !IsSupportedManifestVersion(m.ManifestVersion) {
		errs = append(errs, ValidationError{Field: "manifestVersion", Code: "UNSUPPORTED", Message: fmt.Sprintf("unsupported manifestVersion: %s", m.ManifestVersion)})
	}

	// id
	if m.ID == "" {
		errs = append(errs, ValidationError{Field: "id", Code: "REQUIRED", Message: "id is required"})
	} else {
		if !isKebabCase(m.ID) {
			errs = append(errs, ValidationError{Field: "id", Code: "INVALID_FORMAT", Message: fmt.Sprintf("plugin id must be kebab-case: %q", m.ID)})
		}
		if ReservedPluginIDs[m.ID] {
			errs = append(errs, ValidationError{Field: "id", Code: "RESERVED", Message: fmt.Sprintf("plugin id %q is reserved", m.ID)})
		}
	}

	// version
	if m.Version == "" {
		errs = append(errs, ValidationError{Field: "version", Code: "REQUIRED", Message: "version is required"})
	} else if !versionPattern.MatchString(m.Version) {
		errs = append(errs, ValidationError{Field: "version", Code: "INVALID_VERSION", Message: fmt.Sprintf("version must be semver-like: %q", m.Version)})
	}

	// core section required
	if m.Core == nil {
		errs = append(errs, ValidationError{Field: "core", Code: "REQUIRED", Message: "core section is required"})
	} else {
		errs = append(errs, validateCore(m.Core, m.ID, m.Trusted)...)
	}

	// adapters (both optional)
	errs = append(errs, validateAdapters(m, &m.Adapters)...)

	return errs
}

func validateCore(core *CoreSpec, pluginID string, trusted bool) []ValidationError {
	var errs []ValidationError

	// Permissions
	permIDs := make(map[string]bool)
	for i, p := range core.Permissions {
		if p.ID == "" {
			errs = append(errs, ValidationError{Field: fmt.Sprintf("core.permissions[%d].id", i), Code: "REQUIRED", Message: "permission id is required"})
			continue
		}

		// Namespace check
		if !isNamespacePrefix(pluginID, p.ID) {
			errs = append(errs, ValidationError{
				Field:   fmt.Sprintf("core.permissions[%d].id", i),
				Code:    "NAMESPACE",
				Message: fmt.Sprintf("permission id %q must start with %q", p.ID, pluginID+"."),
			})
		}

		if permIDs[p.ID] {
			errs = append(errs, ValidationError{
				Field:   fmt.Sprintf("core.permissions[%d].id", i),
				Code:    "DUPLICATE",
				Message: fmt.Sprintf("duplicate permission id: %q", p.ID),
			})
		}
		permIDs[p.ID] = true

		// Capabilities
		for j, capName := range p.Capabilities {
			if capName == "" {
				errs = append(errs, ValidationError{
					Field:   fmt.Sprintf("core.permissions[%d].capabilities[%d]", i, j),
					Code:    "EMPTY",
					Message: "capability name must not be empty",
				})
				continue
			}
			if !KnownCapabilities[capName] {
				errs = append(errs, ValidationError{
					Field:   fmt.Sprintf("core.permissions[%d].capabilities[%d]", i, j),
					Code:    "UNKNOWN_CAPABILITY",
					Message: fmt.Sprintf("unknown capability: %q", capName),
				})
			}
		}

		// Default value
		if p.Default != "" && !ValidPermissionDefaults[p.Default] {
			errs = append(errs, ValidationError{
				Field:   fmt.Sprintf("core.permissions[%d].default", i),
				Code:    "INVALID_DEFAULT",
				Message: fmt.Sprintf("invalid default value: %q (must be ask, deny, or allow)", p.Default),
			})
		}

		// Dangerous capability check
		if p.Default == DefaultAllow {
			for _, capName := range p.Capabilities {
				if DangerousCapabilities[capName] && !trusted {
					errs = append(errs, ValidationError{
						Field:   fmt.Sprintf("core.permissions[%d].default", i),
						Code:    "DANGEROUS_DEFAULT_ALLOW",
						Message: fmt.Sprintf("dangerous capability %q cannot have default: allow; plugin must be trusted or use ask/deny", capName),
					})
				}
			}
		}

		// process.spawn requires description
		for _, capName := range p.Capabilities {
			if capName == "process.spawn" && p.Description == "" {
				errs = append(errs, ValidationError{
					Field:   fmt.Sprintf("core.permissions[%d].description", i),
					Code:    "REQUIRED",
					Message: "process.spawn permission requires a description",
				})
			}
		}

		// fs.write requires path constraints
		for _, capName := range p.Capabilities {
			if (capName == "fs.write" || capName == "fs.delete") && p.Description == "" {
				errs = append(errs, ValidationError{
					Field:   fmt.Sprintf("core.permissions[%d].description", i),
					Code:    "REQUIRED",
					Message: fmt.Sprintf("%s permission requires a description", capName),
				})
			}
		}

		// fs.delete requires path constraints and default ask/deny
		for _, capName := range p.Capabilities {
			if capName == "fs.delete" {
				if p.Constraints == nil || p.Constraints.Paths == nil {
					errs = append(errs, ValidationError{
						Field:   fmt.Sprintf("core.permissions[%d].constraints.paths", i),
						Code:    "REQUIRED",
						Message: "fs.delete permission requires path constraints",
					})
				}
				if p.Default == DefaultAllow {
					errs = append(errs, ValidationError{
						Field:   fmt.Sprintf("core.permissions[%d].default", i),
						Code:    "FS_DELETE_NO_ALLOW",
						Message: "fs.delete permission must have default: ask or deny (not allow)",
					})
				}
			}
		}

		// plugin.install.execute requires planRequired task
		for _, capName := range p.Capabilities {
			if capName == "plugin.install.execute" {
				hasPlanTask := false
				for _, t := range core.Tasks {
					if t.PlanRequired {
						hasPlanTask = true
						break
					}
				}
				if !hasPlanTask {
					errs = append(errs, ValidationError{
						Field:   fmt.Sprintf("core.permissions[%d].capabilities", i),
						Code:    "PLAN_REQUIRED",
						Message: "plugin.install.execute requires a planRequired task",
					})
				}
			}
		}
	}

	// Environment checks
	checkIDs := make(map[string]bool)
	for i, c := range core.Environment.Checks {
		if c.ID == "" {
			errs = append(errs, ValidationError{
				Field:   fmt.Sprintf("core.environment.checks[%d].id", i),
				Code:    "REQUIRED",
				Message: "environment check id is required",
			})
			continue
		}
		if checkIDs[c.ID] {
			errs = append(errs, ValidationError{
				Field:   fmt.Sprintf("core.environment.checks[%d].id", i),
				Code:    "DUPLICATE",
				Message: fmt.Sprintf("duplicate environment check id: %q", c.ID),
			})
		}
		checkIDs[c.ID] = true

		switch c.Type {
		case "binary":
			if c.Command == "" {
				errs = append(errs, ValidationError{
					Field:   fmt.Sprintf("core.environment.checks[%d].command", i),
					Code:    "REQUIRED",
					Message: "binary check requires a command",
				})
			}
		case "command":
			if c.Command == "" && c.Args == "" {
				errs = append(errs, ValidationError{
					Field:   fmt.Sprintf("core.environment.checks[%d]", i),
					Code:    "REQUIRED",
					Message: "command check requires command or args",
				})
			}
		}

		if c.RequiredVersion != "" && c.VersionCommand == "" {
			errs = append(errs, ValidationError{
				Field:   fmt.Sprintf("core.environment.checks[%d].versionCommand", i),
				Code:    "RECOMMENDED",
				Message: "requiredVersion set but versionCommand is empty",
			})
		}
	}

	// Files validation
	for i, f := range core.Files.Declarations {
		if f.ID == "" {
			errs = append(errs, ValidationError{
				Field:   fmt.Sprintf("core.files.declarations[%d].id", i),
				Code:    "REQUIRED",
				Message: "file declaration id is required",
			})
		}
		if f.Path == "" {
			errs = append(errs, ValidationError{
				Field:   fmt.Sprintf("core.files.declarations[%d].path", i),
				Code:    "REQUIRED",
				Message: "file declaration path is required",
			})
		}
	}

	// Tasks validation
	taskIDs := make(map[string]bool)
	for i, t := range core.Tasks {
		if t.ID == "" {
			errs = append(errs, ValidationError{
				Field:   fmt.Sprintf("core.tasks[%d].id", i),
				Code:    "REQUIRED",
				Message: "task id is required",
			})
			continue
		}
		if taskIDs[t.ID] {
			errs = append(errs, ValidationError{
				Field:   fmt.Sprintf("core.tasks[%d].id", i),
				Code:    "DUPLICATE",
				Message: fmt.Sprintf("duplicate task id: %q", t.ID),
			})
		}
		taskIDs[t.ID] = true
	}

	return errs
}

func validateAdapters(m *Manifest, adapters *AdapterSpec) []ValidationError {
	var errs []ValidationError

	// System UI adapter (optional)
	if adapters.SystemUI != nil {
		// Views
		for i, v := range adapters.SystemUI.Views {
			if v.ID == "" {
				errs = append(errs, ValidationError{Field: fmt.Sprintf("adapters.system-ui.views[%d].id", i), Code: "REQUIRED", Message: "view id is required"})
			} else if !isNamespacePrefix(m.ID, v.ID) {
				errs = append(errs, ValidationError{
					Field: fmt.Sprintf("adapters.system-ui.views[%d].id", i), Code: "NAMESPACE",
					Message: fmt.Sprintf("view id %q must start with %q", v.ID, m.ID+"."),
				})
			}
			if v.Entry != "" {
				errs = append(errs, validateEntryPath(v.Entry, fmt.Sprintf("adapters.system-ui.views[%d].entry", i))...)
			}
			if v.Type != "" && v.Type != "custom-react" && v.Type != "host-rendered" {
				errs = append(errs, ValidationError{
					Field: fmt.Sprintf("adapters.system-ui.views[%d].type", i), Code: "INVALID_TYPE",
					Message: fmt.Sprintf("invalid view type: %q (must be custom-react or host-rendered)", v.Type),
				})
			}
		}

		// Panels
		for i, p := range adapters.SystemUI.Panels {
			if p.ID == "" {
				errs = append(errs, ValidationError{Field: fmt.Sprintf("adapters.system-ui.panels[%d].id", i), Code: "REQUIRED", Message: "panel id is required"})
			} else if !isNamespacePrefix(m.ID, p.ID) {
				errs = append(errs, ValidationError{
					Field: fmt.Sprintf("adapters.system-ui.panels[%d].id", i), Code: "NAMESPACE",
					Message: fmt.Sprintf("panel id %q must start with %q", p.ID, m.ID+"."),
				})
			}
			if p.Entry != "" {
				errs = append(errs, validateEntryPath(p.Entry, fmt.Sprintf("adapters.system-ui.panels[%d].entry", i))...)
			}
		}

		// Commands
		for i, c := range adapters.SystemUI.Commands {
			if c.ID == "" {
				errs = append(errs, ValidationError{Field: fmt.Sprintf("adapters.system-ui.commands[%d].id", i), Code: "REQUIRED", Message: "command id is required"})
			} else if !isNamespacePrefix(m.ID, c.ID) {
				errs = append(errs, ValidationError{
					Field: fmt.Sprintf("adapters.system-ui.commands[%d].id", i), Code: "NAMESPACE",
					Message: fmt.Sprintf("command id %q must start with %q", c.ID, m.ID+"."),
				})
			}
		}

		// Status items
		for i, s := range adapters.SystemUI.Status {
			if s.ID == "" {
				errs = append(errs, ValidationError{Field: fmt.Sprintf("adapters.system-ui.status[%d].id", i), Code: "REQUIRED", Message: "status item id is required"})
			}
		}

		// Reject commands over limit
		if len(adapters.SystemUI.Commands) > maxCommands {
			errs = append(errs, ValidationError{
				Field: "adapters.system-ui.commands", Code: "TOO_MANY",
				Message: fmt.Sprintf("too many commands (%d, max %d)", len(adapters.SystemUI.Commands), maxCommands),
			})
		}
	}

	// CLI adapter (optional)
	if adapters.CLI != nil {
		for i, c := range adapters.CLI.Commands {
			if c.ID == "" {
				errs = append(errs, ValidationError{Field: fmt.Sprintf("adapters.cli.commands[%d].id", i), Code: "REQUIRED", Message: "cli command id is required"})
			} else if !isNamespacePrefix(m.ID, c.ID) {
				errs = append(errs, ValidationError{
					Field: fmt.Sprintf("adapters.cli.commands[%d].id", i), Code: "NAMESPACE",
					Message: fmt.Sprintf("cli command id %q must start with %q", c.ID, m.ID+"."),
				})
			}

			// Check forbidden pattern: declaring another plugin's namespace
			for reserved := range ReservedPluginIDs {
				if strings.HasPrefix(c.ID, reserved+".") && !strings.HasPrefix(c.ID, m.ID+".") {
					errs = append(errs, ValidationError{
						Field: fmt.Sprintf("adapters.cli.commands[%d].id", i), Code: "RESERVED_NAMESPACE",
						Message: fmt.Sprintf("cli command id %q uses reserved namespace %q", c.ID, reserved),
					})
				}
			}
		}

		if len(adapters.CLI.Commands) > maxCommands {
			errs = append(errs, ValidationError{
				Field: "adapters.cli.commands", Code: "TOO_MANY",
				Message: fmt.Sprintf("too many cli commands (%d, max %d)", len(adapters.CLI.Commands), maxCommands),
			})
		}
	}

	return errs
}

// validateEntryPath checks that an entry path is safe (relative, no escape).
func validateEntryPath(entry, field string) []ValidationError {
	var errs []ValidationError
	if strings.HasPrefix(entry, "/") || strings.HasPrefix(entry, "\\") || (len(entry) > 1 && entry[1] == ':') {
		errs = append(errs, ValidationError{
			Field: field, Code: "ABSOLUTE_PATH",
			Message: fmt.Sprintf("entry path must be relative, got absolute: %q", entry),
		})
	}
	if strings.Contains(entry, "..") {
		errs = append(errs, ValidationError{
			Field: field, Code: "PATH_ESCAPE",
			Message: fmt.Sprintf("entry path must not contain '..': %q", entry),
		})
	}
	return errs
}
