package pluginmanifest

// ValidationError represents a single validation failure.
type ValidationError struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e ValidationError) Error() string {
	return e.Message
}

// Conflict represents a conflict between two plugin manifests.
type Conflict struct {
	Type     string `json:"type"`     // "duplicate-id" | "reserved-id" | "namespace-violation"
	PluginID string `json:"pluginId" json:"pluginId"`
	Field    string `json:"field"`
	Value    string `json:"value"`
	Message  string `json:"message"`
}

func (c Conflict) Error() string {
	return c.Message
}

// ConstraintViolation is returned when a path or permission violates safety rules.
type ConstraintViolation struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (c ConstraintViolation) Error() string {
	return c.Message
}

// ValidationErrorList is a collection of validation errors.
type ValidationErrorList []ValidationError

func (l ValidationErrorList) Error() string {
	if len(l) == 0 {
		return "no errors"
	}
	return l[0].Message
}

func (l ValidationErrorList) HasErrors() bool {
	return len(l) > 0
}

// ErrUnsupportedManifestVersion is used when manifestVersion is not supported.
var ErrUnsupportedManifestVersion = &ConstraintViolation{
	Code:    "UNSUPPORTED_MANIFEST_VERSION",
	Message: "unsupported manifest version",
}

// ErrReservedPluginID is used when a plugin attempts to use a reserved ID.
var ErrReservedPluginID = &ConstraintViolation{
	Code:    "RESERVED_PLUGIN_ID",
	Message: "plugin ID is reserved",
}

// ErrInvalidIDFormat is used when an ID doesn't match kebab-case rules.
var ErrInvalidIDFormat = &ConstraintViolation{
	Code:    "INVALID_ID_FORMAT",
	Message: "ID must be kebab-case (lowercase letters, digits, hyphens)",
}

// ErrNamespaceViolation is used when a plugin declares an ID outside its namespace.
var ErrNamespaceViolation = &ConstraintViolation{
	Code:    "NAMESPACE_VIOLATION",
	Message: "declared ID must be namespaced with the plugin ID",
}

// ErrPathEscape is used when a path attempts to escape the plugin directory.
var ErrPathEscape = &ConstraintViolation{
	Code:    "PATH_ESCAPE",
	Message: "path must not escape plugin directory",
}

// ErrAbsolutePath is used when a relative path is expected but absolute is given.
var ErrAbsolutePath = &ConstraintViolation{
	Code:    "ABSOLUTE_PATH",
	Message: "absolute paths not allowed for entry files; use relative path",
}

// ErrClearableDangerousPath is used when a clearable path points to a dangerous location.
var ErrClearableDangerousPath = &ConstraintViolation{
	Code:    "CLEARABLE_DANGEROUS_PATH",
	Message: "clearable path points to a dangerous system directory",
}

// ErrDangerousDefaultAllow is used when a dangerous capability is set to default allow.
var ErrDangerousDefaultAllow = &ConstraintViolation{
	Code:    "DANGEROUS_DEFAULT_ALLOW",
	Message: "dangerous capability cannot have default: allow without explicit override",
}

// ErrMissingCore is returned when the manifest has no core section.
var ErrMissingCore = &ConstraintViolation{
	Code:    "MISSING_CORE",
	Message: "manifest must have a core section",
}
