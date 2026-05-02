package response

// ResponseCode is a stable, machine-readable API response code.
//
// Naming convention:
//   - success codes start with "ok."
//   - error codes start with "error."
//   - format: "<kind>.<domain>.<reason>"
//
// Examples:
//   - ok.job.created
//   - error.param.invalid
//   - error.job.not_found
type ResponseCode string

func (c ResponseCode) String() string {
	return string(c)
}

const (
	// Success codes.
	CodeOK                ResponseCode = "ok"
	CodeJobCreated        ResponseCode = "ok.job.created"
	CodeJobFetched        ResponseCode = "ok.job.fetched"
	CodeJobCreatedPartial ResponseCode = "ok.job.created_partial"
	CodeUserRegistered    ResponseCode = "ok.user.registered"
	CodeUserLoggedIn      ResponseCode = "ok.user.logged_in"

	// Client-side request / validation errors.
	CodeRequestInvalid ResponseCode = "error.request.invalid"
	CodeBodyInvalid    ResponseCode = "error.body.invalid"
	CodeParamInvalid   ResponseCode = "error.param.invalid"
	CodeParamRequired  ResponseCode = "error.param.required"
	CodeParamMalformed ResponseCode = "error.param.malformed"
	CodeUnauthorized   ResponseCode = "error.auth.unauthorized"
	CodeForbidden      ResponseCode = "error.auth.forbidden"
	CodeInvalidCreds   ResponseCode = "error.auth.invalid_credentials"

	// Resource / domain errors.
	CodeJobNotFound      ResponseCode = "error.job.not_found"
	CodeUserExists       ResponseCode = "error.user.already_exists"
	CodeQueueEnqueueFail ResponseCode = "error.queue.enqueue_failed"

	// Infrastructure / server errors.
	CodeDatabaseUnavailable ResponseCode = "error.database.unavailable"
	CodeQueueUnavailable    ResponseCode = "error.queue.unavailable"
	CodeInternal            ResponseCode = "error.internal"
)
