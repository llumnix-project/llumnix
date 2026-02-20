package consts

import (
	"errors"
	"fmt"
)

var (
	ErrorNoAvailableEndpoint    = errors.New("no available endpoint") // there is no endpoints for the backend service
	ErrorBackendServiceNoFound  = errors.New("backend service not found")
	ErrorSchedulerNotReady      = errors.New("scheduler not ready")
	ErrorAllEndpointBusy        = errors.New("all endpoints busy")
	ErrorNoMatchInferMode       = errors.New("not match infer mode")
	ErrorGatewayNotFound        = errors.New("gateway not found")
	ErrorRequestExits           = errors.New("request state exists")
	ErrorRequestNotExits        = errors.New("request state not exists")
	ErrorReadTimeout            = errors.New("read timeout")
	ErrorUnitLimitExceeded      = errors.New("unit limit exceeded") // the unit resource is not enough, and the limit is reached
	ErrorRequestTooLong         = errors.New("request too long")    // the request is too long, and the limit is reached
	ErrorRequestAbortedByEngine = errors.New("request aborted by LLM engine")
	ErrorReadSSETimeout         = errors.New("read sse timeout")
	ErrorCmsNotAvailable        = errors.New("cms not available")
	ErrorBackendBadRequest      = errors.New("bad inference request") // inference return not status ok

	// inference server response error
	ErrorInternalServerError = errors.New("server internal error") // http status code 5xx, may need to retry
	ErrorBadRequest          = errors.New("bad request")           // http status code 4xx, not retry

	// rate limit error
	ErrorRateLimitExceeded     = errors.New("rate limit exceeded")
	ErrorRateLimitQueueTimeOut = errors.New("rate limit queue: request wait timeout")

	// protocol error
	ErrorInvalidModel                            = errors.New("this model is not supported now")
	ErrorContentFieldsMisused                    = errors.New("can't use both Content and MultiContent properties simultaneously")
	ErrorCompletionRequestPromptTypeNotSupported = errors.New("the type of CompletionRequest.Prompt only supports string and []string")
	ErrorUnSupportedProtocol                     = errors.New("not support this protocol")
)

// RetryableError is an interface for errors that can be retried.
// Errors implementing this interface provide retry eligibility and failed instance information.
type RetryableError interface {
	error
	IsRetryable() bool
	FailedInstanceID() string
}

// UpstreamError represents an error response from an upstream service.
// It contains the HTTP status code, response body, request URL, and instance ID.
type UpstreamError struct {
	StatusCode int    // HTTP status code from the upstream response
	Body       []byte // Response body from the upstream
	URL        string // Request URL that caused the error
	InstanceID string // Instance ID that failed
}

// Error implements the error interface.
func (e *UpstreamError) Error() string {
	return fmt.Sprintf("upstream error for %s: status code %d, body: %s",
		e.URL, e.StatusCode, string(e.Body))
}

// IsRetryable returns true if the error is potentially recoverable with a retry.
// Server errors (5xx) are generally retryable, while client errors (4xx) are not.
func (e *UpstreamError) IsRetryable() bool {
	return e.StatusCode >= 500
}

// IsClientError returns true if the error is a client error (4xx).
func (e *UpstreamError) IsClientError() bool {
	return e.StatusCode >= 400 && e.StatusCode < 500
}

// IsServerError returns true if the error is a server error (5xx).
func (e *UpstreamError) IsServerError() bool {
	return e.StatusCode >= 500
}

// FailedInstanceID returns the instance ID that caused this error.
func (e *UpstreamError) FailedInstanceID() string {
	return e.InstanceID
}

// NewUpstreamError creates a new UpstreamError with the given parameters.
func NewUpstreamError(url string, statusCode int, body []byte, instanceID string) *UpstreamError {
	return &UpstreamError{
		StatusCode: statusCode,
		Body:       body,
		URL:        url,
		InstanceID: instanceID,
	}
}

// NetworkError represents a network-level error (connection failure, timeout, etc.).
// Network errors are generally transient and retryable.
type NetworkError struct {
	URL        string // Request URL that caused the error
	Err        error  // Underlying network error
	InstanceID string // Instance ID that failed
}

// Error implements the error interface.
func (e *NetworkError) Error() string {
	return fmt.Sprintf("network error for request to %s: %v", e.URL, e.Err)
}

// Unwrap returns the underlying error for errors.Is and errors.As compatibility.
func (e *NetworkError) Unwrap() error {
	return e.Err
}

// IsRetryable returns true for network errors.
// Network errors are generally transient and can be retried.
func (e *NetworkError) IsRetryable() bool {
	return true
}

// FailedInstanceID returns the instance ID that caused this error.
func (e *NetworkError) FailedInstanceID() string {
	return e.InstanceID
}

// NewNetworkError creates a new NetworkError with the given parameters.
func NewNetworkError(url string, err error, instanceID string) *NetworkError {
	return &NetworkError{
		URL:        url,
		Err:        err,
		InstanceID: instanceID,
	}
}
