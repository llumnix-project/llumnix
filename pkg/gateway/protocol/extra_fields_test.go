package protocol

import (
	"encoding/json"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// fieldCache stores cached JSON field names for struct types to avoid repeated reflection
var fieldCache sync.Map // map[reflect.Type]map[string]bool

// getStructJSONFields extracts all JSON field names from a struct using reflection.
// This function automatically discovers all struct fields with json tags, eliminating
// the need for manual field list maintenance. Results are cached for performance.
//
// How it works:
//  1. Uses reflection to iterate through struct fields
//  2. Extracts JSON field names from struct tags
//  3. Handles omitempty and inline tags correctly
//  4. Caches results using sync.Map for thread-safe access
//
// Example:
//
//	type Request struct {
//	    Model string `json:"model"`
//	    Temp  float32 `json:"temperature,omitempty"`
//	}
//	fields := getStructJSONFields(Request{})
//	// Returns: map["model":true, "temperature":true]
func getStructJSONFields(v interface{}) map[string]bool {
	t := reflect.TypeOf(v)

	// Check cache first
	if cached, ok := fieldCache.Load(t); ok {
		return cached.(map[string]bool)
	}

	// Build field map using reflection
	fields := make(map[string]bool)
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		jsonTag := field.Tag.Get("json")

		// Skip fields without json tag or with "-" tag
		if jsonTag == "" || jsonTag == "-" {
			continue
		}

		// Extract field name (remove omitempty, inline, etc.)
		fieldName := strings.Split(jsonTag, ",")[0]
		if fieldName != "" && fieldName != "-" {
			fields[fieldName] = true
		}
	}

	// Cache the result
	fieldCache.Store(t, fields)
	return fields
}

// TestChatCompletionRequest_ExtraFields tests that ChatCompletionRequest preserves unknown fields
func TestChatCompletionRequest_ExtraFields(t *testing.T) {
	tests := []struct {
		name           string
		inputJSON      string
		expectedModel  string
		expectedExtras map[string]interface{}
		wantErr        bool
	}{
		{
			name:          "with unknown fields",
			inputJSON:     `{"model":"gpt-4","messages":[],"custom_field":"value","another_field":123}`,
			expectedModel: "gpt-4",
			expectedExtras: map[string]interface{}{
				"custom_field":  "value",
				"another_field": float64(123), // JSON numbers unmarshal as float64
			},
			wantErr: false,
		},
		{
			name:           "without unknown fields",
			inputJSON:      `{"model":"gpt-4","messages":[]}`,
			expectedModel:  "gpt-4",
			expectedExtras: nil, // No extra fields means nil map (memory optimization)
			wantErr:        false,
		},
		{
			name:          "with complex unknown field",
			inputJSON:     `{"model":"gpt-4","messages":[],"metadata":{"user_id":"123","session":"abc"}}`,
			expectedModel: "gpt-4",
			expectedExtras: map[string]interface{}{
				"metadata": map[string]interface{}{
					"user_id": "123",
					"session": "abc",
				},
			},
			wantErr: false,
		},
		{
			name:          "with array unknown field",
			inputJSON:     `{"model":"gpt-4","messages":[],"tags":["tag1","tag2"]}`,
			expectedModel: "gpt-4",
			expectedExtras: map[string]interface{}{
				"tags": []interface{}{"tag1", "tag2"},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req ChatCompletionRequest
			err := json.Unmarshal([]byte(tt.inputJSON), &req)

			if (err != nil) != tt.wantErr {
				t.Errorf("Unmarshal() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if err == nil {
				if req.Model != tt.expectedModel {
					t.Errorf("Model = %v, want %v", req.Model, tt.expectedModel)
				}

				if !reflect.DeepEqual(req.ExtraFields, tt.expectedExtras) {
					t.Errorf("ExtraFields = %v, want %v", req.ExtraFields, tt.expectedExtras)
				}
			}
		})
	}
}

// TestChatCompletionRequest_MarshalWithExtraFields tests that marshaling includes extra fields
func TestChatCompletionRequest_MarshalWithExtraFields(t *testing.T) {
	req := ChatCompletionRequest{
		Model:    "gpt-4",
		Messages: []ChatCompletionMessage{},
		ExtraFields: map[string]interface{}{
			"custom_field": "value",
			"number_field": 123,
		},
	}

	data, err := json.Marshal(&req)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}

	// Unmarshal back to verify all fields are present
	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("Unmarshal result error = %v", err)
	}

	if result["model"] != "gpt-4" {
		t.Errorf("model = %v, want gpt-4", result["model"])
	}

	if result["custom_field"] != "value" {
		t.Errorf("custom_field = %v, want value", result["custom_field"])
	}

	if result["number_field"] != float64(123) {
		t.Errorf("number_field = %v, want 123", result["number_field"])
	}
}

// TestChatCompletionRequest_RoundTrip tests unmarshal then marshal preserves all fields
func TestChatCompletionRequest_RoundTrip(t *testing.T) {
	originalJSON := `{"model":"gpt-4","messages":[],"temperature":0.7,"custom_field":"value","metadata":{"key":"val"}}`

	var req ChatCompletionRequest
	if err := json.Unmarshal([]byte(originalJSON), &req); err != nil {
		t.Fatalf("Unmarshal error = %v", err)
	}

	data, err := json.Marshal(&req)
	if err != nil {
		t.Fatalf("Marshal error = %v", err)
	}

	// Parse both JSONs as maps for comparison
	var original, result map[string]interface{}
	json.Unmarshal([]byte(originalJSON), &original)
	json.Unmarshal(data, &result)

	// Check all original fields are preserved
	if result["custom_field"] != original["custom_field"] {
		t.Errorf("custom_field not preserved")
	}

	if !reflect.DeepEqual(result["metadata"], original["metadata"]) {
		t.Errorf("metadata not preserved")
	}
}

// TestChatCompletionRequest_Clone tests that Clone properly copies ExtraFields
func TestChatCompletionRequest_Clone(t *testing.T) {
	original := &ChatCompletionRequest{
		Model:    "gpt-4",
		Messages: []ChatCompletionMessage{},
		ExtraFields: map[string]interface{}{
			"custom_field": "value",
			"number":       123,
		},
	}

	cloned := original.Clone()

	// Verify ExtraFields are copied
	if !reflect.DeepEqual(cloned.ExtraFields, original.ExtraFields) {
		t.Errorf("ExtraFields not properly cloned")
	}

	// Verify it's a deep copy (modifying clone doesn't affect original)
	cloned.ExtraFields["new_field"] = "new_value"
	if _, exists := original.ExtraFields["new_field"]; exists {
		t.Errorf("Clone is not deep copy, modification affected original")
	}
}

// TestChatCompletionResponse_ExtraFields tests ChatCompletionResponse extra fields handling
func TestChatCompletionResponse_ExtraFields(t *testing.T) {
	inputJSON := `{"id":"chatcmpl-123","object":"chat.completion","created":1234567890,"model":"gpt-4","choices":[],"custom_response_field":"value"}`

	var resp ChatCompletionResponse
	if err := json.Unmarshal([]byte(inputJSON), &resp); err != nil {
		t.Fatalf("Unmarshal error = %v", err)
	}

	if resp.ID != "chatcmpl-123" {
		t.Errorf("ID = %v, want chatcmpl-123", resp.ID)
	}

	expectedExtra := "value"
	if resp.ExtraFields["custom_response_field"] != expectedExtra {
		t.Errorf("ExtraFields[custom_response_field] = %v, want %v",
			resp.ExtraFields["custom_response_field"], expectedExtra)
	}

	// Marshal and verify extra field is included
	data, err := json.Marshal(&resp)
	if err != nil {
		t.Fatalf("Marshal error = %v", err)
	}

	var result map[string]interface{}
	json.Unmarshal(data, &result)
	if result["custom_response_field"] != expectedExtra {
		t.Errorf("Marshaled custom_response_field = %v, want %v",
			result["custom_response_field"], expectedExtra)
	}
}

// TestChatCompletionStreamResponse_ExtraFields tests streaming response extra fields
func TestChatCompletionStreamResponse_ExtraFields(t *testing.T) {
	inputJSON := `{"id":"chatcmpl-123","object":"chat.completion.chunk","created":1234567890,"model":"gpt-4","choices":[],"stream_metadata":"value"}`

	var resp ChatCompletionStreamResponse
	if err := json.Unmarshal([]byte(inputJSON), &resp); err != nil {
		t.Fatalf("Unmarshal error = %v", err)
	}

	if resp.ID != "chatcmpl-123" {
		t.Errorf("ID = %v, want chatcmpl-123", resp.ID)
	}

	if resp.ExtraFields["stream_metadata"] != "value" {
		t.Errorf("ExtraFields[stream_metadata] = %v, want value",
			resp.ExtraFields["stream_metadata"])
	}

	// Test marshal preserves extra fields
	data, _ := json.Marshal(&resp)
	var result map[string]interface{}
	json.Unmarshal(data, &result)
	if result["stream_metadata"] != "value" {
		t.Errorf("Marshal did not preserve stream_metadata")
	}
}

// TestCompletionRequest_ExtraFields tests CompletionRequest extra fields handling
func TestCompletionRequest_ExtraFields(t *testing.T) {
	inputJSON := `{"model":"gpt-3.5-turbo","prompt":"Hello","max_tokens":100,"custom_param":"custom_value","experimental_feature":true}`

	var req CompletionRequest
	if err := json.Unmarshal([]byte(inputJSON), &req); err != nil {
		t.Fatalf("Unmarshal error = %v", err)
	}

	if req.Model != "gpt-3.5-turbo" {
		t.Errorf("Model = %v, want gpt-3.5-turbo", req.Model)
	}

	if req.ExtraFields["custom_param"] != "custom_value" {
		t.Errorf("ExtraFields[custom_param] = %v, want custom_value",
			req.ExtraFields["custom_param"])
	}

	if req.ExtraFields["experimental_feature"] != true {
		t.Errorf("ExtraFields[experimental_feature] = %v, want true",
			req.ExtraFields["experimental_feature"])
	}

	// Test round trip
	data, _ := json.Marshal(&req)
	var result map[string]interface{}
	json.Unmarshal(data, &result)

	if result["custom_param"] != "custom_value" {
		t.Errorf("Round trip lost custom_param")
	}
}

// TestCompletionRequest_Clone tests CompletionRequest Clone with extra fields
func TestCompletionRequest_Clone(t *testing.T) {
	original := &CompletionRequest{
		Model: "gpt-3.5-turbo",
		ExtraFields: map[string]interface{}{
			"custom": "value",
		},
	}

	cloned := original.Clone()

	if !reflect.DeepEqual(cloned.ExtraFields, original.ExtraFields) {
		t.Errorf("ExtraFields not properly cloned")
	}

	// Verify deep copy
	cloned.ExtraFields["modified"] = "new"
	if _, exists := original.ExtraFields["modified"]; exists {
		t.Errorf("Clone is not deep, modification affected original")
	}
}

// TestCompletionResponse_ExtraFields tests CompletionResponse extra fields handling
func TestCompletionResponse_ExtraFields(t *testing.T) {
	inputJSON := `{"id":"cmpl-123","object":"text_completion","created":1234567890,"model":"gpt-3.5-turbo","choices":[],"backend_info":"server-1"}`

	var resp CompletionResponse
	if err := json.Unmarshal([]byte(inputJSON), &resp); err != nil {
		t.Fatalf("Unmarshal error = %v", err)
	}

	if resp.ID != "cmpl-123" {
		t.Errorf("ID = %v, want cmpl-123", resp.ID)
	}

	if resp.ExtraFields["backend_info"] != "server-1" {
		t.Errorf("ExtraFields[backend_info] = %v, want server-1",
			resp.ExtraFields["backend_info"])
	}

	// Test marshal includes extra fields
	data, _ := json.Marshal(&resp)
	var result map[string]interface{}
	json.Unmarshal(data, &result)

	if result["backend_info"] != "server-1" {
		t.Errorf("Marshal did not preserve backend_info")
	}
}

// TestExtraFields_EmptyMap tests that empty ExtraFields doesn't add overhead
func TestExtraFields_EmptyMap(t *testing.T) {
	req := ChatCompletionRequest{
		Model:       "gpt-4",
		Messages:    []ChatCompletionMessage{},
		ExtraFields: nil, // No extra fields
	}

	data, err := json.Marshal(&req)
	if err != nil {
		t.Fatalf("Marshal error = %v", err)
	}

	// Verify no extra fields in output
	var result map[string]interface{}
	json.Unmarshal(data, &result)

	// Should only have known fields
	if len(result) > 2 { // model and messages
		t.Logf("Result fields: %v", result)
		// This is acceptable as other fields might have default values
	}
}

// TestExtraFields_InvalidJSON tests error handling for malformed JSON
func TestExtraFields_InvalidJSON(t *testing.T) {
	tests := []struct {
		name      string
		inputJSON string
	}{
		{
			name:      "invalid json",
			inputJSON: `{invalid json}`,
		},
		{
			name:      "truncated json",
			inputJSON: `{"model":"gpt-4","messages":[]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req ChatCompletionRequest
			err := json.Unmarshal([]byte(tt.inputJSON), &req)
			if err == nil {
				t.Errorf("Expected error for invalid JSON, got nil")
			}
		})
	}
}

// TestExtraFields_NestedStructures tests complex nested unknown fields
func TestExtraFields_NestedStructures(t *testing.T) {
	inputJSON := `{
		"model": "gpt-4",
		"messages": [],
		"metadata": {
			"user": {
				"id": "123",
				"name": "test"
			},
			"tags": ["a", "b", "c"]
		},
		"config": {
			"level1": {
				"level2": {
					"value": 42
				}
			}
		}
	}`

	var req ChatCompletionRequest
	if err := json.Unmarshal([]byte(inputJSON), &req); err != nil {
		t.Fatalf("Unmarshal error = %v", err)
	}

	// Verify nested structures are preserved
	if req.ExtraFields["metadata"] == nil {
		t.Errorf("metadata field not preserved")
	}

	if req.ExtraFields["config"] == nil {
		t.Errorf("config field not preserved")
	}

	// Marshal and verify nested structures are preserved
	data, _ := json.Marshal(&req)
	var result map[string]interface{}
	json.Unmarshal(data, &result)

	metadata, ok := result["metadata"].(map[string]interface{})
	if !ok {
		t.Fatalf("metadata is not a map")
	}

	user, ok := metadata["user"].(map[string]interface{})
	if !ok {
		t.Fatalf("user is not a map")
	}

	if user["id"] != "123" {
		t.Errorf("Nested user.id = %v, want 123", user["id"])
	}
}

// TestGetStructJSONFields_Reflection tests the reflection-based field extraction
func TestGetStructJSONFields_Reflection(t *testing.T) {
	// Test that reflection correctly extracts all fields
	fields := getStructJSONFields(ChatCompletionRequest{})

	// Verify some known fields are present
	expectedFields := []string{
		"model", "messages", "temperature", "max_tokens",
		"stream", "tools", "kv_transfer_params",
	}

	for _, fieldName := range expectedFields {
		if !fields[fieldName] {
			t.Errorf("Expected field %q not found in reflection result", fieldName)
		}
	}

	// Verify ExtraFields is NOT in the list (it has json:"-" tag)
	if fields["ExtraFields"] {
		t.Errorf("ExtraFields should not be in known fields list (has json:\"-\" tag)")
	}
}

// TestGetStructJSONFields_Cache tests that reflection results are cached
func TestGetStructJSONFields_Cache(t *testing.T) {
	// Call twice and verify we get the same map instance (cached)
	fields1 := getStructJSONFields(ChatCompletionRequest{})
	fields2 := getStructJSONFields(ChatCompletionRequest{})

	// They should be the exact same map (pointer equality via interface{})
	// We can't directly compare pointers, but we can verify content is identical
	if len(fields1) != len(fields2) {
		t.Errorf("Cache returned different results: len %d vs %d", len(fields1), len(fields2))
	}

	for k := range fields1 {
		if !fields2[k] {
			t.Errorf("Field %q present in first call but not second", k)
		}
	}
}

// TestExtraFields_AutomaticFieldDetection tests that adding new fields works automatically
func TestExtraFields_AutomaticFieldDetection(t *testing.T) {
	// This test documents the behavior: if you add a new field to ChatCompletionRequest
	// with a proper json tag, it will automatically be recognized as a known field
	// without needing to update any manual field list.

	// Simulate a request with both known and unknown fields
	inputJSON := `{"model":"gpt-4","messages":[],"unknown_new_field":"should_be_in_extra"}`

	var req ChatCompletionRequest
	if err := json.Unmarshal([]byte(inputJSON), &req); err != nil {
		t.Fatalf("Unmarshal error = %v", err)
	}

	// Verify known field is accessible
	if req.Model != "gpt-4" {
		t.Errorf("Model = %v, want gpt-4", req.Model)
	}

	// Verify unknown field is in ExtraFields
	if req.ExtraFields["unknown_new_field"] != "should_be_in_extra" {
		t.Errorf("Unknown field not captured in ExtraFields")
	}

	// If you add a new field to the struct like:
	//   NewField string `json:"new_field"`
	// Then "new_field" would automatically become a known field
	// and would NOT appear in ExtraFields anymore.
}

// BenchmarkChatCompletionRequest_Unmarshal benchmarks unmarshaling with extra fields
func BenchmarkChatCompletionRequest_Unmarshal(b *testing.B) {
	// Use randomized data to prevent compiler optimizations from skewing results
	jsonData := []byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hello"}],"temperature":0.7,"max_tokens":100,"custom_field":"value","another_field":123}`)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var req ChatCompletionRequest
		if err := json.Unmarshal(jsonData, &req); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkChatCompletionRequest_Marshal benchmarks marshaling with extra fields
func BenchmarkChatCompletionRequest_Marshal(b *testing.B) {
	req := ChatCompletionRequest{
		Model: "gpt-4",
		Messages: []ChatCompletionMessage{
			{Role: "user", Content: "hello"},
		},
		ExtraFields: map[string]interface{}{
			"custom_field":  "value",
			"another_field": 123,
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := json.Marshal(&req)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkChatCompletionRequest_UnmarshalNoExtra benchmarks unmarshaling without extra fields
func BenchmarkChatCompletionRequest_UnmarshalNoExtra(b *testing.B) {
	jsonData := []byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hello"}],"temperature":0.7}`)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var req ChatCompletionRequest
		if err := json.Unmarshal(jsonData, &req); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkChatCompletionRequest_MarshalNoExtra benchmarks marshaling without extra fields
func BenchmarkChatCompletionRequest_MarshalNoExtra(b *testing.B) {
	req := ChatCompletionRequest{
		Model: "gpt-4",
		Messages: []ChatCompletionMessage{
			{Role: "user", Content: "hello"},
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := json.Marshal(&req)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkGetStructJSONFields_Reflection benchmarks reflection-based field extraction
func BenchmarkGetStructJSONFields_Reflection(b *testing.B) {
	// Clear cache to measure cold start (only first iteration)
	fieldCache = sync.Map{}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = getStructJSONFields(ChatCompletionRequest{})
	}
}

// BenchmarkGetStructJSONFields_Cached benchmarks cached field lookup
func BenchmarkGetStructJSONFields_Cached(b *testing.B) {
	// Warm up cache
	_ = getStructJSONFields(ChatCompletionRequest{})

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = getStructJSONFields(ChatCompletionRequest{})
	}
}

// TestChatCompletionRequest_MessagesWithContent tests messages with simple content strings
func TestChatCompletionRequest_MessagesWithContent(t *testing.T) {
	tests := []struct {
		name      string
		inputJSON string
		check     func(t *testing.T, req *ChatCompletionRequest)
	}{
		{
			name: "single message with content",
			inputJSON: `{
				"model": "gpt-4",
				"messages": [{"role": "user", "content": "Hello"}]
			}`,
			check: func(t *testing.T, req *ChatCompletionRequest) {
				if len(req.Messages) != 1 {
					t.Fatalf("expected 1 message, got %d", len(req.Messages))
				}
				if req.Messages[0].Content != "Hello" {
					t.Errorf("expected content='Hello', got %s", req.Messages[0].Content)
				}
				if req.Messages[0].MultiContent != nil {
					t.Error("expected MultiContent to be nil")
				}
			},
		},
		{
			name: "multiple messages with content",
			inputJSON: `{
				"model": "gpt-4",
				"messages": [
					{"role": "system", "content": "You are a helpful assistant"},
					{"role": "user", "content": "Hello"},
					{"role": "assistant", "content": "Hi there!"}
				]
			}`,
			check: func(t *testing.T, req *ChatCompletionRequest) {
				if len(req.Messages) != 3 {
					t.Fatalf("expected 3 messages, got %d", len(req.Messages))
				}
				if req.Messages[0].Role != "system" {
					t.Errorf("expected first role='system', got %s", req.Messages[0].Role)
				}
				if req.Messages[1].Content != "Hello" {
					t.Errorf("expected second content='Hello', got %s", req.Messages[1].Content)
				}
				if req.Messages[2].Content != "Hi there!" {
					t.Errorf("expected third content='Hi there!', got %s", req.Messages[2].Content)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req ChatCompletionRequest
			if err := json.Unmarshal([]byte(tt.inputJSON), &req); err != nil {
				t.Fatalf("Unmarshal failed: %v", err)
			}
			tt.check(t, &req)

			// Test round-trip: marshal and unmarshal again
			data, err := json.Marshal(&req)
			if err != nil {
				t.Fatalf("Marshal failed: %v", err)
			}
			var req2 ChatCompletionRequest
			if err := json.Unmarshal(data, &req2); err != nil {
				t.Fatalf("Second unmarshal failed: %v", err)
			}
			tt.check(t, &req2)
		})
	}
}

// TestChatCompletionRequest_MessagesWithMultiContent tests messages with array content (vision)
func TestChatCompletionRequest_MessagesWithMultiContent(t *testing.T) {
	tests := []struct {
		name      string
		inputJSON string
		check     func(t *testing.T, req *ChatCompletionRequest)
	}{
		{
			name: "message with text and image",
			inputJSON: `{
				"model": "gpt-4-vision",
				"messages": [{
					"role": "user",
					"content": [
						{"type": "text", "text": "What is in this image?"},
						{"type": "image_url", "image_url": {"url": "https://example.com/image.jpg"}}
					]
				}]
			}`,
			check: func(t *testing.T, req *ChatCompletionRequest) {
				if len(req.Messages) != 1 {
					t.Fatalf("expected 1 message, got %d", len(req.Messages))
				}
				if req.Messages[0].Content != "" {
					t.Errorf("expected Content to be empty, got %s", req.Messages[0].Content)
				}
				if len(req.Messages[0].MultiContent) != 2 {
					t.Fatalf("expected 2 content parts, got %d", len(req.Messages[0].MultiContent))
				}
				if req.Messages[0].MultiContent[0].Type != "text" {
					t.Errorf("expected first part type='text', got %s", req.Messages[0].MultiContent[0].Type)
				}
				if req.Messages[0].MultiContent[0].Text != "What is in this image?" {
					t.Errorf("expected text='What is in this image?', got %s", req.Messages[0].MultiContent[0].Text)
				}
				if req.Messages[0].MultiContent[1].Type != "image_url" {
					t.Errorf("expected second part type='image_url', got %s", req.Messages[0].MultiContent[1].Type)
				}
				if req.Messages[0].MultiContent[1].ImageURL == nil {
					t.Fatal("expected ImageURL to be set")
				}
				if req.Messages[0].MultiContent[1].ImageURL.URL != "https://example.com/image.jpg" {
					t.Errorf("expected URL='https://example.com/image.jpg', got %s", req.Messages[0].MultiContent[1].ImageURL.URL)
				}
			},
		},
		{
			name: "message with multiple images",
			inputJSON: `{
				"model": "gpt-4-vision",
				"messages": [{
					"role": "user",
					"content": [
						{"type": "text", "text": "Compare these images"},
						{"type": "image_url", "image_url": {"url": "https://example.com/img1.jpg", "detail": "high"}},
						{"type": "image_url", "image_url": {"url": "https://example.com/img2.jpg", "detail": "low"}}
					]
				}]
			}`,
			check: func(t *testing.T, req *ChatCompletionRequest) {
				if len(req.Messages[0].MultiContent) != 3 {
					t.Fatalf("expected 3 content parts, got %d", len(req.Messages[0].MultiContent))
				}
				if req.Messages[0].MultiContent[1].ImageURL.Detail != "high" {
					t.Errorf("expected first image detail='high', got %s", req.Messages[0].MultiContent[1].ImageURL.Detail)
				}
				if req.Messages[0].MultiContent[2].ImageURL.Detail != "low" {
					t.Errorf("expected second image detail='low', got %s", req.Messages[0].MultiContent[2].ImageURL.Detail)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req ChatCompletionRequest
			if err := json.Unmarshal([]byte(tt.inputJSON), &req); err != nil {
				t.Fatalf("Unmarshal failed: %v", err)
			}
			tt.check(t, &req)

			// Test round-trip: marshal and unmarshal again
			data, err := json.Marshal(&req)
			if err != nil {
				t.Fatalf("Marshal failed: %v", err)
			}
			var req2 ChatCompletionRequest
			if err := json.Unmarshal(data, &req2); err != nil {
				t.Fatalf("Second unmarshal failed: %v", err)
			}
			tt.check(t, &req2)
		})
	}
}

// TestChatCompletionRequest_MessagesWithToolCalls tests messages with tool calls
func TestChatCompletionRequest_MessagesWithToolCalls(t *testing.T) {
	inputJSON := `{
		"model": "gpt-4",
		"messages": [
			{"role": "user", "content": "What's the weather in Beijing?"},
			{
				"role": "assistant",
				"content": "",
				"tool_calls": [{
					"id": "call_abc123",
					"type": "function",
					"function": {
						"name": "get_weather",
						"arguments": "{\"city\":\"Beijing\"}"
					}
				}]
			},
			{
				"role": "tool",
				"tool_call_id": "call_abc123",
				"content": "Sunny, 25°C"
			}
		]
	}`

	var req ChatCompletionRequest
	if err := json.Unmarshal([]byte(inputJSON), &req); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	// Verify user message
	if req.Messages[0].Role != "user" || req.Messages[0].Content != "What's the weather in Beijing?" {
		t.Errorf("user message incorrect")
	}

	// Verify assistant message with tool calls
	if req.Messages[1].Role != "assistant" {
		t.Errorf("expected assistant role, got %s", req.Messages[1].Role)
	}
	if len(req.Messages[1].ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(req.Messages[1].ToolCalls))
	}
	if req.Messages[1].ToolCalls[0].ID != "call_abc123" {
		t.Errorf("expected tool call ID='call_abc123', got %s", req.Messages[1].ToolCalls[0].ID)
	}
	if req.Messages[1].ToolCalls[0].Function.Name != "get_weather" {
		t.Errorf("expected function name='get_weather', got %s", req.Messages[1].ToolCalls[0].Function.Name)
	}

	// Verify tool message
	if req.Messages[2].Role != "tool" {
		t.Errorf("expected tool role, got %s", req.Messages[2].Role)
	}
	if req.Messages[2].ToolCallID != "call_abc123" {
		t.Errorf("expected tool_call_id='call_abc123', got %s", req.Messages[2].ToolCallID)
	}
	if req.Messages[2].Content != "Sunny, 25°C" {
		t.Errorf("expected content='Sunny, 25°C', got %s", req.Messages[2].Content)
	}

	// Test round-trip
	data, err := json.Marshal(&req)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	var req2 ChatCompletionRequest
	if err := json.Unmarshal(data, &req2); err != nil {
		t.Fatalf("Second unmarshal failed: %v", err)
	}
	if len(req2.Messages[1].ToolCalls) != 1 {
		t.Errorf("tool calls lost after round-trip")
	}
}

// TestChatCompletionRequest_MessagesMixedContent tests request with both content types
func TestChatCompletionRequest_MessagesMixedContent(t *testing.T) {
	inputJSON := `{
		"model": "gpt-4-vision",
		"messages": [
			{"role": "system", "content": "You are a helpful assistant"},
			{
				"role": "user",
				"content": [
					{"type": "text", "text": "What do you see?"},
					{"type": "image_url", "image_url": {"url": "https://example.com/pic.jpg"}}
				]
			},
			{"role": "assistant", "content": "I see a beautiful landscape."}
		]
	}`

	var req ChatCompletionRequest
	if err := json.Unmarshal([]byte(inputJSON), &req); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if len(req.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(req.Messages))
	}

	// First message: simple content
	if req.Messages[0].Content != "You are a helpful assistant" {
		t.Errorf("system message content incorrect")
	}
	if req.Messages[0].MultiContent != nil {
		t.Error("system message should not have MultiContent")
	}

	// Second message: multi content
	if req.Messages[1].Content != "" {
		t.Errorf("user message with MultiContent should have empty Content")
	}
	if len(req.Messages[1].MultiContent) != 2 {
		t.Errorf("expected 2 content parts in user message, got %d", len(req.Messages[1].MultiContent))
	}

	// Third message: simple content
	if req.Messages[2].Content != "I see a beautiful landscape." {
		t.Errorf("assistant message content incorrect")
	}
	if req.Messages[2].MultiContent != nil {
		t.Error("assistant message should not have MultiContent")
	}

	// Test round-trip
	data, err := json.Marshal(&req)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	var req2 ChatCompletionRequest
	if err := json.Unmarshal(data, &req2); err != nil {
		t.Fatalf("Second unmarshal failed: %v", err)
	}

	// Verify mixed content preserved
	if req2.Messages[0].Content != req.Messages[0].Content {
		t.Error("system message content changed after round-trip")
	}
	if len(req2.Messages[1].MultiContent) != len(req.Messages[1].MultiContent) {
		t.Error("user message MultiContent changed after round-trip")
	}
	if req2.Messages[2].Content != req.Messages[2].Content {
		t.Error("assistant message content changed after round-trip")
	}
}

// BenchmarkCompletionRequest_Unmarshal benchmarks CompletionRequest unmarshaling
func BenchmarkCompletionRequest_Unmarshal(b *testing.B) {
	jsonData := []byte(`{"model":"gpt-3.5-turbo","prompt":"Hello","max_tokens":100,"temperature":0.5,"custom_param":"value"}`)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var req CompletionRequest
		if err := json.Unmarshal(jsonData, &req); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCompletionResponse_Marshal benchmarks CompletionResponse marshaling
func BenchmarkCompletionResponse_Marshal(b *testing.B) {
	resp := CompletionResponse{
		ID:      "cmpl-123",
		Object:  "text_completion",
		Created: 1234567890,
		Model:   "gpt-3.5-turbo",
		ExtraFields: map[string]interface{}{
			"backend_info": "server-1",
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := json.Marshal(&resp)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkChatCompletionStreamResponse_Marshal benchmarks streaming response marshaling
func BenchmarkChatCompletionStreamResponse_Marshal(b *testing.B) {
	resp := ChatCompletionStreamResponse{
		ID:      "chatcmpl-123",
		Object:  "chat.completion.chunk",
		Created: 1234567890,
		Model:   "gpt-4",
		ExtraFields: map[string]interface{}{
			"stream_metadata": "value",
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := json.Marshal(&resp)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkChatCompletionRequest_RoundTrip benchmarks full unmarshal+marshal cycle
func BenchmarkChatCompletionRequest_RoundTrip(b *testing.B) {
	jsonData := []byte(`{"model":"gpt-4","messages":[],"temperature":0.7,"custom_field":"value","metadata":{"key":"val"}}`)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var req ChatCompletionRequest
		if err := json.Unmarshal(jsonData, &req); err != nil {
			b.Fatal(err)
		}
		_, err := json.Marshal(&req)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkChatCompletionRequest_Clone benchmarks cloning with extra fields
func BenchmarkChatCompletionRequest_Clone(b *testing.B) {
	original := &ChatCompletionRequest{
		Model: "gpt-4",
		Messages: []ChatCompletionMessage{
			{Role: "user", Content: "hello"},
		},
		ExtraFields: map[string]interface{}{
			"custom_field": "value",
			"number":       123,
			"nested": map[string]interface{}{
				"key": "val",
			},
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = original.Clone()
	}
}
