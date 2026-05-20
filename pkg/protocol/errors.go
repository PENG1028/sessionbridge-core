package protocol

// Protocol error codes returned by the Core.
const (
	ErrCodeUnauthenticated   = "UNAUTHENTICATED"
	ErrCodePermissionDenied  = "PERMISSION_DENIED"
	ErrCodePluginNotFound    = "PLUGIN_NOT_FOUND"
	ErrCodePluginDisabled    = "PLUGIN_DISABLED"
	ErrCodeCapNotDeclared    = "CAPABILITY_NOT_DECLARED"
	ErrCodeCapNotSupported   = "CAPABILITY_UNSUPPORTED_ON_PLATFORM"
	ErrCodeNotGranted        = "NOT_GRANTED"
	ErrCodeNeedApproval      = "NEED_APPROVAL"
	ErrCodePathNotAllowed    = "PATH_NOT_ALLOWED"
	ErrCodeNodeNotAllowed    = "NODE_NOT_ALLOWED"
	ErrCodeNodeUnreachable   = "NODE_UNREACHABLE"
	ErrCodeForwardError      = "FORWARD_ERROR"
	ErrCodeExecutionError    = "EXECUTION_ERROR"
	ErrCodeInvalidRequest    = "INVALID_REQUEST"
	ErrCodeInternalError     = "INTERNAL_ERROR"
	ErrCodePlanRequired      = "PLAN_REQUIRED"
	ErrCodePlanFailed        = "PLAN_FAILED"
	ErrCodeApprovalRequired  = "APPROVAL_REQUIRED"
	ErrCodeApprovalDenied    = "APPROVAL_DENIED"
)
