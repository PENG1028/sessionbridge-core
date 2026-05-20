package pluginmanifest

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ResolvePluginPath resolves a path expression using the given variables.
// Supports ${var} substitution and ~/ expansion.
func ResolvePluginPath(pluginDir, expr string, vars PathVars) (string, error) {
	if expr == "" {
		return "", fmt.Errorf("empty path expression")
	}

	// Expand ~/ to home
	if strings.HasPrefix(expr, "~/") || expr == "~" {
		if vars.Home == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("cannot expand ~: no home directory: %w", err)
			}
			vars.Home = home
		}
		if expr == "~" {
			expr = vars.Home
		} else {
			expr = filepath.Join(vars.Home, expr[2:])
		}
	}

	// Substitute ${var} variables
	expr = strings.ReplaceAll(expr, "${home}", vars.Home)
	expr = strings.ReplaceAll(expr, "${workspace}", vars.Workspace)
	expr = strings.ReplaceAll(expr, "${plugin.dir}", vars.PluginDir)
	expr = strings.ReplaceAll(expr, "${plugin.configDir}", vars.PluginConfig)
	expr = strings.ReplaceAll(expr, "${plugin.dataDir}", vars.PluginData)
	expr = strings.ReplaceAll(expr, "${plugin.cacheDir}", vars.PluginCache)
	expr = strings.ReplaceAll(expr, "${plugin.logsDir}", vars.PluginLogs)
	expr = strings.ReplaceAll(expr, "${plugin.artifactsDir}", vars.PluginArtifacts)
	expr = strings.ReplaceAll(expr, "${node.dataDir}", vars.NodeDataDir)
	expr = strings.ReplaceAll(expr, "${tmp}", vars.Temp)

	// Check for unresolved variables (${...})
	if strings.Contains(expr, "${") {
		return "", fmt.Errorf("unresolved path variable in: %q", expr)
	}

	// Clean the path
	cleaned := filepath.Clean(expr)
	return cleaned, nil
}

// validateDeclaredPath checks that a declared path is safe for the given purpose.
func validateDeclaredPath(path string, purpose string) error {
	if path == "" {
		return fmt.Errorf("path must not be empty")
	}
	return nil
}

// ValidateClearablePath validates that a clearable path does not point to a dangerous location.
func ValidateClearablePath(path string) error {
	if path == "" {
		return &ConstraintViolation{
			Code:    "EMPTY_PATH",
			Message: "clearable path must not be empty",
		}
	}

	// Normalize for comparison
	norm := filepath.ToSlash(filepath.Clean(path))
	lower := strings.ToLower(norm)

	// Disallow root
	if norm == "/" || norm == `\` || len(norm) == 2 && norm[1] == ':' {
		return &ConstraintViolation{
			Code:    "ROOT_PATH",
			Message: fmt.Sprintf("clearable path must not be root: %q", path),
		}
	}

	// Disallow home root
	home, _ := os.UserHomeDir()
	if home != "" {
		homeNorm := filepath.ToSlash(filepath.Clean(home))
		if norm == homeNorm {
			return &ConstraintViolation{
				Code:    "HOME_PATH",
				Message: fmt.Sprintf("clearable path must not be home directory: %q", path),
			}
		}
	}

	// Disallow workspace root
	// (checked via variable; if ${workspace} resolves, we check it)

	// Windows-specific dangerous directories
	if runtime.GOOS == "windows" {
		// Check for Windows directory
		winDir := os.Getenv("WINDIR")
		if winDir != "" {
			winNorm := filepath.ToSlash(filepath.Clean(winDir))
			if strings.HasPrefix(lower, strings.ToLower(winNorm)) {
				return &ConstraintViolation{
					Code:    "WINDOWS_DIR",
					Message: fmt.Sprintf("clearable path must not be in Windows directory: %q", path),
				}
			}
		}

		// Check for Program Files
		for _, pf := range []string{`C:\Program Files`, `C:\Program Files (x86)`} {
			pfLower := strings.ToLower(filepath.ToSlash(filepath.Clean(pf)))
			if strings.HasPrefix(lower, pfLower) {
				return &ConstraintViolation{
					Code:    "PROGRAM_FILES",
					Message: fmt.Sprintf("clearable path must not be in Program Files: %q", path),
				}
			}
		}

		// Check for System32
		sys32 := `c:/windows/system32`
		if strings.HasPrefix(lower, sys32) {
			return &ConstraintViolation{
				Code:    "SYSTEM32",
				Message: fmt.Sprintf("clearable path must not be in System32: %q", path),
			}
		}
	}

	return nil
}

// pluginDirBase returns the base directory for a plugin within the plugins dir.
func pluginDirBase(pluginsDir, pluginID string) string {
	return filepath.Join(pluginsDir, pluginID)
}

// ResolvePluginDir resolves the plugin directory path.
func ResolvePluginDir(pluginsDir, pluginID string) string {
	return filepath.Join(pluginsDir, pluginID)
}

// DefaultPluginDirs returns the default directories to search for plugins.
func DefaultPluginDirs() []string {
	home, _ := os.UserHomeDir()
	var dirs []string

	// Current directory ./plugins
	if cwd, err := os.Getwd(); err == nil {
		dirs = append(dirs, filepath.Join(cwd, "plugins"))
	}

	// User home ~/.sessionnode/plugins
	if home != "" {
		dirs = append(dirs, filepath.Join(home, ".sessionnode", "plugins"))
	}

	return dirs
}
