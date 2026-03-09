package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"llm-gateway/cmd/llm-gateway/app/options"
	"llm-gateway/pkg/consts"
	"llm-gateway/pkg/gateway/processor"
	"llm-gateway/pkg/gateway/protocol"
	"llm-gateway/pkg/gateway/protocol/anthropic"
	"llm-gateway/pkg/gateway/service/backend"
	"llm-gateway/pkg/types"
	"net/http"

	"k8s.io/klog/v2"
)

// init registers the OpenAI handler factory function with the handler registry.
func init() {
	RegisterHandler(consts.AnthropicHandlerName, func(config *options.Config) (RequestHandler, error) {
		return NewAnthropicHandler(config)
	})
}

// AnthropicHandler implements the RequestHandler interface for OpenAI-compatible API endpoints.
// It handles both chat completion and text completion requests, supporting streaming and non-streaming modes.
type AnthropicHandler struct {
	// config holds the gateway configuration
	config *options.Config

	// client is the HTTP client for making backend requests
	client *http.Client

	// preProcessors chain for request preprocessing
	preProcessors *processor.PreProcessorChain
	// postProcessors chain for response postprocessing
	postProcessors *processor.PostProcessorChain

	// streamProcessor encapsulates the generic streaming mechanism
	streamProcessor *StreamProcessor
}

// NewAnthropicHandler creates a new AnthropicHandler with configured processor chains.
// It initializes pre-processors for request transformation and post-processors for response handling.
// Returns the handler instance or an error if initialization fails.
func NewAnthropicHandler(config *options.Config) (RequestHandler, error) {
	// Setup pre-processor chain for request transformation
	preProcessors := processor.CreatePreProcessorChain()
	converter := processor.BuildPreProcessor("anthropic", nil)
	if converter == nil {
		return nil, fmt.Errorf("failed to create request anthropic converter")
	}
	preProcessors.Register(converter)

	// Setup post-processor chain for response handling
	postProcessor := processor.CreatePostProcessorChain()
	respConverter := processor.BuildPostProcessor("anthropic", nil)
	if respConverter == nil {
		return nil, fmt.Errorf("failed to create response chunk processor")
	}
	postProcessor.Register(respConverter)

	handler := &AnthropicHandler{
		config:         config,
		preProcessors:  preProcessors,
		postProcessors: postProcessor,
	}
	// Inject protocol-specific strategies into the generic streaming processor
	handler.streamProcessor = NewStreamProcessor(handler, handler)
	return handler, nil
}

// Name returns the name of the handler.
func (h *AnthropicHandler) Name() string {
	return consts.AnthropicHandlerName
}

// unmarshalRequest reads and parses the HTTP request body into the appropriate LLM request structure.
// It validates the request schema and determines the protocol type (chat completion or text completion).
// Returns an error if the request body cannot be read or parsed.
func (h *AnthropicHandler) unmarshalRequest(reqCtx *types.RequestContext) error {
	// Read the raw request body
	httpReq := reqCtx.HttpRequest.Request
	data, err := io.ReadAll(httpReq.Body)
	if err != nil {
		klog.Warningf("read request failed: %v, data: %s", err, string(data))
		return err
	}
	// Store raw request data for logging and debugging
	reqCtx.AnthropicRequest.RawData = data

	// Parse the raw request data into Anthropic format
	var anthropicReq anthropic.Request
	if err := json.Unmarshal(data, &anthropicReq); err != nil {
		return fmt.Errorf("error parsing request data: %v", err)
	}
	reqCtx.AnthropicRequest.Request = &anthropicReq
	return nil
}

// ParseRequest performs LLM request prompt schema validation and unmarshals the request body.
// It first unmarshals the request, then runs it through the pre-processor chain for transformation.
// The preprocessing duration is recorded in request statistics.
// Returns an error if unmarshaling or preprocessing fails.
func (h *AnthropicHandler) ParseRequest(reqCtx *types.RequestContext) error {
	// In the entry point of the Anthropic handler, define the structure of AnthropicRequest used by this handler
	reqCtx.AnthropicRequest = &types.AnthropicRequest{
		StreamResponseBuffer: &anthropic.StreamingResponseBuffer{
			ToolCalls: make(map[string]*anthropic.ToolCall),
		},
	}

	// Unmarshal and validate the request
	err := h.unmarshalRequest(reqCtx)
	if err != nil {
		return err
	}

	// Execute pre-processing chain and measure duration
	err = h.preProcessors.Process(reqCtx)
	if err != nil {
		klog.Errorf("pre-processor failed: %v", err)
		return err
	}

	return nil
}

// ParseChunk implements ChunkParser interface for Anthropic protocol.
// It parses a raw response chunk from the backend into a CompletionResponse structure.
// Handles the special "[DONE]" marker which indicates the end of a streaming response.
// Returns io.EOF when encountering the done marker, or an error if parsing fails.
func (h *AnthropicHandler) ParseChunk(reqCtx *types.RequestContext, data []byte) error {
	// Check for stream end marker
	if bytes.Equal(data, []byte("[DONE]")) {
		return io.EOF
	}
	// Parse the response data into CompletionResponse structure
	var response protocol.ChatCompletionStreamResponse
	err := json.Unmarshal(data, &response)
	if err != nil {
		klog.Warningf("failed to unmarshal response: %v, data: %s", err, string(data))
		return fmt.Errorf("failed to unmarshal response")
	}
	reqCtx.AnthropicRequest.OpenAIStreamResponse = &response
	return nil
}

// handleStream processes the LLM inference request and streams the response back to the client.
// It delegates to the generic StreamProcessor which encapsulates the streaming mechanism,
// while this handler provides Anthropic-specific parsing and writing strategies.
// Timing metrics like TTFT and ITL are tracked by the StreamProcessor.
func (h *AnthropicHandler) handleStream(req *types.RequestContext, b backend.InferenceBackend) error {
	// Initiate streaming inference from the backend
	chunkChan, err := b.StreamInference(req)
	if err != nil {
		klog.Errorf("failed to stream inference: %v", err)
		return err
	}

	// Delegate to the generic streaming processor with Anthropic-specific strategies
	h.streamProcessor.ProcessStream(req, chunkChan)
	return nil
}

func (h *AnthropicHandler) handleMessage(req *types.RequestContext, b backend.InferenceBackend) error {
	data, err := b.Inference(req)
	if err != nil {
		return err
	}

	var response protocol.ChatCompletionResponse
	err = json.Unmarshal(data, &response)
	if err != nil {
		return err
	}

	req.AnthropicRequest.OpenAIResponse = &response

	// Execute post-processing chain and measure duration
	err = h.postProcessors.Process(req)
	if err != nil {
		return err
	}

	// Write response chunk if there's data to send
	if len(req.AnthropicRequest.ResponseData) > 0 {
		klog.V(3).Infof("writing response chunk: %s", string(req.AnthropicRequest.ResponseData))
		// The reason for using WriteRawResponse is that the relevant converters from Anthropic already involve specific protocol data.
		req.WriteRawResponse(req.AnthropicRequest.ResponseData)
	}
	return nil
}

// Handle processes the LLM inference request and sends the response back to the client.
// It uses the provided backend for inference.
func (h *AnthropicHandler) Handle(req *types.RequestContext, b backend.InferenceBackend) error {
	if req.InferenceStream() {
		return h.handleStream(req, b)
	} else {
		return h.handleMessage(req, b)
	}
}

// ProcessAndWriteChunk implements ChunkWriter interface for Anthropic protocol.
func (h *AnthropicHandler) ProcessAndWriteChunk(req *types.RequestContext, done bool) error {
	// Execute post-processing chain and measure duration
	err := h.postProcessors.ProcessStream(req, done)
	if err != nil {
		return fmt.Errorf("post-processor failed: %w", err)
	}

	// Write response chunk if there's data to send
	if len(req.AnthropicRequest.ResponseData) > 0 {
		klog.V(3).Infof("writing response chunk: %s", string(req.AnthropicRequest.ResponseData))
		req.WriteRawResponse(req.AnthropicRequest.ResponseData)
	}

	return nil
}
