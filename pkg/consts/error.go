package consts

import (
	"errors"
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

	// protocol error
	ErrorInvalidModel                            = errors.New("this model is not supported now")
	ErrorContentFieldsMisused                    = errors.New("can't use both Content and MultiContent properties simultaneously")
	ErrorCompletionRequestPromptTypeNotSupported = errors.New("the type of CompletionRequest.Prompt only supports string and []string")
	ErrorUnSupportedProtocol                     = errors.New("not support this protocol")
)
