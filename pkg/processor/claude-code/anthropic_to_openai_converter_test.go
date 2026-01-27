package claude_code

import (
	"encoding/json"
	"testing"

	"github.com/influxdata/influxdb/pkg/deep"

	"llm-gateway/pkg/protocol"
	"llm-gateway/pkg/protocol/anthropic"
)

func TestConvertTools(t *testing.T) {
	tests := []struct {
		name               string
		inputJSON          string
		expectedOutputJSON string
	}{
		{
			name: "Single tool example",
			inputJSON: `[
				{
					"name": "get_weather", 
					"description": "Get the current weather in a given location", 
					"input_schema": {
						"type": "object", 
						"properties": {
							"location": {
								"type": "string", 
								"description": "The city and state, e.g. San Francisco, CA"
							}
						}, 
						"required": ["location"]
					}
				}
			]`,
			expectedOutputJSON: `[
				{
					"type": "function",
					"function": {
						"name": "get_weather",
						"description": "Get the current weather in a given location",
						"parameters": {
							"type": "object",
							"properties": {
								"location": {
									"type": "string", 
									"description": "The city and state, e.g. San Francisco, CA"
								}
							},
							"required": ["location"]
						}
					}
				}
			]`,
		},
		{
			name: "Parallel tool use",
			inputJSON: `[{
				"name": "get_weather",
				"description": "Get the current weather in a given location",
				"input_schema": {
					"type": "object",
					"properties": {
						"location": {
							"type": "string",
							"description": "The city and state, e.g. San Francisco, CA"
						},
						"unit": {
							"type": "string",
							"enum": ["celsius", "fahrenheit"],
							"description": "The unit of temperature, either 'celsius' or 'fahrenheit'"
						}
					},
					"required": ["location"]
				}
			},
			{
				"name": "get_time",
				"description": "Get the current time in a given time zone",
				"input_schema": {
					"type": "object",
					"properties": {
						"timezone": {
							"type": "string",
							"description": "The IANA time zone name, e.g. America/Los_Angeles"
						}
					},
					"required": ["timezone"]
				}
			}]`,
			expectedOutputJSON: `[{
				"type": "function",
				"function": {
				  "name": "get_weather",
				  "description": "Get the current weather in a given location",
				  "parameters": {
					"type": "object",
					"properties": {
					  "location": {
						"type": "string",
						"description": "The city and state, e.g. San Francisco, CA"
					  },
					  "unit": {
						"type": "string",
						"enum": ["celsius", "fahrenheit"],
						"description": "The unit of temperature, either 'celsius' or 'fahrenheit'"
					  }
					},
					"required": ["location"]
				  }
				}
			  },
			  {
				"type": "function",
				"function": {
				  "name": "get_time",
				  "description": "Get the current time in a given time zone",
				  "parameters": {
					"type": "object",
					"properties": {
					  "timezone": {
						"type": "string",
						"description": "The IANA time zone name, e.g. America/Los_Angeles"
					  }
					},
					"required": ["timezone"]
				  }
				}
			  }]`,
		},
		{
			name: "Sequential tools",
			inputJSON: `[{
				"name": "get_location",
				"description": "Get the current user location based on their IP address. This tool has no parameters or arguments.",
				"input_schema": {
					"type": "object",
					"properties": {}
				}
			},
			{
				"name": "get_weather",
				"description": "Get the current weather in a given location",
				"input_schema": {
					"type": "object",
					"properties": {
						"location": {
							"type": "string",
							"description": "The city and state, e.g. San Francisco, CA"
						},
						"unit": {
							"type": "string",
							"enum": ["celsius", "fahrenheit"],
							"description": "The unit of temperature, either 'celsius' or 'fahrenheit'"
						}
					},
					"required": ["location"]
				}
			}]`,
			expectedOutputJSON: `[{
			  "type": "function",
			  "function": {
				"name": "get_location",
				"description": "Get the current user location based on their IP address. This tool has no parameters or arguments.",
				"parameters": {
				  "type": "object",
				  "properties": {}
				}
			  }
			},
			{
			  "type": "function",
			  "function": {
				"name": "get_weather",
				"description": "Get the current weather in a given location",
				"parameters": {
				  "type": "object",
				  "properties": {
					"location": {
					  "type": "string",
					  "description": "The city and state, e.g. San Francisco, CA"
					},
					"unit": {
					  "type": "string",
					  "enum": ["celsius", "fahrenheit"],
					  "description": "The unit of temperature, either 'celsius' or 'fahrenheit'"
					}
				  },
				  "required": ["location"]
				}
			  }
			}]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var anthropicTools []anthropic.Tool
			var openaiTools []protocol.Tool

			err := json.Unmarshal([]byte(tt.inputJSON), &anthropicTools)
			if err != nil {
				t.Fatalf("Failed to unmarshal input JSON: %v", err)
			}

			err = json.Unmarshal([]byte(tt.expectedOutputJSON), &openaiTools)
			if err != nil {
				t.Fatalf("Failed to unmarshal output JSON: %v", err)
			}

			tools := convertTools(anthropicTools)

			// Compare the results
			if !deep.Equal(tools, openaiTools) {
				output, _ := json.MarshalIndent(tools, "", "  ")
				want, _ := json.MarshalIndent(openaiTools, "", "  ")
				t.Errorf("ConvertAnthropicRequestToOpenAI() = %s, want %s", output, string(want))
			}
		})
	}
}

func TestConvertSystemText(t *testing.T) {
	tests := []struct {
		name   string
		system any
		want   string
	}{
		{
			name:   "String system message",
			system: "You are a helpful assistant",
			want:   "You are a helpful assistant",
		},
		{
			name: "Array system message with text blocks",
			system: []any{
				map[string]any{
					"type": "text",
					"text": "You are a helpful assistant",
				},
			},
			want: "You are a helpful assistant",
		},
		{
			name: "Array system message with multiple text blocks",
			system: []any{
				map[string]any{
					"type": "text",
					"text": "You are a helpful assistant",
				},
				map[string]any{
					"type": "text",
					"text": "Answer concisely",
				},
			},
			want: "You are a helpful assistant\n\nAnswer concisely",
		},
		{
			name:   "Nil system message",
			system: nil,
			want:   "",
		},
		{
			name:   "Other type system message",
			system: 123,
			want:   "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractSystemText(tt.system); got != tt.want {
				t.Errorf("convertSystemText() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConvertUserMessage(t *testing.T) {
	tests := []struct {
		name    string
		message anthropic.Message
		want    []protocol.ChatCompletionMessage
	}{
		{
			name: "String content",
			message: anthropic.Message{
				Role:    "user",
				Content: "Hello, world!",
			},
			want: []protocol.ChatCompletionMessage{
				{
					Role:    "user",
					Content: "Hello, world!",
				},
			},
		},
		{
			name: "Text block content",
			message: anthropic.Message{
				Role: "user",
				Content: []any{
					map[string]any{
						"type": "text",
						"text": "Hello, world!",
					},
				},
			},
			want: []protocol.ChatCompletionMessage{
				{
					Role: "user",
					MultiContent: []protocol.ChatMessagePart{
						protocol.ChatMessagePart{
							Type: "text",
							Text: "Hello, world!",
						},
					},
				},
			},
		},
		{
			name: "Image block content",
			message: anthropic.Message{
				Role: "user",
				Content: []any{
					map[string]any{
						"type": "image",
						"source": map[string]any{
							"type":       "base64",
							"media_type": "image/jpeg",
							"data":       "/9j/4AAQSkZJRgABAQEASABIAAD/2wBDAAgGBgcGBQgHBwcJCQgKDBQNDAsLDBkSEw8UHRofHh0aHBwgJC4nICIsIxwcKDcpLDAxNDQ0Hyc5PTgyPC4zNDL/2wBDAQkJCQwLDBgNDRgyIRwhMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjL/wAARCAABAAEDASIAAhEBAxEB/8QAFQABAQAAAAAAAAAAAAAAAAAAAAX/xAAeEAABAwUBAQEAAAAAAAAAAAABAAIRIQMSUWGRof/EABUBAQEAAAAAAAAAAAAAAAAAAAQF/8QAFxEBAQEBAAAAAAAAAAAAAAAAAAECEf/aAAwDAQACEQMRAD8An6Z8p//Z",
						},
					},
				},
			},
			want: []protocol.ChatCompletionMessage{
				{
					Role: "user",
					MultiContent: []protocol.ChatMessagePart{
						protocol.ChatMessagePart{
							Type: "image",
							ImageURL: &protocol.ChatMessageImageURL{
								URL: "data:image/jpeg;base64,/9j/4AAQSkZJRgABAQEASABIAAD/2wBDAAgGBgcGBQgHBwcJCQgKDBQNDAsLDBkSEw8UHRofHh0aHBwgJC4nICIsIxwcKDcpLDAxNDQ0Hyc5PTgyPC4zNDL/2wBDAQkJCQwLDBgNDRgyIRwhMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjL/wAARCAABAAEDASIAAhEBAxEB/8QAFQABAQAAAAAAAAAAAAAAAAAAAAX/xAAeEAABAwUBAQEAAAAAAAAAAAABAAIRIQMSUWGRof/EABUBAQEAAAAAAAAAAAAAAAAAAAQF/8QAFxEBAQEBAAAAAAAAAAAAAAAAAAECEf/aAAwDAQACEQMRAD8An6Z8p//Z",
							},
						},
					},
				},
			},
		},
		{
			name: "Mixed text and image content",
			message: anthropic.Message{
				Role: "user",
				Content: []any{
					map[string]any{
						"type": "text",
						"text": "This is a beautiful image:",
					},
					map[string]any{
						"type": "image",
						"source": map[string]any{
							"type":       "base64",
							"media_type": "image/png",
							"data":       "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8/5+hHgAHggJ/PchI7wAAAABJRU5ErkJggg==",
						},
					},
				},
			},
			want: []protocol.ChatCompletionMessage{
				{
					Role: "user",
					MultiContent: []protocol.ChatMessagePart{
						protocol.ChatMessagePart{
							Type: "text",
							Text: "This is a beautiful image:",
						},
						protocol.ChatMessagePart{
							Type: "image",
							ImageURL: &protocol.ChatMessageImageURL{
								URL: "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8/5+hHgAHggJ/PchI7wAAAABJRU5ErkJggg==",
							},
						},
					},
				},
			},
		},
		{
			name: "Tool result string content",
			message: anthropic.Message{
				Role: "user",
				Content: []any{
					map[string]any{
						"type":        "tool_result",
						"tool_use_id": "toolu_01A09q901j7xJk456789012345",
						"content":     "259.75 USD",
					},
				},
			},
			want: []protocol.ChatCompletionMessage{
				{
					Role:       "tool",
					ToolCallID: "toolu_01A09q901j7xJk456789012345",
					Content:    "259.75 USD",
				},
			},
		},
		{
			name: "Tool result map content",
			message: anthropic.Message{
				Role: "user",
				Content: []any{
					map[string]any{
						"type":        "tool_result",
						"tool_use_id": "toolu_01A09q901j7xJk456789012345",
						"content": []any{
							map[string]any{
								"type": "text",
								"text": "The weather in San Francisco is 70°F and sunny.",
							},
						},
					},
				},
			},
			want: []protocol.ChatCompletionMessage{
				{
					Role:       "tool",
					ToolCallID: "toolu_01A09q901j7xJk456789012345",
					Content:    "The weather in San Francisco is 70°F and sunny.",
				},
			},
		},
		{
			name: "Tool result list content",
			message: anthropic.Message{
				Role: "user",
				Content: []any{
					map[string]any{
						"type":        "tool_result",
						"tool_use_id": "toolu_01A09q901j7xJk456789012345",
						"content": []any{
							map[string]any{
								"type": "text",
								"text": "The weather in San Francisco is 70°F and sunny.",
							},
						},
					},
					map[string]any{
						"type":        "tool_result",
						"tool_use_id": "toolu_01A09q901j7xJk456789012345",
						"content": []any{
							map[string]any{
								"type": "text",
								"text": "The weather in San Francisco is 70°F and sunny.",
							},
						},
					},
				},
			},
			want: []protocol.ChatCompletionMessage{
				{
					Role:       "tool",
					ToolCallID: "toolu_01A09q901j7xJk456789012345",
					Content:    "The weather in San Francisco is 70°F and sunny.",
				},
				{
					Role:       "tool",
					ToolCallID: "toolu_01A09q901j7xJk456789012345",
					Content:    "The weather in San Francisco is 70°F and sunny.",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := convertUserMessage(&tt.message)
			if !deep.Equal(got, tt.want) {
				gotBytes, _ := json.MarshalIndent(got, "", "  ")
				wantBytes, _ := json.MarshalIndent(tt.want, "", "  ")
				t.Errorf("convertUserMessage() = %s, want %s", gotBytes, wantBytes)
			}
		})
	}
}

func TestConvertAssistantMessage(t *testing.T) {
	tests := []struct {
		name    string
		message anthropic.Message
		want    []protocol.ChatCompletionMessage
	}{
		{
			name: "String content",
			message: anthropic.Message{
				Role:    "assistant",
				Content: "Hello, world!",
			},
			want: []protocol.ChatCompletionMessage{
				{
					Role:    "assistant",
					Content: "Hello, world!",
				},
			},
		},
		{
			name: "Text block content",
			message: anthropic.Message{
				Role: "assistant",
				Content: []any{
					map[string]any{
						"type": "text",
						"text": "Hello, world!",
					},
				},
			},
			want: []protocol.ChatCompletionMessage{
				{
					Role:    "assistant",
					Content: "Hello, world!",
				},
			},
		},
		{
			name: "Multiple text blocks content",
			message: anthropic.Message{
				Role: "assistant",
				Content: []any{
					map[string]any{
						"type": "text",
						"text": "Hello, ",
					},
					map[string]any{
						"type": "text",
						"text": "world!",
					},
				},
			},
			want: []protocol.ChatCompletionMessage{
				{
					Role:    "assistant",
					Content: "Hello, world!",
				},
			},
		},
		{
			name: "Tool use block content",
			message: anthropic.Message{
				Role: "assistant",
				Content: []any{
					map[string]any{
						"type": "tool_use",
						"id":   "toolu_01A09q901j7xJk456789012345",
						"name": "get_weather",
						"input": map[string]any{
							"location": "San Francisco, CA",
							"unit":     "fahrenheit",
						},
					},
				},
			},
			want: []protocol.ChatCompletionMessage{
				{
					Role: "assistant",
					ToolCalls: []protocol.ToolCall{
						{
							ID:   "toolu_01A09q901j7xJk456789012345",
							Type: "function",
							Function: protocol.FunctionCall{
								Name:      "get_weather",
								Arguments: `{"location":"San Francisco, CA","unit":"fahrenheit"}`,
							},
						},
					},
				},
			},
		},
		{
			name: "Tool use with array input",
			message: anthropic.Message{
				Role: "assistant",
				Content: []any{
					map[string]any{
						"type": "tool_use",
						"id":   "toolu_01A09q901j7xJk456789012346",
						"name": "calculate_average",
						"input": map[string]any{
							"numbers": []any{1, 2, 3, 4, 5},
							"round":   true,
						},
					},
				},
			},
			want: []protocol.ChatCompletionMessage{
				{
					Role: "assistant",
					ToolCalls: []protocol.ToolCall{
						{
							ID:   "toolu_01A09q901j7xJk456789012346",
							Type: "function",
							Function: protocol.FunctionCall{
								Name:      "calculate_average",
								Arguments: `{"numbers":[1,2,3,4,5],"round":true}`,
							},
						},
					},
				},
			},
		},
		{
			name: "Tool use with nested array and object input",
			message: anthropic.Message{
				Role: "assistant",
				Content: []any{
					map[string]any{
						"type": "tool_use",
						"id":   "toolu_01A09q901j7xJk456789012347",
						"name": "process_items",
						"input": map[string]any{
							"items": []map[string]any{
								{
									"id":    1,
									"name":  "item1",
									"price": 10.99,
								},
								{
									"id":    2,
									"name":  "item2",
									"price": 20.49,
								},
							},
							"options": map[string]any{
								"sort":    "price",
								"reverse": false,
							},
						},
					},
				},
			},
			want: []protocol.ChatCompletionMessage{
				{
					Role: "assistant",
					ToolCalls: []protocol.ToolCall{
						{
							ID:   "toolu_01A09q901j7xJk456789012347",
							Type: "function",
							Function: protocol.FunctionCall{
								Name:      "process_items",
								Arguments: `{"items":[{"id":1,"name":"item1","price":10.99},{"id":2,"name":"item2","price":20.49}],"options":{"reverse":false,"sort":"price"}}`,
							},
						},
					},
				},
			},
		},
		{
			name: "Mixed text and tool use content",
			message: anthropic.Message{
				Role: "assistant",
				Content: []any{
					map[string]any{
						"type": "text",
						"text": "I'll check the weather for you.",
					},
					map[string]any{
						"type": "tool_use",
						"id":   "toolu_01A09q901j7xJk456789012345",
						"name": "get_weather",
						"input": map[string]any{
							"location": "San Francisco, CA",
							"unit":     "fahrenheit",
						},
					},
				},
			},
			want: []protocol.ChatCompletionMessage{
				{
					Role:    "assistant",
					Content: "I'll check the weather for you.",
					ToolCalls: []protocol.ToolCall{
						{
							ID:   "toolu_01A09q901j7xJk456789012345",
							Type: "function",
							Function: protocol.FunctionCall{
								Name:      "get_weather",
								Arguments: `{"location":"San Francisco, CA","unit":"fahrenheit"}`,
							},
						},
					},
				},
			},
		},
		{
			name: "Multiple content blocks with text and multiple tool uses",
			message: anthropic.Message{
				Role: "assistant",
				Content: []any{
					map[string]any{
						"type": "text",
						"text": "I'll help you check the weather and time.",
					},
					map[string]any{
						"type": "tool_use",
						"id":   "toolu_01A09q901j7xJk456789012345",
						"name": "get_weather",
						"input": map[string]any{
							"location": "San Francisco, CA",
							"unit":     "fahrenheit",
						},
					},
					map[string]any{
						"type": "text",
						"text": "Let me also check the time for you.",
					},
					map[string]any{
						"type": "tool_use",
						"id":   "toolu_01A09q901j7xJk456789012346",
						"name": "get_time",
						"input": map[string]any{
							"timezone": "America/Los_Angeles",
						},
					},
				},
			},
			want: []protocol.ChatCompletionMessage{
				{
					Role:    "assistant",
					Content: "I'll help you check the weather and time.Let me also check the time for you.",
					ToolCalls: []protocol.ToolCall{
						{
							ID:   "toolu_01A09q901j7xJk456789012345",
							Type: "function",
							Function: protocol.FunctionCall{
								Name:      "get_weather",
								Arguments: `{"location":"San Francisco, CA","unit":"fahrenheit"}`,
							},
						},
						{
							ID:   "toolu_01A09q901j7xJk456789012346",
							Type: "function",
							Function: protocol.FunctionCall{
								Name:      "get_time",
								Arguments: `{"timezone":"America/Los_Angeles"}`,
							},
						},
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := convertAssistantMessage(&tt.message)
			if !deep.Equal(got, tt.want) {
				gotBytes, _ := json.MarshalIndent(got, "", "  ")
				wantBytes, _ := json.MarshalIndent(tt.want, "", "  ")
				t.Errorf("convertAssistantMessage() = %s, want %s", gotBytes, wantBytes)
			}
		})
	}
}
