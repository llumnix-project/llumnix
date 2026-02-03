package processor

import (
	claude_code "llm-gateway/pkg/processor/claude-code"
	"llm-gateway/pkg/types"

	"k8s.io/klog/v2"
)

// ResponseAnthropicConverter is a post-processor that converts OpenAI streaming responses to Anthropic format.
// This enables the gateway to return Anthropic-compliant responses to clients that initiated Anthropic API requests,
// even when the backend returns OpenAI-formatted streaming data.

// SSE Event Flow:
//   - First chunk: message_start → content_block_start → ping
//   - Middle chunks: content_block_delta (text/tool deltas)
//   - Last chunk: content_block_stop → message_delta → message_stop
type ResponseAnthropicConverter struct{}

// NewResponseAnthropicConverter creates and returns a new ResponseAnthropicConverter instance.
func NewResponseAnthropicConverter() *ResponseAnthropicConverter {
	return &ResponseAnthropicConverter{}
}

// Name returns the name of the processor.
func (c *ResponseAnthropicConverter) Name() string {
	return "ResponseAnthropicConverter"
}

// PostProcess implements the PostProcessor interface.
// It converts OpenAI streaming response chunks to Anthropic SSE format and updates RequestContext.
//
// Parameters:
//   - req: RequestContext containing OpenAI stream response in req.AnthropicRequest.OpenAIStreamResponse
//   - done: Indicates whether this is the final chunk in the stream
//
// Returns:
//   - error: Returns an error if conversion fails, nil on success
//
// Side effects:
//   - Sets req.AnthropicRequest.ResponseData with converted Anthropic SSE event data
//   - Updates req.AnthropicRequest.IsNotFirst flag to track streaming state
//   - Modifies req.AnthropicRequest.StreamResponseBuffer to accumulate state across chunks
func (c *ResponseAnthropicConverter) PostStreamProcess(req *types.RequestContext, done bool) error {
	// Extract request context and OpenAI streaming response
	anthropicReq := req.AnthropicRequest
	oaiStreamResp := anthropicReq.OpenAIStreamResponse

	// Determine if this is the first chunk by checking the negated flag
	// First chunk requires special handling (message_start, content_block_start events)
	isFirst := !anthropicReq.IsNotFirst

	// Convert OpenAI streaming chunk to Anthropic SSE format
	// This handles:
	// - Initial events (message_start, content_block_start) for first chunk
	// - Delta events (content_block_delta) for text and tool call updates
	// - Final events (content_block_stop, message_delta, message_stop) for last chunk
	modifiedData, err := claude_code.ConvertOpenAIStreamingResponseToAnthropic(
		oaiStreamResp,
		isFirst,
		done,
		req.Id,
		anthropicReq.Request.Model,
		anthropicReq.StreamResponseBuffer)

	// Mark that the first chunk has been processed
	// Subsequent chunks will skip initial event generation
	anthropicReq.IsNotFirst = true

	if err != nil {
		klog.Errorf("req %s failed to convert openai to anthropic, err: %v", req.Id, err)
		return err
	}

	// Store the converted Anthropic SSE data for downstream response writing
	anthropicReq.ResponseData = modifiedData
	return nil
}

func (c *ResponseAnthropicConverter) PostProcess(req *types.RequestContext) error {
	// Extract request context and OpenAI streaming response
	anthropicReq := req.AnthropicRequest
	oaiResp := anthropicReq.OpenAIResponse

	anthropicResp, err := claude_code.ConvertOpenAIResponseToAnthropic(oaiResp, req.Id, anthropicReq.Request.Model)
	if err != nil {
		klog.Errorf("failed to convert openai to anthropic, err: %v", err)
		return nil
	}

	anthropicReq.ResponseData = anthropicResp
	return nil
}
