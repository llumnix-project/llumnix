package anthropic

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"k8s.io/klog/v2"

	"llm-gateway/pkg/consts"
	"llm-gateway/pkg/gateway/protocol"
	"llm-gateway/pkg/gateway/protocol/anthropic"
)

func TestConvertOpenAIResponseToAnthropic(t *testing.T) {
	t.Run("Text content only response", func(t *testing.T) {
		openaiResp := &protocol.ChatCompletionResponse{
			ID:    "test-id",
			Model: "gpt-3.5-turbo",
			Choices: []protocol.ChatCompletionChoice{
				{
					Index: 0,
					Message: protocol.ChatCompletionMessage{
						Role:    "assistant",
						Content: "Hello, this is a test response",
					},
					FinishReason: consts.FINISH_REASON_STOP,
				},
			},
			Usage: &protocol.Usage{
				PromptTokens:     10,
				CompletionTokens: 20,
			},
		}

		messageId := "anthropic-test-id"
		originModel := "claude-3-haiku-20240307"

		result, err := ConvertOpenAIResponseToAnthropic(openaiResp, messageId, originModel)
		if err != nil {
			t.Errorf("ConvertOpenAIResponseToAnthropic() error = %v", err)
			return
		}

		var anthropicResp anthropic.Response
		if err := json.Unmarshal(result, &anthropicResp); err != nil {
			t.Errorf("Failed to unmarshal result: %v", err)
			return
		}

		if anthropicResp.ID != messageId {
			t.Errorf("Expected ID %s, got %s", messageId, anthropicResp.ID)
		}

		if anthropicResp.Model != originModel {
			t.Errorf("Expected Model %s, got %s", originModel, anthropicResp.Model)
		}

		if len(anthropicResp.Content) != 1 {
			t.Errorf("Expected 1 content block, got %d", len(anthropicResp.Content))
			return
		}

		contentBlock, ok := anthropicResp.Content[0].(map[string]interface{})
		if !ok {
			t.Error("Failed to convert content block to map")
			return
		}

		if contentBlock["type"] != consts.CONTENT_TEXT {
			t.Errorf("Expected content type %s, got %s", consts.CONTENT_TEXT, contentBlock["type"])
		}

		if contentBlock["text"] != "Hello, this is a test response" {
			t.Errorf("Expected text 'Hello, this is a test response', got %s", contentBlock["text"])
		}

		if *anthropicResp.StopReason != consts.STOP_END_TURN {
			t.Errorf("Expected stop reason %s, got %s", consts.STOP_END_TURN, *anthropicResp.StopReason)
		}

		if anthropicResp.Usage.InputTokens != 10 {
			t.Errorf("Expected input tokens 10, got %d", anthropicResp.Usage.InputTokens)
		}

		if anthropicResp.Usage.OutputTokens != 20 {
			t.Errorf("Expected output tokens 20, got %d", anthropicResp.Usage.OutputTokens)
		}
	})

	t.Run("Response with tool calls", func(t *testing.T) {
		arguments, err := json.Marshal(map[string]interface{}{
			"location": "San Francisco, CA",
			"unit":     "celsius",
		})
		argumentsStr := string(arguments)

		openaiResp := &protocol.ChatCompletionResponse{
			ID:    "test-id",
			Model: "gpt-3.5-turbo",
			Choices: []protocol.ChatCompletionChoice{
				{
					Index: 0,
					Message: protocol.ChatCompletionMessage{
						Role:    "assistant",
						Content: "test",
						ToolCalls: []protocol.ToolCall{
							{
								ID:   "call_123456",
								Type: consts.TOOL_FUNCTION,
								Function: protocol.FunctionCall{
									Name:      "get_current_weather",
									Arguments: argumentsStr,
								},
							},
						},
					},
					FinishReason: consts.FINISH_REASON_TOOL_CALLS,
				},
			},
			Usage: &protocol.Usage{
				PromptTokens:     15,
				CompletionTokens: 25,
			},
		}

		messageId := "anthropic-test-id"
		originModel := "claude-3-opus-20240229"

		result, err := ConvertOpenAIResponseToAnthropic(openaiResp, messageId, originModel)
		if err != nil {
			t.Errorf("ConvertOpenAIResponseToAnthropic() error = %v", err)
			return
		}

		var anthropicResp anthropic.Response
		if err := json.Unmarshal(result, &anthropicResp); err != nil {
			t.Errorf("Failed to unmarshal result: %v", err)
			return
		}
		fmt.Printf("Anthropic Response: %v\n", anthropicResp)

		if len(anthropicResp.Content) != 2 {
			t.Errorf("Expected 2 content block, got %d", len(anthropicResp.Content))
			return
		}

		contentBlock, ok := anthropicResp.Content[1].(map[string]interface{})
		if !ok {
			t.Error("Failed to convert content block to map")
			return
		}

		if contentBlock["type"] != consts.CONTENT_TOOL_USE {
			t.Errorf("Expected content type %s, got %s", consts.CONTENT_TOOL_USE, contentBlock["type"])
		}

		if contentBlock["id"] != "call_123456" {
			t.Errorf("Expected id 'call_123456', got %s", contentBlock["id"])
		}

		if contentBlock["name"] != "get_current_weather" {
			t.Errorf("Expected name 'get_current_weather', got %s", contentBlock["name"])
		}

		if *anthropicResp.StopReason != consts.STOP_TOOL_USE {
			t.Errorf("Expected stop reason %s, got %s", consts.STOP_TOOL_USE, *anthropicResp.StopReason)
		}
	})

	t.Run("Empty content response", func(t *testing.T) {
		openaiResp := &protocol.ChatCompletionResponse{
			ID:    "test-id",
			Model: "gpt-3.5-turbo",
			Choices: []protocol.ChatCompletionChoice{
				{
					Index: 0,
					Message: protocol.ChatCompletionMessage{
						Role:    "assistant",
						Content: "",
					},
					FinishReason: consts.FINISH_REASON_STOP,
				},
			},
			Usage: &protocol.Usage{
				PromptTokens:     5,
				CompletionTokens: 10,
			},
		}

		messageId := "anthropic-test-id"
		originModel := "claude-3-sonnet-20240229"

		result, err := ConvertOpenAIResponseToAnthropic(openaiResp, messageId, originModel)
		if err != nil {
			t.Errorf("ConvertOpenAIResponseToAnthropic() error = %v", err)
			return
		}

		var anthropicResp anthropic.Response
		if err := json.Unmarshal(result, &anthropicResp); err != nil {
			t.Errorf("Failed to unmarshal result: %v", err)
			return
		}

		if len(anthropicResp.Content) != 1 {
			t.Errorf("Expected 1 content block, got %d", len(anthropicResp.Content))
			return
		}

		contentBlock, ok := anthropicResp.Content[0].(map[string]interface{})
		if !ok {
			t.Error("Failed to convert content block to map")
			return
		}

		if contentBlock["type"] != consts.CONTENT_TEXT {
			t.Errorf("Expected content type %s, got %s", consts.CONTENT_TEXT, contentBlock["type"])
		}

		if contentBlock["text"] != "" {
			t.Errorf("Expected empty text, got %s", contentBlock["text"])
		}
	})

	t.Run("Response with no choices", func(t *testing.T) {
		openaiResp := &protocol.ChatCompletionResponse{
			ID:      "test-id",
			Model:   "gpt-3.5-turbo",
			Choices: []protocol.ChatCompletionChoice{},
			Usage:   &protocol.Usage{},
		}

		messageId := "anthropic-test-id"
		originModel := "claude-3-haiku-20240307"

		_, err := ConvertOpenAIResponseToAnthropic(openaiResp, messageId, originModel)
		if err == nil {
			t.Error("Expected error for response with no choices, but got none")
		}

		if err.Error() != "openai response has no choices" {
			t.Errorf("Expected error message 'openai response has no choices', got %s", err.Error())
		}
	})
}

func TestConvertOpenAIStreamingResponseToAnthropic(t *testing.T) {
	t.Run("First message", func(t *testing.T) {
		buffer := &anthropic.StreamingResponseBuffer{
			ToolCalls: make(map[string]*anthropic.ToolCall),
		}

		messageId := "test-message-id"
		originalModel := "claude-3-haiku-20240307"

		result, err := ConvertOpenAIStreamingResponseToAnthropic(nil, true, false, messageId, originalModel, buffer)
		klog.Info(string(result))

		if err != nil {
			t.Errorf("ConvertOpenAIStreamingResponseToAnthropic() error = %v", err)
			return
		}

		resultStr := string(result)

		if !strings.Contains(resultStr, "event: message_start") {
			t.Error("Expected message_start event in first message")
		}

		if !strings.Contains(resultStr, "event: content_block_start") {
			t.Error("Expected content_block_start event in first message")
		}

		if !strings.Contains(resultStr, "event: ping") {
			t.Error("Expected ping event in first message")
		}

		// Check if the correct message ID and model are included
		if !strings.Contains(resultStr, messageId) {
			t.Error("Expected message ID in message_start event")
		}

		if !strings.Contains(resultStr, originalModel) {
			t.Error("Expected model in message_start event")
		}
	})

	t.Run("Last message", func(t *testing.T) {
		buffer := &anthropic.StreamingResponseBuffer{
			ToolCalls:       make(map[string]*anthropic.ToolCall),
			FinalStopReason: consts.STOP_END_TURN,
			Usage: anthropic.Usage{
				InputTokens:  10,
				OutputTokens: 20,
			},
		}

		// doneMessage := "data: [DONE]\n\n"

		result, err := ConvertOpenAIStreamingResponseToAnthropic(nil, false, true, "test-id", "test-model", buffer)
		klog.Info(string(result))

		if err != nil {
			t.Errorf("ConvertOpenAIStreamingResponseToAnthropic() error = %v", err)
			return
		}

		resultStr := string(result)

		// Check if content_block_stop event is included
		if !strings.Contains(resultStr, "event: content_block_stop") {
			t.Error("Expected content_block_stop event in last message")
		}

		// Check if message_delta event is included
		if !strings.Contains(resultStr, "event: message_delta") {
			t.Error("Expected message_delta event in last message")
		}

		// Check if message_stop event is included
		if !strings.Contains(resultStr, "event: message_stop") {
			t.Error("Expected message_stop event in last message")
		}

		// Check if [DONE] data is included
		if !strings.Contains(resultStr, "data: [DONE]") {
			t.Error("Expected [DONE] data in last message")
		}

		// Check if stop reason is included
		if !strings.Contains(resultStr, consts.STOP_END_TURN) {
			t.Error("Expected stop reason in message_delta event")
		}
	})

	t.Run("Middle stream message with text content", func(t *testing.T) {
		buffer := &anthropic.StreamingResponseBuffer{
			ToolCalls: make(map[string]*anthropic.ToolCall),
		}

		openaiStreamResponse := &protocol.ChatCompletionStreamResponse{
			ID:    "test-stream-id",
			Model: "gpt-3.5-turbo",
			Choices: []protocol.ChatCompletionStreamChoice{
				{
					Index: 0,
					Delta: protocol.ChatCompletionStreamChoiceDelta{
						Content: "Hello, this is a test message",
					},
					FinishReason: "",
				},
			},
		}

		result, err := ConvertOpenAIStreamingResponseToAnthropic(openaiStreamResponse, false, false, "test-id", "test-model", buffer)
		klog.Info(string(result))

		if err != nil {
			t.Errorf("ConvertOpenAIStreamingResponseToAnthropic() error = %v", err)
			return
		}

		resultStr := string(result)

		// Check if content_block_delta event is included
		if !strings.Contains(resultStr, "event: content_block_delta") {
			t.Error("Expected content_block_delta event in middle message")
		}

		// Check if text content is included
		if !strings.Contains(resultStr, "Hello, this is a test message") {
			t.Error("Expected text content in content_block_delta event")
		}
	})

	t.Run("Stream message with tool calls", func(t *testing.T) {
		buffer := &anthropic.StreamingResponseBuffer{
			ToolCalls: make(map[string]*anthropic.ToolCall),
		}

		zeroIndex := 0
		openaiStreamResponse := &protocol.ChatCompletionStreamResponse{
			ID:    "test-stream-id",
			Model: "gpt-3.5-turbo",
			Choices: []protocol.ChatCompletionStreamChoice{
				{
					Index: 0,
					Delta: protocol.ChatCompletionStreamChoiceDelta{
						ToolCalls: []protocol.ToolCall{
							{
								Index: &zeroIndex,
								ID:    "call_123456",
								Function: protocol.FunctionCall{
									Name:      "get_current_weather",
									Arguments: `{"location": "San Francisco, CA"}`,
								},
							},
						},
					},
					FinishReason: "",
				},
			},
		}

		// jsonData, _ := json.Marshal(openaiStreamResponse)
		// streamData := fmt.Sprintf("data: %s\n\n", string(jsonData))

		result, err := ConvertOpenAIStreamingResponseToAnthropic(openaiStreamResponse, false, false, "test-id", "test-model", buffer)
		klog.Info(string(result))

		if err != nil {
			t.Errorf("ConvertOpenAIStreamingResponseToAnthropic() error = %v", err)
			return
		}

		resultStr := string(result)

		if !strings.Contains(resultStr, "event: content_block_start") {
			t.Error("Expected content_block_start event for tool call")
		}

		if !strings.Contains(resultStr, "call_123456") {
			t.Error("Expected tool call ID in content_block_start event")
		}

		if !strings.Contains(resultStr, "get_current_weather") {
			t.Error("Expected tool call name in content_block_start event")
		}
	})
}
