package response

// ResponseCode is a stable, machine-readable API response code.
//
// Every success is "ok": the route and the HTTP status already say what
// happened. Errors are "error.<domain>.<reason>", e.g. error.param.invalid
// or error.job.not_found; clients tell errors apart by them.
type ResponseCode string

func (c ResponseCode) String() string {
	return string(c)
}

const (
	// CodeOK is the code of every success.
	CodeOK ResponseCode = "ok"

	// Client-side request / validation errors.
	CodeRequestInvalid ResponseCode = "error.request.invalid"
	CodeBodyInvalid    ResponseCode = "error.body.invalid"
	CodeParamInvalid   ResponseCode = "error.param.invalid"
	CodeParamRequired  ResponseCode = "error.param.required"
	CodeParamMalformed ResponseCode = "error.param.malformed"
	// CodeBodyTooLarge: the request body exceeds the server's body limit.
	CodeBodyTooLarge ResponseCode = "error.body.too_large"
	// CodeRouteNotFound: no route matches the path.
	CodeRouteNotFound ResponseCode = "error.route.not_found"
	// CodeMethodNotAllowed: the path exists, but not for this method (see Allow).
	CodeMethodNotAllowed ResponseCode = "error.route.method_not_allowed"
	CodeUnauthorized     ResponseCode = "error.auth.unauthorized"
	CodeForbidden        ResponseCode = "error.auth.forbidden"
	CodeInvalidCreds     ResponseCode = "error.auth.invalid_credentials"
	// CodeOriginRejected: a state-changing /api request without the
	// frontend's Origin header (cross-site request protection).
	CodeOriginRejected ResponseCode = "error.auth.origin_rejected"
	// CodeInsufficientScope: the API key is valid but lacks the route's scope.
	CodeInsufficientScope ResponseCode = "error.auth.insufficient_scope"

	// Resource / domain errors.
	CodeJobNotFound      ResponseCode = "error.job.not_found"
	CodeTaskNotFound     ResponseCode = "error.task.not_found"
	CodeUserExists       ResponseCode = "error.user.already_exists"
	CodeQueueEnqueueFail ResponseCode = "error.queue.enqueue_failed"
	CodeFileNotFound     ResponseCode = "error.file.not_found"
	CodeWorkflowNotFound ResponseCode = "error.workflow.not_found"
	CodeAPIKeyNotFound   ResponseCode = "error.api_key.not_found"
	CodeAPIKeyLimit      ResponseCode = "error.api_key.limit_reached"
	// CodeIdempotencyConflict: the Idempotency-Key was used with a different request.
	CodeIdempotencyConflict ResponseCode = "error.idempotency.conflict"
	// CodeTaskConflict: the task's state does not allow the operation (e.g. retrying an active job).
	CodeTaskConflict ResponseCode = "error.task.conflict"
	// CodeFileConflict: the file's state does not allow the operation (e.g. deleting the source of an unfinished job).
	CodeFileConflict ResponseCode = "error.file.conflict"
	// CodeFileGone: the file has expired or was deleted; its content is no longer stored.
	CodeFileGone ResponseCode = "error.file.gone"
	// CodeQuotaExceeded: the request would exceed the user's storage or
	// monthly processing quota; details name the quota.
	CodeQuotaExceeded ResponseCode = "error.quota.exceeded"
	// CodeRateLimited: too many write requests; retry after Retry-After.
	CodeRateLimited ResponseCode = "error.rate_limit.exceeded"

	// Infrastructure / server errors.
	CodeDatabaseUnavailable ResponseCode = "error.database.unavailable"
	CodeQueueUnavailable    ResponseCode = "error.queue.unavailable"
	CodeInternal            ResponseCode = "error.internal"
)
