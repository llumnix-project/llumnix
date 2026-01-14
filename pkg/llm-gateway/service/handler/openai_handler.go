package handler

import (
	"bytes"
	"easgo/cmd/llm-gateway/app/options"
	"easgo/pkg/llm-gateway/processor"
	"easgo/pkg/llm-gateway/protocol"
	"easgo/pkg/llm-gateway/types"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"k8s.io/klog/v2"
)

type OpenAIHandler struct {
	config *options.Config

	client *http.Client

	preProcessors  *processor.PreProcessorChain
	postProcessors *processor.PostProcessorChain

	backend InferenceBackend
}

func NewOpenAIHandler(config *options.Config) *OpenAIHandler {
	preProcessors := processor.CreatePreProcessorChain()
	preProcessors.Register(processor.NewRequestCompletionConverter())
	postProcessor := processor.CreatePostProcessorChain()
	postProcessor.Register(processor.NewResponseChunkProcessor(config))

	handler := &OpenAIHandler{
		config:         config,
		preProcessors:  preProcessors,
		postProcessors: postProcessor,
		backend:        NewSimpleBackend(),
	}
	return handler
}

// ParseOpenAIRequest performs LLM request prompt schema validation and unmarshal the request body.
func (h *OpenAIHandler) ParseRequest(reqCtx *types.RequestContext) error {
	// LLM request prompt schema check and unmarshal the request body
	httpReq := reqCtx.HttpRequest.Request
	data, err := io.ReadAll(httpReq.Body)
	if err != nil {
		klog.Warningf("read request failed: %v, data: %s", err, string(data))
		return err
	}
	reqCtx.LLMRequest.RawData = string(data)

	url := httpReq.URL.Path
	switch {
	case protocol.IsChatCompletionsURL(url):
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
		var completionRequest protocol.CompletionRequest
		err = json.Unmarshal(data, &completionRequest)
		if err != nil {
			klog.Warningf("not support CompletionRequest failed: %v: data: %s", err, string(data))
			return fmt.Errorf("Invalid ChatCompletionRequest format")
		}
		reqCtx.LLMRequest.Model = completionRequest.Model
		reqCtx.LLMRequest.Protocol = protocol.OpenAICompletion
		reqCtx.LLMRequest.CompletionRequest = &completionRequest
	default:
		klog.Warningf("not support URL: %s", url)
		return fmt.Errorf("Invalid ChatCompletionRequest format")
	}
	return nil
}

func (h *OpenAIHandler) parseResponse(reqCtx *types.RequestContext, data []byte) error {
	if bytes.Equal(data, []byte("[DONE]")) {
		return io.EOF
	}

	var response protocol.CompletionResponse
	err := json.Unmarshal(data, &response)
	if err != nil {
		klog.Warningf("failed to unmarshal response: %v, data: %s", err, string(data))
		return fmt.Errorf("failed to unmarshal response")
	}
	reqCtx.LLMRequest.CompletionResponse = &response
	return nil
}

func (h *OpenAIHandler) marshalResponse(reqCtx *types.RequestContext) ([]byte, error) {
	switch reqCtx.LLMRequest.Protocol {
	case protocol.OpenAIChatCompletion:
		stream := reqCtx.LLMRequest.ClientStream
		klog.V(2).Infof("[%s] marshalResponse: streaming=%v chat completion response", reqCtx.Id, stream)
		if stream {
			if reqCtx.LLMRequest.ChatCompletionStreamResponse == nil {
				return nil, nil
			}
			data, err := json.Marshal(reqCtx.LLMRequest.ChatCompletionStreamResponse)
			// Clean up the results of this streaming process to avoid some unforeseen errors
			reqCtx.LLMRequest.ChatCompletionStreamResponse = nil
			return data, err
		} else {
			// No output
			if reqCtx.LLMRequest.ChatCompletionResponse == nil {
				return nil, nil
			}
			data, err := json.Marshal(reqCtx.LLMRequest.ChatCompletionResponse)
			// Clean up the results of this streaming process to avoid some unforeseen errors
			reqCtx.LLMRequest.ChatCompletionResponse = nil
			return data, err
		}
	case protocol.OpenAICompletion:
		stream := reqCtx.LLMRequest.ClientStream
		klog.V(2).Infof("[%s] marshalResponse: streaming=%v completion response", reqCtx.Id, stream)
		if reqCtx.LLMRequest.CompletionResponse == nil {
			klog.Warningf("[%s] marshalResponse: no completion response to marshal", reqCtx.Id)
			return nil, nil
		}
		data, err := json.Marshal(reqCtx.LLMRequest.CompletionResponse)
		reqCtx.LLMRequest.CompletionResponse = nil
		return data, err
	default:
		return nil, fmt.Errorf("Unsupported protocol: %s", reqCtx.LLMRequest.Protocol)
	}
}

func (h *OpenAIHandler) Handle(req *types.RequestContext) {
	defer close(req.ResponseChan)

	// pre-process the request
	err := h.preProcessors.Process(req)
	if err != nil {
		WriteErrorResponse(req, err)
		return
	}

	// stream inference
	chunkChan, err := h.backend.StreamInference(req)
	if err != nil {
		klog.Errorf("failed to stream inference: %v", err)
		WriteErrorResponse(req, err)
		return
	}
	for chunk := range chunkChan {
		klog.V(2).Infof("received stream chunk: %s, err: %v", string(chunk.Data), chunk.err)

		// handle stream errors
		if chunk.err != nil {
			if chunk.err == io.EOF && len(chunk.Data) == 0 {
				// normal stream end with no data
				break
			}
			klog.Errorf("error during stream inference: %v", chunk.err)
			WriteErrorResponse(req, chunk.err)
			return
		}

		// parse the response to internal response struct
		err := h.parseResponse(req, chunk.Data)
		if err != nil && err != io.EOF {
			klog.Errorf("failed to parse response: %v", err)
			WriteErrorResponse(req, err)
			return
		}

		// check if stream has ended
		isStreamEnd := (err == io.EOF)

		// process and write response chunk
		if err := h.processAndWriteChunk(req, isStreamEnd); err != nil {
			klog.Errorf("failed to process and write chunk: %v", err)
			WriteErrorResponse(req, err)
			return
		}

		// check if max tokens limit reached after writing
		if req.RequestStats.OutputExceedMaxTokens() {
			// send final chunk with done=true
			if err := h.processAndWriteChunk(req, true); err != nil {
				klog.Errorf("failed to write final chunk: %v", err)
			}
			return
		}
	}
}

// processAndWriteChunk processes the request through post-processors and writes the response
func (h *OpenAIHandler) processAndWriteChunk(req *types.RequestContext, done bool) error {
	// post-process the request
	err := h.postProcessors.Process(req, done)
	if err != nil {
		return fmt.Errorf("post-processor failed: %w", err)
	}

	// marshal response data
	data, err := h.marshalResponse(req)
	if err != nil {
		return fmt.Errorf("marshal response failed: %w", err)
	}

	// write response if there's data
	if len(data) > 0 {
		klog.V(2).Infof("writing response chunk: %s", string(data))
		WriteResponse(req, data)
	}

	// write done chunk if stream is done
	if done {
		klog.V(2).Infof("writing done chunk")
		WriteResponse(req, []byte("[DONE]"))
	}

	return nil
}
