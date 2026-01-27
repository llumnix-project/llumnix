package processor

import (
	"fmt"
	claude_code "llm-gateway/pkg/processor/claude-code"
	"llm-gateway/pkg/types"
)

// RequestAnthropicConverter is a pre-processor that converts Anthropic API requests to OpenAI format.
type RequestAnthropicConverter struct {
}

// NewRequestAnthropicConverter creates and returns a new RequestAnthropicConverter instance.
func NewRequestAnthropicConverter() *RequestAnthropicConverter {
	return &RequestAnthropicConverter{}
}

// Name returns the name of the processor.
func (a *RequestAnthropicConverter) Name() string {
	return "RequestAnthropicConverter"
}

// PreProcess implements the PreProcessor interface.
// It converts the Anthropic API request to OpenAI format and stores the result in RequestContext.
//
// Parameters:
//   - req: RequestContext containing the original Anthropic request in req.AnthropicRequest.Request
//
// Returns:
//   - error: Returns an error if conversion fails, nil on success
//
// Side effects:
//   - Sets req.AnthropicRequest.OpenAIRequest with the converted OpenAI format request
func (a *RequestAnthropicConverter) PreProcess(req *types.RequestContext) error {
	// Extract the original Anthropic request
	anthropicReq := req.AnthropicRequest.AnthropicRequest

	// Convert Anthropic request to OpenAI format
	// This handles message format, tool definitions, system prompts, and other protocol differences
	openaiReq, err := claude_code.ConvertAnthropicRequestToOpenAI(anthropicReq)
	if err != nil {
		return fmt.Errorf("req %v failed to convert anthropic to openai, err: %v", req.Id, err)
	}

	// Store the converted request for downstream processors and handlers
	req.AnthropicRequest.OpenAIRequest = openaiReq
	return nil
}
