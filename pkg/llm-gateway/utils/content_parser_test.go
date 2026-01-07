package utils

import (
	"testing"

	"gotest.tools/assert"

	"easgo/pkg/llm-gateway/consts"
	"easgo/pkg/llm-gateway/protocol"
)

func TestUnmarshalResponse_Completion(t *testing.T) {
	// Test valid completion response
	completionData := []byte(`{
		"id": "cmpl-123",
		"object": "text_completion",
		"created": 1234567890,
		"model": "gpt-3.5-turbo-instruct",
		"choices": [
			{
				"text": "Hello, world!",
				"index": 0,
				"finish_reason": "stop"
			}
		],
		"usage": {
			"prompt_tokens": 10,
			"completion_tokens": 5,
			"total_tokens": 15
		}
	}`)

	response, err := UnmarshalResponse(protocol.ProtocolCompletions, completionData)
	assert.NilError(t, err)

	completionResp, ok := response.(*protocol.CompletionResponse)
	assert.Assert(t, ok)
	assert.Equal(t, completionResp.ID, "cmpl-123")
	assert.Equal(t, completionResp.Model, "gpt-3.5-turbo-instruct")
	assert.Equal(t, len(completionResp.Choices), 1)
	assert.Equal(t, completionResp.Choices[0].Text, "Hello, world!")
	assert.Equal(t, completionResp.Usage.PromptTokens, uint64(10))
	assert.Equal(t, completionResp.Usage.CompletionTokens, uint64(5))
	assert.Equal(t, completionResp.Usage.TotalTokens, uint64(15))
}

func TestUnmarshalResponse_ChatCompletion(t *testing.T) {
	// Test valid chat completion response
	chatData := []byte(`{
		"id": "chatcmpl-123",
		"object": "chat.completion",
		"created": 1234567890,
		"model": "gpt-4",
		"choices": [
			{
				"index": 0,
				"message": {
					"role": "assistant",
					"content": "Hello! How can I help you today?"
				},
				"finish_reason": "stop"
			}
		],
		"usage": {
			"prompt_tokens": 20,
			"completion_tokens": 10,
			"total_tokens": 30
		}
	}`)

	response, err := UnmarshalResponse(protocol.ProtocolChat, chatData)
	assert.NilError(t, err)

	chatResp, ok := response.(*protocol.ChatCompletionResponse)
	assert.Assert(t, ok)
	assert.Equal(t, chatResp.ID, "chatcmpl-123")
	assert.Equal(t, chatResp.Model, "gpt-4")
	assert.Equal(t, len(chatResp.Choices), 1)
	assert.Equal(t, chatResp.Choices[0].Message.Content, "Hello! How can I help you today?")
	assert.Equal(t, chatResp.Usage.PromptTokens, uint64(20))
	assert.Equal(t, chatResp.Usage.CompletionTokens, uint64(10))
	assert.Equal(t, chatResp.Usage.TotalTokens, uint64(30))
}

func TestUnmarshalResponse_TokenizedChat(t *testing.T) {
	// Test valid tokenized chat completion response
	tokenizedChatData := []byte(`{
		"id": "chatcmpl-456",
		"object": "chat.completion",
		"created": 1234567891,
		"model": "gpt-4-turbo",
		"choices": [
			{
				"index": 0,
				"message": {
					"role": "assistant",
					"content": "I'm here to assist you!",
					"reasoning_content": "Let me think about this..."
				},
				"finish_reason": "stop"
			}
		],
		"usage": {
			"prompt_tokens": 25,
			"completion_tokens": 15,
			"total_tokens": 40
		}
	}`)

	response, err := UnmarshalResponse(protocol.ProtocolTokenizedChat, tokenizedChatData)
	assert.NilError(t, err)

	chatResp, ok := response.(*protocol.ChatCompletionResponse)
	assert.Assert(t, ok)
	assert.Equal(t, chatResp.ID, "chatcmpl-456")
	assert.Equal(t, chatResp.Model, "gpt-4-turbo")
	assert.Equal(t, len(chatResp.Choices), 1)
	assert.Equal(t, chatResp.Choices[0].Message.Content, "I'm here to assist you!")
	assert.Equal(t, chatResp.Choices[0].Message.ReasoningContent, "Let me think about this...")
}

func TestUnmarshalResponse_InvalidJSON(t *testing.T) {
	// Test invalid JSON data
	invalidData := []byte(`{invalid json}`)

	response, err := UnmarshalResponse(protocol.ProtocolCompletions, invalidData)
	assert.Assert(t, err != nil)
	assert.Assert(t, response == nil)
}

func TestUnmarshalResponse_UnsupportedProtocol(t *testing.T) {
	// Test unsupported protocol type
	validData := []byte(`{"id": "test"}`)

	response, err := UnmarshalResponse(protocol.ProtocolUnknown, validData)
	assert.Assert(t, err != nil)
	assert.Equal(t, err, consts.ErrorUnSupportedProtocol)
	assert.Assert(t, response == nil)
}

func TestGetCompletionText(t *testing.T) {
	// Test with multiple choices
	response := &protocol.CompletionResponse{
		Choices: []protocol.CompletionChoice{
			{Text: "Hello"},
			{Text: " World"},
			{Text: "!"},
		},
	}

	text := GetCompletionText(response)
	assert.Equal(t, text, "Hello World!")
}

func TestGetCompletionText_EmptyChoices(t *testing.T) {
	// Test with empty choices
	response := &protocol.CompletionResponse{
		Choices: []protocol.CompletionChoice{},
	}

	text := GetCompletionText(response)
	assert.Equal(t, text, "")
}

func TestGetChatCompletionText_ContentOnly(t *testing.T) {
	// Test with content only (no reasoning content)
	response := &protocol.ChatCompletionResponse{
		Choices: []protocol.ChatCompletionChoice{
			{
				Message: protocol.ChatCompletionMessage{
					Content: "Hello, how can I help you?",
				},
			},
		},
	}

	text := GetChatCompletionText(response)
	assert.Equal(t, text, "Hello, how can I help you?")
}

func TestGetChatCompletionText_ReasoningContent(t *testing.T) {
	// Test with reasoning content
	response := &protocol.ChatCompletionResponse{
		Choices: []protocol.ChatCompletionChoice{
			{
				Message: protocol.ChatCompletionMessage{
					Content:          "The answer is 42",
					ReasoningContent: "Let me think... 40 + 2 = 42",
				},
			},
		},
	}

	text := GetChatCompletionText(response)
	assert.Equal(t, text, "Let me think... 40 + 2 = 42")
}

func TestGetChatCompletionText_MultipleChoices(t *testing.T) {
	// Test with multiple choices
	response := &protocol.ChatCompletionResponse{
		Choices: []protocol.ChatCompletionChoice{
			{
				Message: protocol.ChatCompletionMessage{
					Content: "First message",
				},
			},
			{
				Message: protocol.ChatCompletionMessage{
					Content:          "Second message",
					ReasoningContent: "Reasoning for second",
				},
			},
		},
	}

	text := GetChatCompletionText(response)
	assert.Equal(t, text, "First messageReasoning for second")
}

func TestGetResponseText_Completion(t *testing.T) {
	// Test completion response text extraction
	completionResponse := &protocol.CompletionResponse{
		Choices: []protocol.CompletionChoice{
			{Text: "Hello"},
			{Text: " World"},
		},
	}

	text := GetResponseText(protocol.ProtocolCompletions, completionResponse)
	assert.Equal(t, text, "Hello World")
}

func TestGetResponseText_ChatCompletion(t *testing.T) {
	// Test chat completion response text extraction
	chatResponse := &protocol.ChatCompletionResponse{
		Choices: []protocol.ChatCompletionChoice{
			{
				Message: protocol.ChatCompletionMessage{
					Content: "Hello there!",
				},
			},
		},
	}

	text := GetResponseText(protocol.ProtocolChat, chatResponse)
	assert.Equal(t, text, "Hello there!")
}

func TestGetResponseText_TokenizedChat(t *testing.T) {
	// Test tokenized chat response text extraction
	chatResponse := &protocol.ChatCompletionResponse{
		Choices: []protocol.ChatCompletionChoice{
			{
				Message: protocol.ChatCompletionMessage{
					Content:          "Final answer",
					ReasoningContent: "Thinking process",
				},
			},
		},
	}

	text := GetResponseText(protocol.ProtocolTokenizedChat, chatResponse)
	assert.Equal(t, text, "Thinking process")
}

func TestGetResponseText_UnsupportedProtocol(t *testing.T) {
	// Test unsupported protocol type (should panic)
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("Expected panic for unsupported protocol")
		}
	}()

	GetResponseText(protocol.ProtocolTokenizedChat, nil)
}

func TestGetResponseUsage_Completion(t *testing.T) {
	// Test completion response usage extraction
	usage := &protocol.Usage{
		PromptTokens:     10,
		CompletionTokens: 5,
		TotalTokens:      15,
	}

	completionResponse := &protocol.CompletionResponse{
		Usage: usage,
	}

	result := GetResponseUsage(completionResponse)
	assert.Equal(t, result, usage)
}

func TestGetResponseUsage_ChatCompletion(t *testing.T) {
	// Test chat completion response usage extraction
	usage := &protocol.Usage{
		PromptTokens:     20,
		CompletionTokens: 10,
		TotalTokens:      30,
	}

	chatResponse := &protocol.ChatCompletionResponse{
		Usage: usage,
	}

	result := GetResponseUsage(chatResponse)
	assert.Equal(t, result, usage)
}

func TestGetResponseUsage_NilUsage(t *testing.T) {
	// Test response with nil usage
	completionResponse := &protocol.CompletionResponse{
		Usage: nil,
	}

	result := GetResponseUsage(completionResponse)
	assert.Assert(t, result == nil)
}

func TestGetResponseUsage_UnsupportedType(t *testing.T) {
	// Test unsupported response type (should panic)
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("Expected panic for unsupported response type")
		}
	}()

	GetResponseUsage("invalid type")
}
