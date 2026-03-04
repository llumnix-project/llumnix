package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"k8s.io/klog/v2"

	"llumnix/cmd/gateway/app/options"
	"llumnix/pkg/consts"
	"llumnix/pkg/gateway/processor"
	"llumnix/pkg/gateway/protocol"
	"llumnix/pkg/types"
)

// init registers the OpenAI handler factory function with the handler registry.
func init() {
	registerHandler("openai", func(config *options.GatewayConfig) (RequestHandler, error) {
		return newOpenAIHandler(config)
	})
}

// OpenAIHandler implements the RequestHandler interface for OpenAI-compatible API endpoints.
// It handles both chat completion and text completion requests, supporting streaming and non-streaming modes.
type OpenAIHandler struct {
	// config holds the gateway configuration
	config *options.GatewayConfig

	// client is the HTTP client for making backend requests
	client *http.Client

	// preProcessors chain for request preprocessing
	preProcessors *processor.PreProcessorChain
	// postProcessors chain for response postprocessing
	postProcessors *processor.PostProcessorChain

	// forwarder handles request forwarding to inference engines
	forwarder Forwarder
}

// newOpenAIHandler creates a new OpenAIHandler with configured processor chains.
// It initializes pre-processors for request transformation and post-processors for response handling.
// Returns the handler instance or an error if initialization fails.
func newOpenAIHandler(config *options.GatewayConfig) (RequestHandler, error) {
	// Setup pre-processor chain for request transformation
	preProcessors := processor.CreatePreProcessorChain()
	convertor := processor.NewRequestCompletionConverter()
	if convertor == nil {
		return nil, fmt.Errorf("failed to create request completion converter")
	}
	preProcessors.Register(convertor)

	// Setup post-processor chain for response handling
	postProcessor := processor.CreatePostProcessorChain()
	chunkProcessor := processor.NewResponseChunkProcessor(&config.ProcessorConfig)
	if chunkProcessor == nil {
		return nil, fmt.Errorf("failed to create response chunk processor")
	}
	postProcessor.Register(chunkProcessor)

	name := config.PDDisaggProtocol
	if name == "" {
		name = consts.ForwarderTypeNeutral
	}
	var schedulingMode types.SchedulingMode
	if config.SeparatePDScheduling {
		schedulingMode = types.SchedulingModePDStaged
	} else {
		schedulingMode = types.SchedulingModePDBatch
	}
	forwarder, err := buildForwarder(name, schedulingMode)
	if err != nil {
		klog.Errorf("build forwarder failed: %v", err)
		return nil, err
	}

	handler := &OpenAIHandler{
		config:         config,
		preProcessors:  preProcessors,
		postProcessors: postProcessor,
		forwarder:      forwarder,
	}
	return handler, nil
}

// UnmarshalRequest reads and parses the HTTP request body into the appropriate LLM request structure.
// It validates the request schema and determines the protocol type (chat completion or text completion).
// Returns an error if the request body cannot be read or parsed.
func (h *OpenAIHandler) UnmarshalRequest(reqCtx *types.RequestContext) error {
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
		reqCtx.LLMRequest.Model = chatCompletion.Model
		reqCtx.LLMRequest.Protocol = protocol.OpenAIChatCompletion
		reqCtx.LLMRequest.ChatCompletionRequest = &chatCompletion
	case protocol.IsCompletionsURL(url):
		// Parse text completion request (e.g., /v1/completions)
		var completionRequest protocol.CompletionRequest
		err = json.Unmarshal(data, &completionRequest)
		if err != nil {
			klog.Warningf("not support CompletionRequest failed: %v: data: %s", err, string(data))
			return fmt.Errorf("Invalid CompletionRequest format")
		}
		reqCtx.LLMRequest.Model = completionRequest.Model
		reqCtx.LLMRequest.Protocol = protocol.OpenAICompletion
		reqCtx.LLMRequest.CompletionRequest = &completionRequest
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
// Returns an error if unmarshalling or preprocessing fails.
func (h *OpenAIHandler) ParseRequest(reqCtx *types.RequestContext) error {
	// Unmarshal and validate the request
	err := h.UnmarshalRequest(reqCtx)
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

// parseResponse parses a raw response chunk from the backend into a CompletionResponse structure.
// It handles the special "[DONE]" marker which indicates the end of a streaming response.
// Returns io.EOF when encountering the done marker, or an error if parsing fails.
func (h *OpenAIHandler) parseResponse(reqCtx *types.RequestContext, data []byte) error {
	// Check for stream end marker
	if bytes.Equal(data, []byte("[DONE]")) {
		return io.EOF
	}

	// Parse the response data into CompletionResponse structure
	var response protocol.CompletionResponse
	err := json.Unmarshal(data, &response)
	if err != nil {
		klog.Warningf("failed to unmarshal response: %v, data: %s", err, string(data))
		return fmt.Errorf("failed to unmarshal response")
	}
	reqCtx.LLMRequest.CompletionResponse = &response
	return nil
}

// marshalResponse converts the response structure back to JSON bytes based on the protocol type.
// It handles both streaming and non-streaming responses for chat and text completions.
// The response structure is cleared after marshaling to prevent memory leaks.
// Returns the marshaled JSON bytes or an error if marshaling fails.
func (h *OpenAIHandler) marshalResponse(reqCtx *types.RequestContext) ([]byte, error) {
	switch reqCtx.LLMRequest.Protocol {
	case protocol.OpenAIChatCompletion:
		stream := reqCtx.LLMRequest.ClientStream
		klog.V(3).Infof("[%s] marshalResponse: streaming=%v chat completion response", reqCtx.Id, stream)
		if stream {
			// Handle streaming chat completion response
			if reqCtx.LLMRequest.ChatCompletionStreamResponse == nil {
				return nil, nil
			}
			data, err := json.Marshal(reqCtx.LLMRequest.ChatCompletionStreamResponse)
			// Clear the response to free memory and prevent reuse
			reqCtx.LLMRequest.ChatCompletionStreamResponse = nil
			return data, err
		} else {
			// Handle non-streaming chat completion response
			if reqCtx.LLMRequest.ChatCompletionResponse == nil {
				return nil, nil
			}
			data, err := json.Marshal(reqCtx.LLMRequest.ChatCompletionResponse)
			// Clear the response to free memory and prevent reuse
			reqCtx.LLMRequest.ChatCompletionResponse = nil
			return data, err
		}
	case protocol.OpenAICompletion:
		// Handle text completion response (both streaming and non-streaming)
		stream := reqCtx.LLMRequest.ClientStream
		klog.V(3).Infof("[%s] marshalResponse: streaming=%v completion response", reqCtx.Id, stream)
		if reqCtx.LLMRequest.CompletionResponse == nil {
			klog.Warningf("[%s] marshalResponse: no completion response to marshal", reqCtx.Id)
			return nil, nil
		}
		data, err := json.Marshal(reqCtx.LLMRequest.CompletionResponse)
		// Clear the response to free memory
		reqCtx.LLMRequest.CompletionResponse = nil
		return data, err
	default:
		return nil, fmt.Errorf("unsupported protocol: %s", reqCtx.LLMRequest.Protocol)
	}
}

// Handle processes the LLM inference request and streams the response back to the client.
// It coordinates the entire request lifecycle: inference execution, response parsing,
// post-processing, and response writing. Timing metrics like TTFT and ITL are tracked.
// The response channel is automatically closed when processing completes.
func (h *OpenAIHandler) Handle(req *types.RequestContext) {
	defer func() {
		close(req.ResponseChan)
		req.OnPostRequest()
	}()

	// Initiate streaming inference from the backend
	chunkChan, err := h.forwarder.Forward(req)
	if err != nil {
		klog.Errorf("failed to stream inference: %v", err)
		writeErrorResponse(req, err)
		return
	}

	// Initialize timing metrics for TTFT (Time To First Token) and ITL (Inter-Token Latency)
	isFirst := true
	isFirstDecode := true
	var lastTime time.Time

	// Process each chunk from the streaming inference
	for chunk := range chunkChan {
		klog.V(3).Infof("received stream chunk: %s, err: %v", string(chunk.Data), chunk.err)

		// Record timing metrics for performance monitoring
		if isFirst {
			// Record Time To First Token (TTFT)
			lastTime = time.Now()
			req.RequestStats.FirstTime = lastTime

			// Trigger post-prefill hook to release prefill node resource if PD mode
			req.OnPostPrefill()

			isFirst = false
		} else {
			// Record Inter-Token Latency (ITL)
			req.RequestStats.ITLs = append(req.RequestStats.ITLs, time.Since(lastTime).Milliseconds())
			lastTime = time.Now()

			// Trigger post-decode-first-stream-response hook to add request state of decode instance
			if isFirstDecode {
				req.OnPostDecodeFirstStreamResponse()
			}
			isFirstDecode = false
		}

		// Handle stream errors and end-of-stream conditions
		if chunk.err != nil {
			if chunk.err == io.EOF {
				// Normal stream end - process any remaining data before completing
				if len(chunk.Data) > 0 {
					// Parse and process the final chunk that came with EOF
					err := h.parseResponse(req, chunk.Data)
					if err != nil && err != io.EOF {
						klog.Errorf("failed to parse final response: %v", err)
						writeErrorResponse(req, err)
						return
					}
					if err := h.processAndWriteChunk(req, true); err != nil {
						klog.Errorf("failed to process final chunk: %v", err)
						writeErrorResponse(req, err)
						return
					}
				}
				break
			}
			// Handle unexpected errors during streaming
			klog.Errorf("error during stream inference: %v", chunk.err)
			writeErrorResponse(req, chunk.err)
			return
		}

		// Parse the chunk data into internal response structure
		err := h.parseResponse(req, chunk.Data)
		if err != nil && err != io.EOF {
			klog.Errorf("failed to parse response: %v", err)
			writeErrorResponse(req, err)
			return
		}

		// Trigger post-decode-each-stream-response hook to update request state of decode instance
		if len(req.RequestStats.ITLs) > 0 {
			req.OnPostDecodeEachStreamResponse()
		}

		// Determine if this is the final chunk in the stream
		isStreamEnd := err == io.EOF

		// Process through post-processor chain and write to client
		if err := h.processAndWriteChunk(req, isStreamEnd); err != nil {
			klog.Errorf("failed to process and write chunk: %v", err)
			writeErrorResponse(req, err)
			return
		}

		// Check if maximum token limit has been reached
		if req.RequestStats.OutputExceedMaxTokens() {
			// Send final chunk to gracefully end the stream
			if err := h.processAndWriteChunk(req, true); err != nil {
				klog.Errorf("failed to write final chunk: %v", err)
			}
			return
		}
	}
}

// processAndWriteChunk processes a response chunk through post-processors and writes it to the client.
// It handles both intermediate chunks and the final chunk (marked by done=true).
// The processing duration is accumulated in request statistics.
// Returns an error if post-processing, marshaling, or writing fails.
func (h *OpenAIHandler) processAndWriteChunk(req *types.RequestContext, done bool) error {
	// Execute post-processing chain and measure duration
	tStart := time.Now()
	err := h.postProcessors.Process(req, done)
	if err != nil {
		return fmt.Errorf("post-processor failed: %w", err)
	}
	tCost := time.Since(tStart)
	req.RequestStats.PostprocessCost += tCost

	// Marshal the response structure to JSON
	data, err := h.marshalResponse(req)
	if err != nil {
		return fmt.Errorf("marshal response failed: %w", err)
	}

	// Write response chunk if there's data to send
	if len(data) > 0 {
		klog.V(3).Infof("writing response chunk: %s", string(data))
		writeResponse(req, data)
	}

	// Send the stream completion marker if this is the final chunk
	if done {
		klog.V(3).Infof("writing done chunk")
		writeResponse(req, []byte("[DONE]"))
	}

	return nil
}
