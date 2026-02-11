package consts

import (
	"errors"
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

	// rate limit error
	ErrorRateLimitExceeded     = errors.New("rate limit exceeded")
	ErrorRateLimitQueueTimeOut = errors.New("rate limit queue: request wait timeout")

	// protocol error
	ErrorInvalidModel                            = errors.New("this model is not supported now")
	ErrorContentFieldsMisused                    = errors.New("can't use both Content and MultiContent properties simultaneously")
	ErrorCompletionRequestPromptTypeNotSupported = errors.New("the type of CompletionRequest.Prompt only supports string and []string")
	ErrorUnSupportedProtocol                     = errors.New("not support this protocol")
)
