package tool_parser

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestDsV31ParseComplete_SingleToolCall(t *testing.T) {
	parser := NewDeepSeekV31Parser()

	// Test case with single tool call using the provided format
	input := `r#"<｜tool▁calls▁begin｜><｜tool▁calls▁begin｜><｜tool▁call▁begin｜>search<｜tool▁sep｜>{"query": "rust programming"}<｜tool▁call▁end｜><｜tool▁calls▁end｜>`

	result, err := parser.ParseComplete(input)
	if err != nil {
		t.Fatalf("ParseComplete failed: %v", err)
	}

	fmt.Printf("result: %v", result)

	// Check normal text
	expectedNormalText := `r#"`
	if result.NormalText != expectedNormalText {
		t.Errorf("Expected normal text %q, got %q", expectedNormalText, result.NormalText)
	}

	// Check tool calls
	if len(result.Calls) != 1 {
		t.Fatalf("Expected 1 tool call, got %d", len(result.Calls))
	}

	toolCall := result.Calls[0]
	if toolCall.Function.Name != "search" {
		t.Errorf("Expected function name 'search', got %q", toolCall.Function.Name)
	}

	// Check arguments
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &args); err != nil {
		t.Fatalf("Failed to unmarshal arguments: %v", err)
	}

	if args["query"] != "rust programming" {
		t.Errorf("Expected query 'rust programming', got %q", args["query"])
	}
}

func TestDsV31ParseComplete_MultipleToolCalls(t *testing.T) {
	parser := NewDeepSeekV31Parser()

	// Test case with multiple tool calls using the provided format
	input := `r#"<｜tool▁calls▁begin｜><｜tool▁calls▁begin｜><｜tool▁call▁begin｜>search<｜tool▁sep｜>{"query": "rust programming"}<｜tool▁call▁end｜><｜tool▁call▁begin｜>translate<｜tool▁sep｜>{"text": "Hello World", "to": "ja"}<｜tool▁call▁end｜><｜tool▁calls▁end｜>`

	result, err := parser.ParseComplete(input)
	if err != nil {
		t.Fatalf("ParseComplete failed: %v", err)
	}

	// Check normal text
	expectedNormalText := `r#"`
	if result.NormalText != expectedNormalText {
		t.Errorf("Expected normal text %q, got %q", expectedNormalText, result.NormalText)
	}

	// Check tool calls
	if len(result.Calls) != 2 {
		t.Fatalf("Expected 2 tool calls, got %d", len(result.Calls))
	}

	// Check first tool call
	searchCall := result.Calls[0]
	if searchCall.Function.Name != "search" {
		t.Errorf("Expected function name 'search', got %q", searchCall.Function.Name)
	}
	var searchArgs map[string]interface{}
	if err := json.Unmarshal([]byte(searchCall.Function.Arguments), &searchArgs); err != nil {
		t.Fatalf("Failed to unmarshal search arguments: %v", err)
	}
	if searchArgs["query"] != "rust programming" {
		t.Errorf("Expected query 'rust programming', got %q", searchArgs["query"])
	}

	// Check second tool call
	translateCall := result.Calls[1]
	if translateCall.Function.Name != "translate" {
		t.Errorf("Expected function name 'translate', got %q", translateCall.Function.Name)
	}
	var translateArgs map[string]interface{}
	if err := json.Unmarshal([]byte(translateCall.Function.Arguments), &translateArgs); err != nil {
		t.Fatalf("Failed to unmarshal translate arguments: %v", err)
	}
	if translateArgs["text"] != "Hello World" {
		t.Errorf("Expected text 'Hello World', got %q", translateArgs["text"])
	}
	if translateArgs["to"] != "ja" {
		t.Errorf("Expected language 'ja', got %q", translateArgs["to"])
	}
}

func TestDsV31ParseComplete_NoToolCalls(t *testing.T) {
	parser := NewDeepSeekV31Parser()

	// Test case with no tool calls
	input := "This is a normal text without any tool calls."

	result, err := parser.ParseComplete(input)
	if err != nil {
		t.Fatalf("ParseComplete failed: %v", err)
	}

	if result.NormalText != input {
		t.Errorf("Expected normal text %q, got %q", input, result.NormalText)
	}

	if len(result.Calls) != 0 {
		t.Errorf("Expected 0 tool calls, got %d", len(result.Calls))
	}
}

func TestDsV31ParseComplete_InvalidJSON(t *testing.T) {
	parser := NewDeepSeekV31Parser()

	// Test case with invalid JSON in tool call
	input := `r#"<｜tool▁calls▁begin｜><｜tool▁calls▁begin｜><｜tool▁call▁begin｜>search<｜tool▁sep｜>{"query": "rust programming", invalid json}<｜tool▁call▁end｜><｜tool▁calls▁end｜>`

	result, err := parser.ParseComplete(input)
	if err != nil {
		t.Fatalf("ParseComplete failed: %v", err)
	}

	// Should return normal text and no tool calls due to invalid JSON
	if result.NormalText != `r#"` {
		t.Errorf("Expected normal text %q, got %q", `r#"`, result.NormalText)
	}

	if len(result.Calls) != 0 {
		t.Errorf("Expected 0 tool calls due to invalid JSON, got %d", len(result.Calls))
	}
}

func TestDsV31ParseStreaming_CompleteToolCall(t *testing.T) {
	parser := NewDeepSeekV31Parser()

	cases := []struct {
		chunk      string
		normalText string
		funcName   string
		argsValue  string
	}{
		{`r#"<｜tool▁calls▁begin｜>`, `r#"`, "", ""},
		{`<｜tool▁call▁begin｜>`, "", "", ""},
		{`search`, "", "", ""},
		{`<｜tool▁sep｜>`, "", "search", ""},
		{"{", "", "", "{"},
		{`"query`, "", "", `"query`},
		{`": "`, "", "", `": "`},
		{`rust `, "", "", `rust `},
		{`programming`, "", "", `programming`},
		{`"`, "", "", `"`},
		{`}`, "", "", `}`},
		{"<｜tool▁call▁end｜>", "", "", ""},
		{"\n ", "", "", ""},
		{" ", "", "", ""},
		{`<｜tool▁call▁begin｜>`, "", "", ""},
		{"sea", "", "", ""},
		{"rch", "", "", ""},
		{`<｜tool▁sep｜>`, "", "search", ""},
		{"{", "", "", "{"},
		{`"query`, "", "", `"query`},
		{`": "`, "", "", `": "`},
		{`rust `, "", "", `rust `},
		{`programming`, "", "", `programming`},
		{`"`, "", "", `"`},
		{`}`, "", "", `}`},
		{"<｜tool▁call▁end｜>", "", "", ""},
		{"<｜tool▁calls▁end｜>", "", "", ""},
	}

	// Simulate streaming input with complete tool call
	var result *ParseResult
	var err error

	i := 0
	for _, c := range cases {
		fmt.Printf("test case %d, input: ##%s##, result: ##%s##, ##%s##, ##%s##\n", i, c.chunk, c.normalText, c.funcName, c.argsValue)
		result, err = parser.ParseStreaming(c.chunk)
		if err != nil {
			t.Fatalf("[%d]ParseStreaming failed on chunk %q: %v", i, c.chunk, err)
		}

		if result.NormalText != c.normalText {
			t.Fatalf("[%d]Expected normal text %q, got %q", i, c.normalText, result.NormalText)
		}

		if c.funcName != "" || c.argsValue != "" {
			if len(result.Calls) == 0 {
				t.Fatalf("[%d]Expected no calls but got %d, %v", i, len(result.Calls), result.Calls)
			}
		}

		if c.funcName != "" {
			if result.Calls[0].Function.Name != c.funcName {
				t.Fatalf("[%d]Expected function name '%s', got %q", i, c.funcName, result.Calls[0].Function.Name)
			}
		}

		if c.argsValue != "" {
			if result.Calls[0].Function.Arguments != c.argsValue {
				t.Fatalf("[%d]Expected args name '%s', got %q", i, c.argsValue, result.Calls[0].Function.Arguments)
			}
		}
		i++
	}
}

func TestDsV31ParseStreaming_NoFunctionNameOrFunctionTypeToolCall(t *testing.T) {
	parser := NewDeepSeekV31Parser()

	cases := []struct {
		chunk      string
		normalText string
		funcName   string
		argsValue  string
	}{
		{`r#"<｜tool▁calls▁begin｜>`, `r#"`, "", ""},
		{`<｜tool▁call▁begin｜>`, "", "", ""},
		{`<｜tool▁sep｜>`, "", "", ""},
		{"{", "", "", ""},
		{`"query`, "", "", ``},
		{`": "`, "", "", ``},
		{`rust `, "", "", ``},
		{`programming`, "", "", ``},
		{`"`, "", "", ``},
		{`}`, "", "", ``},
		{"<｜tool▁call▁end｜>", "", "", ""},
		{"\n ", "", "", ""},
		{" ", "", "", ""},
		{`<｜tool▁call▁begin｜>`, "", "", ""},
		{"sea", "", "", ""},
		{"rch", "", "", ""},
		{`<｜tool▁sep｜>`, "", "search", ""},
		{"{", "", "", "{"},
		{`"query`, "", "", `"query`},
		{`": "`, "", "", `": "`},
		{`rust `, "", "", `rust `},
		{`programming`, "", "", `programming`},
		{`"`, "", "", `"`},
		{`}`, "", "", `}`},
		{"\n", "", "", ""},
		{"```", "", "", ""},
		{"<｜tool▁call▁end｜>", "", "", ""},
		{"<｜tool▁calls▁end｜>", "", "", ""},
	}

	// Simulate streaming input with complete tool call
	var result *ParseResult
	var err error

	i := 0
	for _, c := range cases {
		fmt.Printf("test case %d, input: ##%s##, result: ##%s##, ##%s##, ##%s##\n", i, c.chunk, c.normalText, c.funcName, c.argsValue)
		result, err = parser.ParseStreaming(c.chunk)
		if err != nil {
			t.Fatalf("[%d]ParseStreaming failed on chunk %q: %v", i, c.chunk, err)
		}

		if result.NormalText != c.normalText {
			t.Fatalf("[%d]Expected normal text %q, got %q", i, c.normalText, result.NormalText)
		}

		if c.funcName != "" || c.argsValue != "" {
			if len(result.Calls) == 0 {
				t.Fatalf("[%d]Expected no calls but got %d, %v", i, len(result.Calls), result.Calls)
			}
		}

		if c.funcName != "" {
			if result.Calls[0].Function.Name != c.funcName {
				t.Fatalf("[%d]Expected function name '%s', got %q", i, c.funcName, result.Calls[0].Function.Name)
			}
		}

		if c.argsValue != "" {
			if result.Calls[0].Function.Arguments != c.argsValue {
				t.Fatalf("[%d]Expected args name '%s', got %q", i, c.argsValue, result.Calls[0].Function.Arguments)
			}
		}
		i++
	}
}

func TestDsV31ParseStreaming_NoToolCalls(t *testing.T) {
	parser := NewDeepSeekV31Parser()

	// Test case with no tool calls in streaming
	input := "This is a normal text without any tool calls."

	result, err := parser.ParseStreaming(input)
	if err != nil {
		t.Fatalf("ParseStreaming failed: %v", err)
	}

	if result.NormalText != input {
		t.Errorf("Expected normal text %q, got %q", input, result.NormalText)
	}

	if len(result.Calls) != 0 {
		t.Errorf("Expected 0 tool calls, got %d", len(result.Calls))
	}
}

func TestDsV31ParseStreaming_ResetState(t *testing.T) {
	parser := NewDeepSeekV31Parser()

	// Test that parser state is reset between different inputs
	firstInput := `r#"<｜tool▁calls▁begin｜>`
	_, err := parser.ParseStreaming(firstInput)
	if err != nil {
		t.Fatalf("ParseStreaming failed: %v", err)
	}

	// Create a new parser for second input to ensure clean state
	parser2 := NewDeepSeekV31Parser()
	secondInput := "Normal text without tool calls"
	result2, err := parser2.ParseStreaming(secondInput)
	if err != nil {
		t.Fatalf("ParseStreaming failed: %v", err)
	}

	if result2.NormalText != secondInput {
		t.Errorf("Expected normal text %q, got %q", secondInput, result2.NormalText)
	}

	if len(result2.Calls) != 0 {
		t.Errorf("Expected 0 tool calls, got %d", len(result2.Calls))
	}
}
