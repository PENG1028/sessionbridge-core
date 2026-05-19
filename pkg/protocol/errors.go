package protocol

// Protocol error codes returned by the Core.
const (
	ErrCodeUnauthenticated   = "UNAUTHENTICATED"
	ErrCodePermissionDenied  = "PERMISSION_DENIED"
	ErrCodePluginNotFound    = "PLUGIN_NOT_FOUND"
	ErrCodePluginDisabled    = "PLUGIN_DISABLED"
	ErrCodeCapNotDeclared    = "CAPABILITY_NOT_DECLARED"
	ErrCodeNotGranted        = "NOT_GRANTED"
	ErrCodeNeedApproval      = "NEED_APPROVAL"
	ErrCodePathNotAllowed    = "PATH_NOT_ALLOWED"
	ErrCodeNodeUnreachable   = "NODE_UNREACHABLE"
	ErrCodeForwardError      = "FORWARD_ERROR"
	ErrCodeExecutionError    = "EXECUTION_ERROR"
	ErrCodeInvalidRequest    = "INVALID_REQUEST"
	ErrCodeInternalError     = "INTERNAL_ERROR"
)
