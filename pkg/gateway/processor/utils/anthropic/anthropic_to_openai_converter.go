package anthropic

import (
	"encoding/json"
	"fmt"
	"strings"

	"k8s.io/klog/v2"

	"llm-gateway/pkg/consts"
	"llm-gateway/pkg/gateway/protocol"
	"llm-gateway/pkg/gateway/protocol/anthropic"
)

// ConvertAnthropicRequestToOpenAI converts Anthropic API request format to OpenAI format
func ConvertAnthropicRequestToOpenAI(anthropicReq *anthropic.Request) (*protocol.ChatCompletionRequest, error) {
	openaiReq := &protocol.ChatCompletionRequest{
		Model:       "", // For SGLang or vLLM, the model name can be omitted; an empty string is used to indicate that the default model should be used.
		MaxTokens:   &(anthropicReq.MaxTokens),
		Temperature: anthropicReq.Temperature,
		TopP:        anthropicReq.TopP,
		Stream:      anthropicReq.Stream,
		Stop:        anthropicReq.StopSequences,
	}

	// Enable usage reporting for streaming requests (supported by OpenAI, vLLM, SGLang)
	if anthropicReq.Stream {
		openaiReq.StreamOptions = &protocol.StreamOptions{
			IncludeUsage:         true,
			ContinuousUsageStats: true,
		}
	}

	// System message is the model's goal or role. If a system message is set, it should be placed at the beginning of the messages list.
	// For QwQ models, setting a System Message is not recommended. For QVQ models, the System Message will not take effect.
	if anthropicReq.System != nil {
		systemText := extractSystemText(anthropicReq.System)
		if systemText != "" {
			openaiReq.Messages = append(openaiReq.Messages,
				protocol.ChatCompletionMessage{
					Role:    consts.ROLE_SYSTEM,
					Content: systemText,
				})
		}
	}

	// Process messages
	messages, err := convertMessages(anthropicReq.Messages)
	if err != nil {
		return nil, fmt.Errorf("error processing messages: %v", err)
	}
	openaiReq.Messages = append(openaiReq.Messages, messages...)

	// Convert tools
	if anthropicReq.Tools != nil {
		openaiReq.Tools = convertTools(anthropicReq.Tools)
	}

	// Convert tool choice
	if anthropicReq.ToolChoice != nil {
		openaiReq.ToolChoice = convertToolChoice(anthropicReq.ToolChoice)
	}

	return openaiReq, nil
}

// convertMessages converts Anthropic messages to OpenAI format
func convertMessages(messages []anthropic.Message) ([]protocol.ChatCompletionMessage, error) {
	var openaiMessages []protocol.ChatCompletionMessage

	for _, msg := range messages {
		if msg.Content == nil {
			continue
		}

		if msg.Role == consts.ROLE_USER {
			openaiMessages = append(openaiMessages, convertUserMessage(&msg)...)
		} else if msg.Role == consts.ROLE_ASSISTANT {
			openaiMessages = append(openaiMessages, convertAssistantMessage(&msg)...)
		}
	}
	return openaiMessages, nil
}

func extractText(content any) string {
	switch item := content.(type) {
	case string:
		return item
	case map[string]any:
		if item["type"] == consts.CONTENT_TEXT {
			return item["text"].(string)
		} else if item["text"] != nil {
			return item["text"].(string)
		} else {
			return MapToString(item)
		}
	}
	return ""
}

func hasToolResult(blocks []map[string]any) bool {
	for _, block := range blocks {
		if block["type"] == consts.CONTENT_TOOL_RESULT {
			return true
		}
	}
	return false
}

func parseToolResultContent(rc any) string {
	switch content := rc.(type) {
	case string:
		return content
	case []any:
		var results []string
		for _, item := range content {
			if text := extractText(item); text != "" {
				results = append(results, text)
			}
		}
		return strings.TrimSpace(strings.Join(results, "\n"))
	default:
		return extractText(content)
	}
}

func parseToolCall(content map[string]any) *protocol.ToolCall {
	id, ok := content["id"].(string)
	if !ok || id == "" {
		return nil
	}

	name, ok := content["name"].(string)
	if !ok || name == "" {
		return nil
	}

	jsonData, err := json.Marshal(content["input"])
	if err != nil {
		klog.Warningf("failed to marshal tool_call input: %v, err: %v", content["input"], err)
		return nil
	}
	arguments := string(jsonData)

	return &protocol.ToolCall{
		ID:   id,
		Type: consts.TOOL_FUNCTION,
		Function: protocol.FunctionCall{
			Name:      name,
			Arguments: arguments,
		},
	}
}

func parseToolResultMessages(content []map[string]any) []protocol.ChatCompletionMessage {
	var openaiMsgs []protocol.ChatCompletionMessage

	for _, block := range content {
		if block["type"] == consts.CONTENT_TOOL_RESULT {
			if block["content"] == nil {
				continue
			}

			rc := parseToolResultContent(block["content"])
			msg := protocol.ChatCompletionMessage{
				Role:    consts.ROLE_TOOL,
				Content: rc,
			}
			if toolCallId, ok := block["tool_use_id"].(string); ok {
				msg.ToolCallID = toolCallId
			}
			openaiMsgs = append(openaiMsgs, msg)
		}
	}

	return openaiMsgs
}

func parseTextBlock(block map[string]any) *protocol.ChatMessagePart {
	return &protocol.ChatMessagePart{
		Type: consts.CONTENT_TEXT,
		Text: block["text"].(string),
	}
}

func parseImageBlock(block map[string]any) *protocol.ChatMessagePart {
	source, ok := block["source"].(map[string]any)
	if !ok {
		return nil
	}

	if source["type"] == "base64" {
		if source["media_type"] == nil ||
			source["data"] == nil {
			return nil
		}
		return &protocol.ChatMessagePart{
			Type: consts.CONTENT_IMAGE,
			ImageURL: &protocol.ChatMessageImageURL{
				URL: "data:" + source["media_type"].(string) + ";base64," + source["data"].(string),
			},
		}
	} else if source["type"] == "url" {
		if source["url"] == nil {
			return nil
		}
		return &protocol.ChatMessagePart{
			Type: consts.CONTENT_IMAGE,
			ImageURL: &protocol.ChatMessageImageURL{
				URL: source["url"].(string),
			},
		}
	}
	return nil
}

func convertUserMessage(msg *anthropic.Message) []protocol.ChatCompletionMessage {
	var openaiMsgs []protocol.ChatCompletionMessage

	switch content := msg.Content.(type) {
	case string:
		openaiMsgs = append(openaiMsgs, protocol.ChatCompletionMessage{
			Role:    msg.Role,
			Content: content,
		})
	case []any:
		blocks, ok := ConvertSliceAnyToMapStringAny(content)
		if !ok {
			klog.Warningf("failed to convert content to []map[string]any, content: %v", content)
			return openaiMsgs
		}

		if hasToolResult(blocks) {
			openaiMsgs = append(openaiMsgs, parseToolResultMessages(blocks)...)
		} else {
			var openaiContent []protocol.ChatMessagePart
			for _, block := range blocks {
				if block["type"] == consts.CONTENT_TEXT {
					textBlock := parseTextBlock(block)
					if textBlock != nil {
						openaiContent = append(openaiContent, *textBlock)
					}
				} else if block["type"] == consts.CONTENT_IMAGE {
					imageBlock := parseImageBlock(block)
					if imageBlock != nil {
						openaiContent = append(openaiContent, *imageBlock)
					}
				}
			}
			if len(openaiContent) > 0 {
				openaiMsgs = append(openaiMsgs, protocol.ChatCompletionMessage{
					Role:         msg.Role,
					MultiContent: openaiContent,
				})
			}
		}
	default:
		klog.Warningf("unknown content type: %T", content)
	}

	return openaiMsgs
}

func convertAssistantMessage(msg *anthropic.Message) []protocol.ChatCompletionMessage {
	var openaiMsgs []protocol.ChatCompletionMessage

	switch content := msg.Content.(type) {
	case string:
		openaiMsgs = append(openaiMsgs, protocol.ChatCompletionMessage{
			Role:    msg.Role,
			Content: content,
		})
	case []any:
		blocks, ok := ConvertSliceAnyToMapStringAny(content)
		if !ok {
			klog.Warningf("failed to convert content to []map[string]any, content: %v", content)
			return openaiMsgs
		}

		var text string
		var toolCalls []protocol.ToolCall

		for _, block := range blocks {
			if block["type"] == consts.CONTENT_TEXT {
				text += block["text"].(string)
			} else if block["type"] == consts.CONTENT_TOOL_USE {
				toolCall := parseToolCall(block)
				if toolCall == nil {
					continue
				}
				toolCalls = append(toolCalls, *toolCall)
			}
		}
		openaiMsg := protocol.ChatCompletionMessage{
			Role: msg.Role,
		}
		if text != "" {
			openaiMsg.Content = text
		}
		if len(toolCalls) != 0 {
			openaiMsg.ToolCalls = toolCalls
		}
		openaiMsgs = append(openaiMsgs, openaiMsg)
	}
	return openaiMsgs
}

func MapToString(data map[string]any) string {
	bytes, err := json.Marshal(data)
	if err != nil {
		return ""
	}
	return string(bytes)
}

func extractSystemText(system any) string {
	if system == nil {
		return ""
	}

	switch s := system.(type) {
	case string:
		// Simple string format - process cch value
		return replaceCchValue(s)
	case []any:
		// List of content blocks
		systemText := ""
		for _, block := range s {
			blockMap, ok := block.(map[string]any)
			if !ok {
				continue
			}

			if t, ok := blockMap["type"].(string); !ok || t != consts.CONTENT_TEXT {
				continue
			}

			if text, ok := blockMap["text"].(string); ok && text != "" {
				// Process cch value in each text block
				text = replaceCchValue(text)
				systemText += text + "\n\n"
			}
		}
		return strings.TrimSpace(systemText)
	default:
		return ""
	}
}

func convertToolChoice(toolChoice map[string]string) any {
	if toolChoice["type"] == consts.TOOL {
		return protocol.ToolChoice{
			Type: consts.TOOL_FUNCTION,
			Function: protocol.ToolFunction{
				Name: toolChoice["name"],
			},
		}
	}
	return consts.TOOL_CHOICE_AUTO
}

func convertTools(anthropicTools []anthropic.Tool) []protocol.Tool {
	var openaiTools []protocol.Tool
	for _, tool := range anthropicTools {
		openaiTools = append(openaiTools, protocol.Tool{
			Type: consts.TOOL_FUNCTION,
			Function: &protocol.FunctionDefinition{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.InputSchema,
			},
		})
	}
	return openaiTools
}

func ConvertSliceAnyToMapStringAny(input []any) ([]map[string]any, bool) {
	result := make([]map[string]any, 0, len(input))
	for _, item := range input {
		m, ok := item.(map[string]any)
		if !ok {
			return nil, false
		}
		result = append(result, m)
	}
	return result, true
}

// replaceCchValue replaces the cch value in x-anthropic-billing-header to "00000"
func replaceCchValue(text string) string {
	// Check if this text contains x-anthropic-billing-header with cch
	if strings.Contains(text, "x-anthropic-billing-header:") && strings.Contains(text, "cch=") {
		// Use regex to replace cch=xxxxx; with cch=00000;
		// Pattern matches cch= followed by any characters until semicolon
		result := text
		// Find and replace cch value
		idx := strings.Index(result, "cch=")
		if idx != -1 {
			start := idx + 4 // len("cch=")
			end := strings.Index(result[start:], ";")
			if end != -1 {
				end += start
				// Replace the value between cch= and ;
				result = result[:start] + "00000" + result[end:]
			}
		}
		return result
	}
	return text
}
