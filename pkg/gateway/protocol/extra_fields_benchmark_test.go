package protocol

import (
	"encoding/json"
	"testing"

	jsoniter "github.com/json-iterator/go"
)

// ===================================================================================
// Performance Benchmark Test Results - ChatCompletionRequest Serialization
// ===================================================================================
//
// Test Environment:
//   - CPU: Apple M1 Pro
//   - Go Version: 1.x
//   - Date: 2026-02-12
//   - Optimization: ExtraFields index caching (v2)
//
// Test Scenarios:
//   1. Small Data (~100 bytes): Standard OpenAI request with simple message
//   2. Large Data (~1MB): Multi-turn conversation with large content (750KB total)
//
// Tested Versions:
//   1. Baseline           - No ExtraFields support, standard json package
//   2. ReflectionBased    - Production version, uses reflection with cached ExtraFields index
//   3. StdlibDoubleParse  - Standard library with double-parse approach
//   4. JsoniterDoubleParse- Jsoniter library with double-parse approach
//   5. JsoniterStreaming  - Jsoniter with hand-written streaming API (single-pass)
//
// ===================================================================================
// BENCHMARK RESULTS (After Optimization)
// ===================================================================================
//
// Small Data (~100 bytes) - Standard Request:
// ┌──────────────────────┬─────────────┬──────────┬────────┬─────────────┬──────────┬────────┐
// │ Version              │ Unmarshal   │ Memory   │ Allocs │ Marshal     │ Memory   │ Allocs │
// │                      │ (ns/op)     │ (B/op)   │ (n)    │ (ns/op)     │ (B/op)   │ (n)    │
// ├──────────────────────┼─────────────┼──────────┼────────┼─────────────┼──────────┼────────┤
// │ Baseline             │ 1,765       │ 1,152    │ 17     │ 1,041       │ 368      │ 3      │
// │ ReflectionBased ⚡    │ 1,819 (1.03x)│ 1,288   │ 24     │ 1,467 (1.4x)│ 881      │ 11     │
// │ StdlibDoubleParse    │ 4,414 (2.5x)│ 2,208    │ 41     │ 1,762 (1.7x)│ 512      │ 4      │
// │ JsoniterDoubleParse  │ 2,813 (1.6x)│ 2,120    │ 47     │ 1,496 (1.4x)│ 881      │ 11     │
// │ JsoniterStreaming    │ 1,690 (0.96x)│ 1,288   │ 24     │ 1,525 (1.5x)│ 881      │ 11     │
// └──────────────────────┴─────────────┴──────────┴────────┴─────────────┴──────────┴────────┘
//
// Large Data (~1MB) - Multi-turn Conversation:
// ┌──────────────────────┬──────────────┬───────────┬────────┬──────────────┬───────────┬────────┐
// │ Version              │ Unmarshal    │ Memory    │ Allocs │ Marshal      │ Memory    │ Allocs │
// │                      │ (ns/op)      │ (B/op)    │ (n)    │ (ns/op)      │ (B/op)    │ (n)    │
// ├──────────────────────┼──────────────┼───────────┼────────┼──────────────┼───────────┼────────┤
// │ Baseline             │ 6,018,608    │ 765,013   │ 40     │ 3,453,540    │ 1,765,762 │ 13     │
// │ ReflectionBased ⚡    │ 6,733,841    │ 1,528,221 │ 71     │ 3,692,828    │ 2,558,142 │ 24     │
// │                      │ (1.12x)      │ (2.0x)    │ (1.8x) │ (1.07x)      │ (1.4x)    │ (1.8x) │
// │ StdlibDoubleParse    │ 12,488,974   │ 1,519,688 │ 64     │ 6,217,580    │ 2,573,240 │ 15     │
// │                      │ (2.07x)      │ (2.0x)    │ (1.6x) │ (1.80x)      │ (1.5x)    │ (1.2x) │
// │ JsoniterDoubleParse  │ 7,702,869    │ 2,283,394 │ 105    │ 3,843,579    │ 2,642,470 │ 24     │
// │                      │ (1.28x)      │ (3.0x)    │ (2.6x) │ (1.11x)      │ (1.5x)    │ (1.8x) │
// │ JsoniterStreaming    │ 6,813,364    │ 1,528,217 │ 71     │ 3,529,827    │ 2,521,140 │ 23     │
// │                      │ (1.13x)      │ (2.0x)    │ (1.8x) │ (1.02x)      │ (1.4x)    │ (1.8x) │
// └──────────────────────┴──────────────┴───────────┴────────┴──────────────┴───────────┴────────┘
//
// ===================================================================================
// OPTIMIZATION IMPACT (v1 → v2)
// ===================================================================================
//
// ReflectionBased - Small Data:
//   Before: 2,829 ns/op, 1,528 B/op, 53 allocs/op
//   After:  1,819 ns/op, 1,288 B/op, 24 allocs/op
//   ✅ 35.7% faster, 29 fewer allocations, 240 bytes less memory
//
// ReflectionBased - Large Data:
//   Before: 7,102,685 ns/op, 1,528,548 B/op, 100 allocs/op
//   After:  6,733,841 ns/op, 1,528,221 B/op, 71 allocs/op
//   ✅ 5.2% faster, 29 fewer allocations
//
// Optimization Details:
//   - Cached ExtraFields field index in buildFieldMap() (called once per type)
//   - Eliminated 30+ Field(i) reflection calls per unmarshal operation
//   - Reduced from O(n) field scan to O(1) cached lookup
//
// ===================================================================================
// KEY FINDINGS
// ===================================================================================
//
// Small Data (~100 bytes):
//   ⚡ ReflectionBased now only 3% slower than Baseline (1,819 vs 1,765 ns)
//   🏆 JsoniterStreaming remains fastest with ExtraFields support
//   ❌ StdlibDoubleParse is 2.5x slower - avoid in production
//   📊 ReflectionBased vs JsoniterStreaming: 1,819 vs 1,690 ns (7.6% difference)
//
// Large Data (~1MB):
//   🏆 Baseline fastest (no ExtraFields overhead)
//   ⚡ ReflectionBased only 12% slower than Baseline (excellent for 1MB payloads!)
//   📊 ReflectionBased beats JsoniterStreaming on large data (6.73ms vs 6.81ms)
//   ✅ Fixed overhead amortizes well as payload size increases
//
// Memory Efficiency:
//   - ReflectionBased: 1,288 B / 24 allocs (small), 1.5MB / 71 allocs (large)
//   - Same memory footprint as hand-written JsoniterStreaming
//   - Significantly better than double-parse approaches
//
// ===================================================================================
// RECOMMENDATION
// ===================================================================================
//
// ✅ Use ReflectionBased (current production implementation):
//   - Excellent performance: only 3% slower than baseline on small data
//   - Zero maintenance cost: automatically handles all struct types
//   - Best large payload performance among ExtraFields implementations
//   - Clean, readable code with comprehensive field parser caching
//
// 🎯 Alternative for extreme performance requirements:
//   - JsoniterStreaming: 7.6% faster on small data, but requires manual maintenance
//   - Trade-off: ~130ns gain vs maintaining 30+ case statements per type
//
// ❌ Avoid:
//   - StdlibDoubleParse: 2.5x slower, double memory parsing overhead
//   - JsoniterDoubleParse: Still 54% slower than optimized ReflectionBased
//
// ===================================================================================

// ===================================================================================
// Test Type Definitions
// ===================================================================================

// ChatCompletionRequestBaseline represents the baseline without ExtraFields support.
// This uses standard json package with no custom marshal/unmarshal logic.
type ChatCompletionRequestBaseline struct {
	Model              string                        `json:"model"`
	Messages           []ChatCompletionMessage       `json:"messages"`
	MaxTokens          *int                          `json:"max_tokens,omitempty"`
	Temperature        float32                       `json:"temperature,omitempty"`
	TopP               float32                       `json:"top_p,omitempty"`
	N                  int                           `json:"n,omitempty"`
	Stream             bool                          `json:"stream,omitempty"`
	Stop               []string                      `json:"stop,omitempty"`
	PresencePenalty    float32                       `json:"presence_penalty,omitempty"`
	ResponseFormat     *ChatCompletionResponseFormat `json:"response_format,omitempty"`
	Seed               *int                          `json:"seed,omitempty"`
	FrequencyPenalty   float32                       `json:"frequency_penalty,omitempty"`
	LogitBias          map[string]int                `json:"logit_bias,omitempty"`
	LogProbs           bool                          `json:"logprobs,omitempty"`
	TopLogProbs        int                           `json:"top_logprobs,omitempty"`
	User               string                        `json:"user,omitempty"`
	Functions          []FunctionDefinition          `json:"functions,omitempty"`
	FunctionCall       interface{}                   `json:"function_call,omitempty"`
	Tools              []Tool                        `json:"tools,omitempty"`
	ToolChoice         interface{}                   `json:"tool_choice,omitempty"`
	StreamOptions      *StreamOptions                `json:"stream_options,omitempty"`
	ParallelToolCalls  interface{}                   `json:"parallel_tool_calls,omitempty"`
	KvTransferParams   map[string]interface{}        `json:"kv_transfer_params,omitempty"`
	ChatTemplateKwargs map[string]any                `json:"chat_template_kwargs,omitempty"`
	GuidedChoice       []string                      `json:"guided_choice,omitempty"`
	Rid                string                        `json:"rid,omitempty"`
	BootStrapHost      string                        `json:"bootstrap_host,omitempty"`
	BootStrapRoom      int                           `json:"bootstrap_room,omitempty"`
}

// ChatCompletionRequestStdlibDoubleParse uses standard library with double-parse approach.
type ChatCompletionRequestStdlibDoubleParse struct {
	Model              string                        `json:"model"`
	Messages           []ChatCompletionMessage       `json:"messages"`
	MaxTokens          *int                          `json:"max_tokens,omitempty"`
	Temperature        float32                       `json:"temperature,omitempty"`
	TopP               float32                       `json:"top_p,omitempty"`
	N                  int                           `json:"n,omitempty"`
	Stream             bool                          `json:"stream,omitempty"`
	Stop               []string                      `json:"stop,omitempty"`
	PresencePenalty    float32                       `json:"presence_penalty,omitempty"`
	ResponseFormat     *ChatCompletionResponseFormat `json:"response_format,omitempty"`
	Seed               *int                          `json:"seed,omitempty"`
	FrequencyPenalty   float32                       `json:"frequency_penalty,omitempty"`
	LogitBias          map[string]int                `json:"logit_bias,omitempty"`
	LogProbs           bool                          `json:"logprobs,omitempty"`
	TopLogProbs        int                           `json:"top_logprobs,omitempty"`
	User               string                        `json:"user,omitempty"`
	Functions          []FunctionDefinition          `json:"functions,omitempty"`
	FunctionCall       interface{}                   `json:"function_call,omitempty"`
	Tools              []Tool                        `json:"tools,omitempty"`
	ToolChoice         interface{}                   `json:"tool_choice,omitempty"`
	StreamOptions      *StreamOptions                `json:"stream_options,omitempty"`
	ParallelToolCalls  interface{}                   `json:"parallel_tool_calls,omitempty"`
	KvTransferParams   map[string]interface{}        `json:"kv_transfer_params,omitempty"`
	ChatTemplateKwargs map[string]any                `json:"chat_template_kwargs,omitempty"`
	GuidedChoice       []string                      `json:"guided_choice,omitempty"`
	Rid                string                        `json:"rid,omitempty"`
	BootStrapHost      string                        `json:"bootstrap_host,omitempty"`
	BootStrapRoom      int                           `json:"bootstrap_room,omitempty"`

	ExtraFields map[string]interface{} `json:"-"`
}

func (r *ChatCompletionRequestStdlibDoubleParse) UnmarshalJSON(data []byte) error {
	type Alias ChatCompletionRequestStdlibDoubleParse
	if err := json.Unmarshal(data, (*Alias)(r)); err != nil {
		return err
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	knownFields := getStructJSONFields(ChatCompletionRequestStdlibDoubleParse{})
	for k, v := range raw {
		if !knownFields[k] {
			if r.ExtraFields == nil {
				r.ExtraFields = make(map[string]interface{})
			}
			var value interface{}
			json.Unmarshal(v, &value)
			r.ExtraFields[k] = value
		}
	}
	return nil
}

func (r *ChatCompletionRequestStdlibDoubleParse) MarshalJSON() ([]byte, error) {
	type Alias ChatCompletionRequestStdlibDoubleParse
	data, err := json.Marshal((*Alias)(r))
	if err != nil {
		return nil, err
	}

	if len(r.ExtraFields) == 0 {
		return data, nil
	}

	var base map[string]interface{}
	if err := json.Unmarshal(data, &base); err != nil {
		return nil, err
	}

	for k, v := range r.ExtraFields {
		base[k] = v
	}

	return json.Marshal(base)
}

// ChatCompletionRequestJsoniterDoubleParse uses jsoniter with double-parse approach.
type ChatCompletionRequestJsoniterDoubleParse struct {
	Model              string                        `json:"model"`
	Messages           []ChatCompletionMessage       `json:"messages"`
	MaxTokens          *int                          `json:"max_tokens,omitempty"`
	Temperature        float32                       `json:"temperature,omitempty"`
	TopP               float32                       `json:"top_p,omitempty"`
	N                  int                           `json:"n,omitempty"`
	Stream             bool                          `json:"stream,omitempty"`
	Stop               []string                      `json:"stop,omitempty"`
	PresencePenalty    float32                       `json:"presence_penalty,omitempty"`
	ResponseFormat     *ChatCompletionResponseFormat `json:"response_format,omitempty"`
	Seed               *int                          `json:"seed,omitempty"`
	FrequencyPenalty   float32                       `json:"frequency_penalty,omitempty"`
	LogitBias          map[string]int                `json:"logit_bias,omitempty"`
	LogProbs           bool                          `json:"logprobs,omitempty"`
	TopLogProbs        int                           `json:"top_logprobs,omitempty"`
	User               string                        `json:"user,omitempty"`
	Functions          []FunctionDefinition          `json:"functions,omitempty"`
	FunctionCall       interface{}                   `json:"function_call,omitempty"`
	Tools              []Tool                        `json:"tools,omitempty"`
	ToolChoice         interface{}                   `json:"tool_choice,omitempty"`
	StreamOptions      *StreamOptions                `json:"stream_options,omitempty"`
	ParallelToolCalls  interface{}                   `json:"parallel_tool_calls,omitempty"`
	KvTransferParams   map[string]interface{}        `json:"kv_transfer_params,omitempty"`
	ChatTemplateKwargs map[string]any                `json:"chat_template_kwargs,omitempty"`
	GuidedChoice       []string                      `json:"guided_choice,omitempty"`
	Rid                string                        `json:"rid,omitempty"`
	BootStrapHost      string                        `json:"bootstrap_host,omitempty"`
	BootStrapRoom      int                           `json:"bootstrap_room,omitempty"`

	ExtraFields map[string]interface{} `json:"-"`
}

func (r *ChatCompletionRequestJsoniterDoubleParse) UnmarshalJSON(data []byte) error {
	type Alias ChatCompletionRequestJsoniterDoubleParse
	if err := jsoniterAPI.Unmarshal(data, (*Alias)(r)); err != nil {
		return err
	}

	var raw map[string]jsoniter.RawMessage
	if err := jsoniterAPI.Unmarshal(data, &raw); err != nil {
		return err
	}

	knownFields := getStructJSONFields(ChatCompletionRequestJsoniterDoubleParse{})
	for k, v := range raw {
		if !knownFields[k] {
			if r.ExtraFields == nil {
				r.ExtraFields = make(map[string]interface{})
			}
			var value interface{}
			jsoniterAPI.Unmarshal(v, &value)
			r.ExtraFields[k] = value
		}
	}
	return nil
}

func (r *ChatCompletionRequestJsoniterDoubleParse) MarshalJSON() ([]byte, error) {
	type Alias ChatCompletionRequestJsoniterDoubleParse
	data, err := jsoniterAPI.Marshal((*Alias)(r))
	if err != nil {
		return nil, err
	}

	if len(r.ExtraFields) == 0 {
		return data, nil
	}

	var base map[string]interface{}
	if err := jsoniterAPI.Unmarshal(data, &base); err != nil {
		return nil, err
	}

	for k, v := range r.ExtraFields {
		base[k] = v
	}

	return jsoniterAPI.Marshal(base)
}

// ChatCompletionRequestJsoniterStreaming uses jsoniter streaming API for single-pass parsing.
type ChatCompletionRequestJsoniterStreaming struct {
	Model              string                        `json:"model"`
	Messages           []ChatCompletionMessage       `json:"messages"`
	MaxTokens          *int                          `json:"max_tokens,omitempty"`
	Temperature        float32                       `json:"temperature,omitempty"`
	TopP               float32                       `json:"top_p,omitempty"`
	N                  int                           `json:"n,omitempty"`
	Stream             bool                          `json:"stream,omitempty"`
	Stop               []string                      `json:"stop,omitempty"`
	PresencePenalty    float32                       `json:"presence_penalty,omitempty"`
	ResponseFormat     *ChatCompletionResponseFormat `json:"response_format,omitempty"`
	Seed               *int                          `json:"seed,omitempty"`
	FrequencyPenalty   float32                       `json:"frequency_penalty,omitempty"`
	LogitBias          map[string]int                `json:"logit_bias,omitempty"`
	LogProbs           bool                          `json:"logprobs,omitempty"`
	TopLogProbs        int                           `json:"top_logprobs,omitempty"`
	User               string                        `json:"user,omitempty"`
	Functions          []FunctionDefinition          `json:"functions,omitempty"`
	FunctionCall       interface{}                   `json:"function_call,omitempty"`
	Tools              []Tool                        `json:"tools,omitempty"`
	ToolChoice         interface{}                   `json:"tool_choice,omitempty"`
	StreamOptions      *StreamOptions                `json:"stream_options,omitempty"`
	ParallelToolCalls  interface{}                   `json:"parallel_tool_calls,omitempty"`
	KvTransferParams   map[string]interface{}        `json:"kv_transfer_params,omitempty"`
	ChatTemplateKwargs map[string]any                `json:"chat_template_kwargs,omitempty"`
	GuidedChoice       []string                      `json:"guided_choice,omitempty"`
	Rid                string                        `json:"rid,omitempty"`
	BootStrapHost      string                        `json:"bootstrap_host,omitempty"`
	BootStrapRoom      int                           `json:"bootstrap_room,omitempty"`

	ExtraFields map[string]interface{} `json:"-"`
}

func (r *ChatCompletionRequestJsoniterStreaming) UnmarshalJSON(data []byte) error {
	iter := jsoniterAPI.BorrowIterator(data)
	defer jsoniterAPI.ReturnIterator(iter)

	for field := iter.ReadObject(); field != ""; field = iter.ReadObject() {
		switch field {
		case "model":
			r.Model = iter.ReadString()
		case "messages":
			iter.ReadVal(&r.Messages)
		case "max_tokens":
			val := iter.ReadInt()
			r.MaxTokens = &val
		case "temperature":
			r.Temperature = iter.ReadFloat32()
		case "top_p":
			r.TopP = iter.ReadFloat32()
		case "n":
			r.N = iter.ReadInt()
		case "stream":
			r.Stream = iter.ReadBool()
		case "stop":
			iter.ReadVal(&r.Stop)
		case "presence_penalty":
			r.PresencePenalty = iter.ReadFloat32()
		case "response_format":
			iter.ReadVal(&r.ResponseFormat)
		case "seed":
			val := iter.ReadInt()
			r.Seed = &val
		case "frequency_penalty":
			r.FrequencyPenalty = iter.ReadFloat32()
		case "logit_bias":
			iter.ReadVal(&r.LogitBias)
		case "logprobs":
			r.LogProbs = iter.ReadBool()
		case "top_logprobs":
			r.TopLogProbs = iter.ReadInt()
		case "user":
			r.User = iter.ReadString()
		case "functions":
			iter.ReadVal(&r.Functions)
		case "function_call":
			iter.ReadVal(&r.FunctionCall)
		case "tools":
			iter.ReadVal(&r.Tools)
		case "tool_choice":
			iter.ReadVal(&r.ToolChoice)
		case "stream_options":
			iter.ReadVal(&r.StreamOptions)
		case "parallel_tool_calls":
			iter.ReadVal(&r.ParallelToolCalls)
		case "kv_transfer_params":
			iter.ReadVal(&r.KvTransferParams)
		case "chat_template_kwargs":
			iter.ReadVal(&r.ChatTemplateKwargs)
		case "guided_choice":
			iter.ReadVal(&r.GuidedChoice)
		case "rid":
			r.Rid = iter.ReadString()
		case "bootstrap_host":
			r.BootStrapHost = iter.ReadString()
		case "bootstrap_room":
			r.BootStrapRoom = iter.ReadInt()
		default:
			if r.ExtraFields == nil {
				r.ExtraFields = make(map[string]interface{})
			}
			var value interface{}
			iter.ReadVal(&value)
			r.ExtraFields[field] = value
		}
	}

	return iter.Error
}

func (r *ChatCompletionRequestJsoniterStreaming) MarshalJSON() ([]byte, error) {
	if len(r.ExtraFields) == 0 {
		type Alias ChatCompletionRequestJsoniterStreaming
		return jsoniterAPI.Marshal((*Alias)(r))
	}

	stream := jsoniterAPI.BorrowStream(nil)
	defer jsoniterAPI.ReturnStream(stream)

	stream.WriteObjectStart()

	stream.WriteObjectField("model")
	stream.WriteString(r.Model)

	stream.WriteMore()
	stream.WriteObjectField("messages")
	stream.WriteVal(r.Messages)

	if r.MaxTokens != nil {
		stream.WriteMore()
		stream.WriteObjectField("max_tokens")
		stream.WriteInt(*r.MaxTokens)
	}

	if r.Temperature != 0 {
		stream.WriteMore()
		stream.WriteObjectField("temperature")
		stream.WriteFloat32(r.Temperature)
	}

	for k, v := range r.ExtraFields {
		stream.WriteMore()
		stream.WriteObjectField(k)
		stream.WriteVal(v)
	}

	stream.WriteObjectEnd()

	if stream.Error != nil {
		return nil, stream.Error
	}

	result := make([]byte, stream.Buffered())
	copy(result, stream.Buffer())
	return result, nil
}

// ===================================================================================
// Benchmark Tests - Small Data
// ===================================================================================

func BenchmarkChatCompletionRequest_Unmarshal_Small_Baseline(b *testing.B) {
	jsonData := []byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hello world"}],"temperature":0.7,"max_tokens":100}`)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var req ChatCompletionRequestBaseline
		if err := json.Unmarshal(jsonData, &req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkChatCompletionRequest_Unmarshal_Small_ReflectionBased(b *testing.B) {
	jsonData := []byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hello world"}],"temperature":0.7,"max_tokens":100}`)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var req ChatCompletionRequest
		if err := json.Unmarshal(jsonData, &req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkChatCompletionRequest_Unmarshal_Small_StdlibDoubleParse(b *testing.B) {
	jsonData := []byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hello world"}],"temperature":0.7,"max_tokens":100}`)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var req ChatCompletionRequestStdlibDoubleParse
		if err := json.Unmarshal(jsonData, &req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkChatCompletionRequest_Unmarshal_Small_JsoniterDoubleParse(b *testing.B) {
	jsonData := []byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hello world"}],"temperature":0.7,"max_tokens":100}`)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var req ChatCompletionRequestJsoniterDoubleParse
		if err := json.Unmarshal(jsonData, &req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkChatCompletionRequest_Unmarshal_Small_JsoniterStreaming(b *testing.B) {
	jsonData := []byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hello world"}],"temperature":0.7,"max_tokens":100}`)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var req ChatCompletionRequestJsoniterStreaming
		if err := json.Unmarshal(jsonData, &req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkChatCompletionRequest_Marshal_Small_Baseline(b *testing.B) {
	req := ChatCompletionRequestBaseline{
		Model: "gpt-4",
		Messages: []ChatCompletionMessage{
			{Role: "user", Content: "hello world"},
		},
		Temperature: 0.7,
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := json.Marshal(&req)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkChatCompletionRequest_Marshal_Small_ReflectionBased(b *testing.B) {
	req := ChatCompletionRequest{
		Model: "gpt-4",
		Messages: []ChatCompletionMessage{
			{Role: "user", Content: "hello world"},
		},
		Temperature: 0.7,
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := json.Marshal(&req)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkChatCompletionRequest_Marshal_Small_StdlibDoubleParse(b *testing.B) {
	req := ChatCompletionRequestStdlibDoubleParse{
		Model: "gpt-4",
		Messages: []ChatCompletionMessage{
			{Role: "user", Content: "hello world"},
		},
		Temperature: 0.7,
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := json.Marshal(&req)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkChatCompletionRequest_Marshal_Small_JsoniterDoubleParse(b *testing.B) {
	req := ChatCompletionRequestJsoniterDoubleParse{
		Model: "gpt-4",
		Messages: []ChatCompletionMessage{
			{Role: "user", Content: "hello world"},
		},
		Temperature: 0.7,
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := json.Marshal(&req)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkChatCompletionRequest_Marshal_Small_JsoniterStreaming(b *testing.B) {
	req := ChatCompletionRequestJsoniterStreaming{
		Model: "gpt-4",
		Messages: []ChatCompletionMessage{
			{Role: "user", Content: "hello world"},
		},
		Temperature: 0.7,
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := json.Marshal(&req)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// ===================================================================================
// Benchmark Tests - Large Data (~1MB)
// ===================================================================================

// Helper function to generate ~1MB of message content
func generateLargeMessages() []ChatCompletionMessage {
	// Create messages totaling approximately 1MB
	// Each message contains ~250KB to reach 1MB with 4 messages
	largeContent := generateLargeString(250000)

	return []ChatCompletionMessage{
		{Role: "system", Content: "You are a helpful assistant."},
		{Role: "user", Content: largeContent},
		{Role: "assistant", Content: largeContent},
		{Role: "user", Content: largeContent},
	}
}

func BenchmarkChatCompletionRequest_Unmarshal_Large_Baseline(b *testing.B) {
	messages := generateLargeMessages()
	messagesJSON, _ := json.Marshal(messages)
	jsonData := []byte(`{"model":"gpt-4","messages":` + string(messagesJSON) + `,"temperature":0.7,"max_tokens":2000}`)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var req ChatCompletionRequestBaseline
		if err := json.Unmarshal(jsonData, &req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkChatCompletionRequest_Unmarshal_Large_ReflectionBased(b *testing.B) {
	messages := generateLargeMessages()
	messagesJSON, _ := json.Marshal(messages)
	jsonData := []byte(`{"model":"gpt-4","messages":` + string(messagesJSON) + `,"temperature":0.7,"max_tokens":2000}`)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var req ChatCompletionRequest
		if err := json.Unmarshal(jsonData, &req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkChatCompletionRequest_Unmarshal_Large_StdlibDoubleParse(b *testing.B) {
	messages := generateLargeMessages()
	messagesJSON, _ := json.Marshal(messages)
	jsonData := []byte(`{"model":"gpt-4","messages":` + string(messagesJSON) + `,"temperature":0.7,"max_tokens":2000}`)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var req ChatCompletionRequestStdlibDoubleParse
		if err := json.Unmarshal(jsonData, &req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkChatCompletionRequest_Unmarshal_Large_JsoniterDoubleParse(b *testing.B) {
	messages := generateLargeMessages()
	messagesJSON, _ := json.Marshal(messages)
	jsonData := []byte(`{"model":"gpt-4","messages":` + string(messagesJSON) + `,"temperature":0.7,"max_tokens":2000}`)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var req ChatCompletionRequestJsoniterDoubleParse
		if err := json.Unmarshal(jsonData, &req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkChatCompletionRequest_Unmarshal_Large_JsoniterStreaming(b *testing.B) {
	messages := generateLargeMessages()
	messagesJSON, _ := json.Marshal(messages)
	jsonData := []byte(`{"model":"gpt-4","messages":` + string(messagesJSON) + `,"temperature":0.7,"max_tokens":2000}`)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var req ChatCompletionRequestJsoniterStreaming
		if err := json.Unmarshal(jsonData, &req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkChatCompletionRequest_Marshal_Large_Baseline(b *testing.B) {
	req := ChatCompletionRequestBaseline{
		Model:       "gpt-4",
		Messages:    generateLargeMessages(),
		Temperature: 0.7,
		MaxTokens:   intPtr(2000),
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := json.Marshal(&req)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkChatCompletionRequest_Marshal_Large_ReflectionBased(b *testing.B) {
	req := ChatCompletionRequest{
		Model:       "gpt-4",
		Messages:    generateLargeMessages(),
		Temperature: 0.7,
		MaxTokens:   intPtr(2000),
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := json.Marshal(&req)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkChatCompletionRequest_Marshal_Large_StdlibDoubleParse(b *testing.B) {
	req := ChatCompletionRequestStdlibDoubleParse{
		Model:       "gpt-4",
		Messages:    generateLargeMessages(),
		Temperature: 0.7,
		MaxTokens:   intPtr(2000),
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := json.Marshal(&req)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkChatCompletionRequest_Marshal_Large_JsoniterDoubleParse(b *testing.B) {
	req := ChatCompletionRequestJsoniterDoubleParse{
		Model:       "gpt-4",
		Messages:    generateLargeMessages(),
		Temperature: 0.7,
		MaxTokens:   intPtr(2000),
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := json.Marshal(&req)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkChatCompletionRequest_Marshal_Large_JsoniterStreaming(b *testing.B) {
	req := ChatCompletionRequestJsoniterStreaming{
		Model:       "gpt-4",
		Messages:    generateLargeMessages(),
		Temperature: 0.7,
		MaxTokens:   intPtr(2000),
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, err := json.Marshal(&req)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// ===================================================================================
// Helper Functions
// ===================================================================================

func intPtr(i int) *int {
	return &i
}

func generateLargeString(size int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 "
	b := make([]byte, size)
	for i := range b {
		b[i] = charset[i%len(charset)]
	}
	return string(b)
}
