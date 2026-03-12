package consts

import (
	"context"
	"errors"
	"fmt"
)

var (
	ErrorNoAvailableEndpoint    = errors.New("no available endpoint") // the backend instances exist, but it cannot be obtained as the load is too high
	ErrorEndpointNotFound       = errors.New("endpoint not found")    // there is no endpoints for the backend service
	ErrorBackendServiceNoFound  = errors.New("backend service not found")
	ErrorSchedulerNotReady      = errors.New("scheduler not ready")
	ErrorMayNetworkBroken       = errors.New("may network broken")
	ErrorAllEndpointBusy        = errors.New("all endpoints busy")
	ErrorNoMatchInferType       = errors.New("not match infer type")
	ErrorGatewayNotFound        = errors.New("gateway not found")
	ErrorRequestExits           = errors.New("request state exists")
	ErrorRequestNotExits        = errors.New("request state not exists")
	ErrorRequestGatewayChanged  = errors.New("request gateway changed")
	ErrorReadTimeout            = errors.New("read timeout")
	ErrorUnitLimitExceeded      = errors.New("unit limit exceeded") // the unit resource is not enough, and the limit is reached
	ErrorRequestTooLong         = errors.New("request too long")    // the request is too long, and the limit is reached
	ErrorRequestAbortedByEngine = errors.New("request aborted by LLM engine")
	ErrorReadSSETimeout         = errors.New("read sse timeout")
	ErrorCmsNotAvailable        = errors.New("cms not available")
	ErrorBackendBadRequest      = errors.New("bad inference request") // inference return not status ok

	ErrorInternalServerError = errors.New("internal server error")
	ErrorBadRequest          = errors.New("bad request")
	ErrorRateLimitExceeded   = errors.New("rate limit exceeded")

	// protocol error
	ErrorInvalidModel                            = errors.New("this model is not supported now")
	ErrorContentFieldsMisused                    = errors.New("can't use both Content and MultiContent properties simultaneously")
	ErrorCompletionRequestPromptTypeNotSupported = errors.New("the type of CompletionRequest.Prompt only supports string and []string")
	ErrorUnSupportedProtocol                     = errors.New("not support this protocol")
)

// RetryableError is an interface for errors that can be retried.
type RetryableError interface {
	error
	IsRetryable() bool
	FailedInstanceID() string
}

// UpstreamError represents an error response from an upstream service.
type UpstreamError struct {
	StatusCode int
	Body       []byte
	URL        string
	InstanceID string
}

func (e *UpstreamError) Error() string {
	return fmt.Sprintf("upstream error for %s: status code %d, body: %s",
		e.URL, e.StatusCode, string(e.Body))
}

func (e *UpstreamError) IsRetryable() bool {
	return e.StatusCode >= 500
}

func (e *UpstreamError) IsClientError() bool {
	return e.StatusCode >= 400 && e.StatusCode < 500
}

func (e *UpstreamError) FailedInstanceID() string {
	return e.InstanceID
}

func NewUpstreamError(url string, statusCode int, body []byte, instanceID string) *UpstreamError {
	return &UpstreamError{
		StatusCode: statusCode,
		Body:       body,
		URL:        url,
		InstanceID: instanceID,
	}
}

// NetworkError represents a network-level error (connection failure, timeout, etc.).
type NetworkError struct {
	URL        string
	Err        error
	InstanceID string
}

func (e *NetworkError) Error() string {
	return fmt.Sprintf("network error for request to %s: %v", e.URL, e.Err)
}

func (e *NetworkError) Unwrap() error {
	return e.Err
}

func (e *NetworkError) IsRetryable() bool {
	return e.Err != nil && !errors.Is(e.Err, context.Canceled) && !errors.Is(e.Err, context.DeadlineExceeded)
}

func (e *NetworkError) FailedInstanceID() string {
	return e.InstanceID
}
