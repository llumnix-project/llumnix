package pre

import (
	"encoding/json"
	"fmt"
	"llm-gateway/pkg/gateway/processor/registry"
	"llm-gateway/pkg/gateway/processor/utils/anthropic"
	"llm-gateway/pkg/types"

	"k8s.io/klog/v2"
)

func init() {
	registry.RegisterPreProcessor("anthropic", func(params map[string]interface{}) registry.PreProcessor {
		return newAnthropicPreProcessor()
	})
}

// AnthropicPreProcessor is a pre-processor that converts Anthropic API requests to OpenAI format.
type AnthropicPreProcessor struct {
}

// newAnthropicPreProcessor creates and returns a new AnthropicPreProcessor instance.
func newAnthropicPreProcessor() *AnthropicPreProcessor {
	return &AnthropicPreProcessor{}
}

// Name returns the name of the processor.
func (a *AnthropicPreProcessor) Name() string {
	return "anthropic_pre_processor"
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
func (a *AnthropicPreProcessor) PreProcess(req *types.RequestContext) error {
	// Extract the original Anthropic request
	anthropicReq := req.AnthropicRequest.Request

	// Convert Anthropic request to OpenAI format
	// This handles message format, tool definitions, system prompts, and other protocol differences
	openaiReq, err := anthropic.ConvertAnthropicRequestToOpenAI(anthropicReq)
	if err != nil {
		return fmt.Errorf("req %v failed to convert anthropic to openai, err: %v", req.Id, err)
	}

	if klog.V(3).Enabled() {
		jsonData, _ := json.Marshal(openaiReq)
		klog.V(3).Infof("req %v converted anthropic to openai: %s", req.Id, jsonData)
	}

	// Store the converted request for downstream processors and handlers
	req.AnthropicRequest.OpenAIRequest = openaiReq
	return nil
}
