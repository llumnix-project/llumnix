package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"llm-gateway/cmd/llm-gateway/app/options"
	"llm-gateway/pkg/consts"
	"llm-gateway/pkg/processor"
	"llm-gateway/pkg/protocol"
	"llm-gateway/pkg/types"
	"net/http"
	"time"

	"k8s.io/klog/v2"
)

// init registers the OpenAI handler factory function with the handler registry.
func init() {
	RegisterHandler(consts.OpenAIHandlerName, func(config *options.Config) (RequestHandler, error) {
		return NewOpenAIHandler(config)
	})
}

// OpenAIHandler implements the RequestHandler interface for OpenAI-compatible API endpoints.
// It handles both chat completion and text completion requests, supporting streaming and non-streaming modes.
type OpenAIHandler struct {
	// config holds the gateway configuration
	config *options.Config

	// client is the HTTP client for making backend requests
	client *http.Client

	// preProcessors chain for request preprocessing
	preProcessors *processor.PreProcessorChain
	// postProcessors chain for response postprocessing
	postProcessors *processor.PostProcessorChain

	// backend handles the actual inference execution
	backend InferenceBackend

	// streamProcessor encapsulates the generic streaming mechanism
	streamProcessor *StreamProcessor
}

// NewOpenAIHandler creates a new OpenAIHandler with configured processor chains.
// It initializes pre-processors for request transformation and post-processors for response handling.
// Returns the handler instance or an error if initialization fails.
func NewOpenAIHandler(config *options.Config) (RequestHandler, error) {
	// Setup pre/post-processor chain for request transformation
	preProcessors := processor.CreatePreProcessorChain()
	postProcessor := processor.CreatePostProcessorChain()

	if config.TokenizerEnabled() {
		convertor := processor.NewRequestCompletionConverter()
		if convertor == nil {
			return nil, fmt.Errorf("failed to create request completion converter")
		}
		preProcessors.Register(convertor)

		// Setup post-processor chain for response handling
		chunkProcessor := processor.NewResponseChunkProcessor(config)
		if chunkProcessor == nil {
			return nil, fmt.Errorf("failed to create response chunk processor")
		}
		postProcessor.Register(chunkProcessor)
	}

	// Create the inference backend based on the configuration
	backend, err := BuildBackend(config)
	if err != nil {
		klog.Errorf("build inference backend failed: %v", err)
		return nil, err
	}

	handler := &OpenAIHandler{
		config:         config,
		preProcessors:  preProcessors,
		postProcessors: postProcessor,
		backend:        backend,
	}
	// Inject protocol-specific strategies into the generic streaming processor
	handler.streamProcessor = NewStreamProcessor(handler, handler)
	return handler, nil
}

// Name returns the name of the handler.
func (h *OpenAIHandler) Name() string {
	return consts.OpenAIHandlerName
}

// unmarshalRequest reads and parses the HTTP request body into the appropriate LLM request structure.
// It validates the request schema and determines the protocol type (chat completion or text completion).
// Returns an error if the request body cannot be read or parsed.
func (h *OpenAIHandler) unmarshalRequest(reqCtx *types.RequestContext) error {
	// Read the raw request body
	httpReq := reqCtx.HttpRequest.Request
	data, err := io.ReadAll(httpReq.Body)
	if err != nil {
		klog.Warningf("read request failed: %v, data: %s", err, string(data))
		return err
	}
	// Store raw request data for logging and debugging
	reqCtx.LLMRequest.RawData = string(data)

	// Determine request type based on URL path and parse accordingly
	url := httpReq.URL.Path
	switch {
	case protocol.IsChatCompletionsURL(url):
		// Parse chat completion request (e.g., /v1/chat/completions)
		var chatCompletion protocol.ChatCompletionRequest
		err = json.Unmarshal(data, &chatCompletion)
		if err != nil {
			klog.Warningf("not support ChatCompletionRequest failed: %v, data: %s", err, string(data))
			return fmt.Errorf("Invalid ChatCompletionRequest format")
		}
		reqCtx.LLMRequest.Protocol = protocol.OpenAIChatCompletion
		reqCtx.LLMRequest.OriginChatCompletionRequest = chatCompletion.Clone()
		chatCompletion.StreamOptions = &protocol.StreamOptions{
			IncludeUsage:         true,
			ContinuousUsageStats: true,
		}
		reqCtx.LLMRequest.ChatCompletionRequest = &chatCompletion

		// Now, the backend protocol here is consistent with the input protocol.
		// If a converter is implemented later, it will be modified there.
		reqCtx.LLMRequest.BackendProtocol = protocol.OpenAIChatCompletion
	case protocol.IsCompletionsURL(url):
		// Parse text completion request (e.g., /v1/completions)
		var completionRequest protocol.CompletionRequest
		err = json.Unmarshal(data, &completionRequest)
		if err != nil {
			klog.Warningf("not support CompletionRequest failed: %v: data: %s", err, string(data))
			return fmt.Errorf("Invalid CompletionRequest format")
		}
		reqCtx.LLMRequest.Protocol = protocol.OpenAICompletion
		reqCtx.LLMRequest.OriginCompletionRequest = completionRequest.Clone()
		completionRequest.StreamOptions = &protocol.StreamOptions{
			IncludeUsage:         true,
			ContinuousUsageStats: true,
		}
		reqCtx.LLMRequest.CompletionRequest = &completionRequest

		reqCtx.LLMRequest.BackendProtocol = protocol.OpenAICompletion
	default:
		// Unsupported URL path
		klog.Warningf("not support URL: %s", url)
		return fmt.Errorf("Invalid ChatCompletionRequest format")
	}
	return nil
}

// ParseRequest performs LLM request prompt schema validation and unmarshals the request body.
// It first unmarshals the request, then runs it through the pre-processor chain for transformation.
// The preprocessing duration is recorded in request statistics.
// Returns an error if unmarshaling or preprocessing fails.
func (h *OpenAIHandler) ParseRequest(reqCtx *types.RequestContext) error {
	// In the entry point of the OpenAI handler, define the structure of LLMRequest used by this handler
	reqCtx.LLMRequest = &types.LLMRequest{}

	// Unmarshal and validate the request
	err := h.unmarshalRequest(reqCtx)
	if err != nil {
		return err
	}

	// Execute pre-processing chain and measure duration
	tStart := time.Now()
	err = h.preProcessors.Process(reqCtx)
	if err != nil {
		klog.Errorf("pre-processor failed: %v", err)
		return err
	}
	reqCtx.RequestStats.PreprocessCost = time.Since(tStart)

	return nil
}

func isResponseContentEmpty(response *protocol.ChatCompletionResponse) bool {
	if response == nil || response.Choices == nil || len(response.Choices) == 0 {
		return true
	}
	if response.Choices[0].Message.Content == "" && response.Choices[0].Message.ReasoningContent == "" {
		return true
	}
	return false
}

func (h *OpenAIHandler) unMarshalResponse(reqCtx *types.RequestContext, data []byte) error {
	stream := reqCtx.InferenceStream()
	klog.V(3).Infof("[%s] unMarshalResponse: streaming=%v, protocol=%s", reqCtx.Id, stream, reqCtx.LLMRequest.BackendProtocol)
	switch reqCtx.LLMRequest.BackendProtocol {
	case protocol.OpenAIChatCompletion:
		if stream {
			var response protocol.ChatCompletionStreamResponse
			err := json.Unmarshal(data, &response)
			if err != nil {
				klog.Warningf("failed to unmarshal response: %v, data: %s", err, string(data))
				return fmt.Errorf("failed to unmarshal response")
			}
			klog.V(3).Infof("[%s] unMarshalResponse: got chat completion stream response: %v", reqCtx.Id, response)
			reqCtx.LLMRequest.ChatCompletionStreamResponse = &response
		} else {
			var response protocol.ChatCompletionResponse
			err := json.Unmarshal(data, &response)
			if err != nil {
				klog.Warningf("failed to unmarshal response: %v, data: %s", err, string(data))
				return fmt.Errorf("failed to unmarshal response")
			}
			klog.V(3).Infof("[%s] unMarshalResponse: got chat completion response: %v", reqCtx.Id, response, response)

			if isResponseContentEmpty(&response) {
				klog.Warningf("[%s] unMarshalResponse: response content is empty", reqCtx.Id)
				return fmt.Errorf("response content is empty: %s", string(data))
			}
			reqCtx.LLMRequest.ChatCompletionResponse = &response
		}
	case protocol.OpenAICompletion:
		var response protocol.CompletionResponse
		err := json.Unmarshal(data, &response)
		if err != nil {
			klog.Warningf("failed to unmarshal response: %v, data: %s", err, string(data))
			return fmt.Errorf("failed to unmarshal response")
		}
		klog.V(3).Infof("[%s] unMarshalResponse: got completion response: %v", reqCtx.Id, response)
		reqCtx.LLMRequest.CompletionResponse = &response
	default:
		return fmt.Errorf("Unsupported protocol: %s", reqCtx.LLMRequest.BackendProtocol)
	}
	return nil
}

// ParseChunk implements ChunkParser interface for OpenAI protocol.
// It parses a raw response chunk from the backend into a CompletionResponse structure.
// Handles the special "[DONE]" marker which indicates the end of a streaming response.
// Returns io.EOF when encountering the done marker, or an error if parsing fails.
func (h *OpenAIHandler) ParseChunk(reqCtx *types.RequestContext, data []byte) error {
	// Check for stream end marker
	if bytes.Equal(data, []byte("[DONE]")) {
		return io.EOF
	}
	return h.unMarshalResponse(reqCtx, data)
}

// marshalResponse converts the response structure back to JSON bytes based on the protocol type.
// It handles both streaming and non-streaming responses for chat and text completions.
// The response structure is cleared after marshaling to prevent memory leaks.
// Returns the marshaled JSON bytes or an error if marshaling fails.
func (h *OpenAIHandler) marshalResponse(reqCtx *types.RequestContext) ([]byte, error) {
	stream := reqCtx.ClientStream()
	switch reqCtx.LLMRequest.Protocol {
	case protocol.OpenAIChatCompletion:
		klog.V(3).Infof("[%s] marshalResponse: streaming=%v chat completion response", reqCtx.Id, stream)
		if stream {
			// Handle streaming chat completion response
			if reqCtx.LLMRequest.ChatCompletionStreamResponse == nil {
				return nil, nil
			}
			return json.Marshal(reqCtx.LLMRequest.ChatCompletionStreamResponse)
		} else {
			// Handle non-streaming chat completion response
			if reqCtx.LLMRequest.ChatCompletionResponse == nil {
				return nil, nil
			}
			return json.Marshal(reqCtx.LLMRequest.ChatCompletionResponse)
		}
	case protocol.OpenAICompletion:
		// Handle text completion response (both streaming and non-streaming)
		klog.V(3).Infof("[%s] marshalResponse: streaming=%v completion response", reqCtx.Id, stream)
		if reqCtx.LLMRequest.CompletionResponse == nil {
			klog.Warningf("[%s] marshalResponse: no completion response to marshal", reqCtx.Id)
			return nil, nil
		}
		return json.Marshal(reqCtx.LLMRequest.CompletionResponse)
	default:
		return nil, fmt.Errorf("Unsupported protocol: %s", reqCtx.LLMRequest.Protocol)
	}
}

// Handle processes the LLM inference request and streams the response back to the client.
// It delegates to the generic StreamProcessor which encapsulates the streaming mechanism,
// while this handler provides OpenAI-specific parsing and writing strategies.
// Timing metrics like TTFT and ITL are tracked by the StreamProcessor.
func (h *OpenAIHandler) handleStream(req *types.RequestContext) error {
	// Initiate streaming inference from the backend
	chunkChan, err := h.backend.StreamInference(req)
	if err != nil {
		klog.Errorf("failed to stream inference: %v", err)
		return err
	}

	// Delegate to the generic streaming processor with OpenAI-specific strategies
	h.streamProcessor.ProcessStream(req, chunkChan)
	return nil
}

func (h *OpenAIHandler) handleMessage(req *types.RequestContext) error {
	data, err := h.backend.Inference(req)
	if err != nil {
		return err
	}

	err = h.unMarshalResponse(req, data)
	if err != nil {
		return err
	}

	err = h.postProcessors.Process(req)
	if err != nil {
		return err
	}

	// Marshal the response structure to JSON
	data, err = h.marshalResponse(req)
	if err != nil {
		return err
	}

	// Write response chunk if there's data to send
	if len(data) > 0 {
		klog.V(3).Infof("writing response chunk: %s", string(data))
		req.WriteResponse(data)
	}
	return nil
}

func (h *OpenAIHandler) Handle(req *types.RequestContext) error {
	if req.InferenceStream() {
		return h.handleStream(req)
	} else {
		return h.handleMessage(req)
	}
}

// ProcessAndWriteChunk implements ChunkWriter interface for OpenAI protocol.
// It processes a response chunk through post-processors and writes it to the client.
// Handles both intermediate chunks and the final chunk (marked by done=true).
// The processing duration is accumulated in request statistics.
// Returns an error if post-processing, marshaling, or writing fails.
func (h *OpenAIHandler) ProcessAndWriteChunk(req *types.RequestContext, done bool) error {
	// Execute post-processing chain and measure duration
	err := h.postProcessors.ProcessStream(req, done)
	if err != nil {
		return fmt.Errorf("post-processor failed: %w", err)
	}

	// Marshal the response structure to JSON
	data, err := h.marshalResponse(req)
	if err != nil {
		return fmt.Errorf("marshal response failed: %w", err)
	}

	// Write response chunk if there's data to send
	if len(data) > 0 {
		klog.V(3).Infof("writing response chunk: %s", string(data))
		req.WriteResponse(data)
	}

	// Send the stream completion marker if this is the final chunk
	if done {
		klog.V(3).Infof("writing done chunk")
		req.WriteResponse([]byte("[DONE]"))
	}

	return nil
}
