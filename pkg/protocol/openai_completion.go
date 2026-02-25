package protocol

import (
	"encoding/json"
	"fmt"
)

const (
	CompletionsPath   = "/v1/completions"
	CompletionsSuffix = "/completions"
)

func IsCompletionsURL(url string) bool {
	return len(url) >= len(CompletionsSuffix) && url[len(url)-len(CompletionsSuffix):] == CompletionsSuffix
}

type PromptValue struct {
	value interface{}
}

func (p PromptValue) MarshalJSON() ([]byte, error) {
	return json.Marshal(p.value)
}

func (p *PromptValue) UnmarshalJSON(data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("empty prompt field")
	}
	switch data[0] {
	case '"':
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		p.value = s

	case '[':
		// try []uint32 -> []string -> [][]uint32
		if result := []uint32{}; json.Unmarshal(data, &result) == nil {
			p.value = result
			return nil
		}
		if result := []string{}; json.Unmarshal(data, &result) == nil {
			p.value = result
			return nil
		}
		if result := [][]uint32{}; json.Unmarshal(data, &result) == nil {
			p.value = result
			return nil
		}
		return fmt.Errorf("array is not []string, []uint32 or [][]uint32")
	default:
		return fmt.Errorf("unsupported prompt type: expected string or array")
	}
	return nil
}

func (p PromptValue) GetString() (string, bool) {
	s, ok := p.value.(string)
	return s, ok
}

func (p PromptValue) GetStringSlice() ([]string, bool) {
	s, ok := p.value.([]string)
	return s, ok
}

func (p PromptValue) GetUint32Slice() ([]uint32, bool) {
	u, ok := p.value.([]uint32)
	return u, ok
}

func (p PromptValue) GetUint32Matrix() ([][]uint32, bool) {
	m, ok := p.value.([][]uint32)
	return m, ok
}

func (p *PromptValue) SetValue(v interface{}) {
	p.value = v
}

// CompletionRequest represents a request structure for completion API.
type CompletionRequest struct {
	Id               string      `json:"request_id,omitempty"` // vllm
	Rid              string      `json:"rid,omitempty"`        // sglang
	Model            string      `json:"model"`
	Prompt           PromptValue `json:"prompt,omitempty"`
	BestOf           int         `json:"best_of,omitempty"`
	Echo             bool        `json:"echo,omitempty"`
	FrequencyPenalty float32     `json:"frequency_penalty,omitempty"`
	// LogitBias is must be a token id string (specified by their token ID in the tokenizer), not a word string.
	// incorrect: `"logit_bias":{"You": 6}`, correct: `"logit_bias":{"1639": 6}`
	// refs: https://platform.openai.com/docs/api-reference/completions/create#completions/create-logit_bias
	LogitBias           map[string]int         `json:"logit_bias,omitempty"`
	LogProbs            int                    `json:"logprobs,omitempty"`
	MaxTokens           *int                   `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int                   `json:"max_completion_tokens,omitempty"`
	N                   int                    `json:"n,omitempty"`
	PresencePenalty     float32                `json:"presence_penalty,omitempty"`
	Seed                *int                   `json:"seed,omitempty"`
	Stop                []string               `json:"stop,omitempty"`
	Stream              bool                   `json:"stream,omitempty"`
	StreamOptions       *StreamOptions         `json:"stream_options,omitempty"`
	Suffix              string                 `json:"suffix,omitempty"`
	Temperature         float32                `json:"temperature,omitempty"`
	TopP                float32                `json:"top_p,omitempty"`
	User                string                 `json:"user,omitempty"`
	KvTransferParams    map[string]interface{} `json:"kv_transfer_params,omitempty"`
	BootStrapHost       string                 `json:"bootstrap_host,omitempty"` // sglang
	BootStrapRoom       int                    `json:"bootstrap_room,omitempty"` // sglang

	// ExtraFields stores any additional fields not explicitly defined in the struct.
	// This allows preserving unknown fields during JSON unmarshal/marshal operations.
	ExtraFields map[string]interface{} `json:"-"`
}

// CompletionChoice represents one of possible completions.
type CompletionChoice struct {
	Text           string        `json:"text"`
	Index          int           `json:"index"`
	FinishReason   FinishReason  `json:"finish_reason"`
	LogProbs       LogprobResult `json:"logprobs"`
	StopReason     interface{}   `json:"stop_reason,omitempty"`
	PromptLogprobs interface{}   `json:"prompt_logprobs,omitempty"`
}

// LogprobResult represents logprob result of Choice.
type LogprobResult struct {
	Tokens        []string             `json:"tokens"`
	TokenLogprobs []float32            `json:"token_logprobs"`
	TopLogprobs   []map[string]float32 `json:"top_logprobs"`
	TextOffset    []int                `json:"text_offset"`
}

// CompletionResponse represents a response structure for completion API.
type CompletionResponse struct {
	ID                string                 `json:"id"`
	Object            string                 `json:"object"`
	Created           int64                  `json:"created"`
	Model             string                 `json:"model"`
	Choices           []CompletionChoice     `json:"choices"`
	Usage             *Usage                 `json:"usage"`
	ServiceTier       string                 `json:"service_tier,omitempty"`
	KvTransferParams  map[string]interface{} `json:"kv_transfer_params,omitempty"`
	SystemFingerprint string                 `json:"system_fingerprint"`

	// ExtraFields stores any additional fields not explicitly defined in the struct.
	// This allows preserving unknown fields during JSON unmarshal/marshal operations.
	ExtraFields map[string]interface{} `json:"-"`
}

// UnmarshalJSON custom unmarshaler using reflection-based streaming API.
// This implementation automatically discovers fields via reflection and uses
// jsoniter's streaming API for optimal performance with minimal maintenance overhead.
func (r *CompletionResponse) UnmarshalJSON(data []byte) error {
	return unmarshalWithReflection(data, r)
}

// MarshalJSON custom marshaler for CompletionResponse that includes unknown fields.
// Uses jsoniter for consistent serialization with UnmarshalJSON.
func (r *CompletionResponse) MarshalJSON() ([]byte, error) {
	type Alias CompletionResponse
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

// Clone creates a deep copy of the CompletionRequest.
func (r *CompletionRequest) Clone() *CompletionRequest {
	if r == nil {
		return nil
	}

	// Direct value copy for all primitive fields
	cloned := &CompletionRequest{
		Id:               r.Id,
		Rid:              r.Rid,
		Model:            r.Model,
		BestOf:           r.BestOf,
		Echo:             r.Echo,
		FrequencyPenalty: r.FrequencyPenalty,
		LogProbs:         r.LogProbs,
		N:                r.N,
		PresencePenalty:  r.PresencePenalty,
		Stream:           r.Stream,
		Suffix:           r.Suffix,
		Temperature:      r.Temperature,
		TopP:             r.TopP,
		User:             r.User,
		BootStrapHost:    r.BootStrapHost,
		BootStrapRoom:    r.BootStrapRoom,
	}

	// Deep copy Prompt (PromptValue contains interface{} field)
	cloned.Prompt = clonePromptValue(&r.Prompt)

	// Deep copy LogitBias map with pre-allocated capacity
	if len(r.LogitBias) > 0 {
		cloned.LogitBias = make(map[string]int, len(r.LogitBias))
		for k, v := range r.LogitBias {
			cloned.LogitBias[k] = v
		}
	}

	// Deep copy MaxTokens pointer
	if r.MaxTokens != nil {
		val := *r.MaxTokens
		cloned.MaxTokens = &val
	}

	// Deep copy MaxCompletionTokens pointer
	if r.MaxCompletionTokens != nil {
		val := *r.MaxCompletionTokens
		cloned.MaxCompletionTokens = &val
	}

	// Deep copy Seed pointer
	if r.Seed != nil {
		val := *r.Seed
		cloned.Seed = &val
	}

	// Deep copy Stop slice
	if len(r.Stop) > 0 {
		cloned.Stop = make([]string, len(r.Stop))
		copy(cloned.Stop, r.Stop)
	}

	// Deep copy StreamOptions pointer
	if r.StreamOptions != nil {
		cloned.StreamOptions = &StreamOptions{
			IncludeUsage:         r.StreamOptions.IncludeUsage,
			ContinuousUsageStats: r.StreamOptions.ContinuousUsageStats,
		}
	}

	// Shallow copy KvTransferParams map values (interface{} deep copy requires reflection)
	if len(r.KvTransferParams) > 0 {
		cloned.KvTransferParams = make(map[string]interface{}, len(r.KvTransferParams))
		for k, v := range r.KvTransferParams {
			cloned.KvTransferParams[k] = v
		}
	}

	// Shallow copy ExtraFields map values (interface{} deep copy requires reflection)
	if len(r.ExtraFields) > 0 {
		cloned.ExtraFields = make(map[string]interface{}, len(r.ExtraFields))
		for k, v := range r.ExtraFields {
			cloned.ExtraFields[k] = v
		}
	}

	return cloned
}

// UnmarshalJSON custom unmarshaler using reflection-based streaming API.
// This implementation automatically discovers fields via reflection and uses
// jsoniter's streaming API for optimal performance with minimal maintenance overhead.
func (r *CompletionRequest) UnmarshalJSON(data []byte) error {
	return unmarshalWithReflection(data, r)
}

// MarshalJSON custom marshaler for CompletionRequest that includes unknown fields.
// Uses jsoniter for consistent serialization with UnmarshalJSON.
func (r *CompletionRequest) MarshalJSON() ([]byte, error) {
	type Alias CompletionRequest
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

// clonePromptValue performs deep copy of PromptValue.
// The internal value is copied based on its runtime type (string, []string, []uint32, [][]uint32).
func clonePromptValue(p *PromptValue) PromptValue {
	if p == nil || p.value == nil {
		return PromptValue{}
	}

	// Type switch to handle different prompt value types
	switch v := p.value.(type) {
	case string:
		// String is immutable, direct copy is safe
		return PromptValue{value: v}

	case []string:
		// Deep copy string slice
		if len(v) > 0 {
			cloned := make([]string, len(v))
			copy(cloned, v)
			return PromptValue{value: cloned}
		}
		return PromptValue{value: []string{}}

	case []uint32:
		// Deep copy uint32 slice
		if len(v) > 0 {
			cloned := make([]uint32, len(v))
			copy(cloned, v)
			return PromptValue{value: cloned}
		}
		return PromptValue{value: []uint32{}}

	case [][]uint32:
		// Deep copy 2D uint32 matrix
		if len(v) > 0 {
			cloned := make([][]uint32, len(v))
			for i := range v {
				if len(v[i]) > 0 {
					cloned[i] = make([]uint32, len(v[i]))
					copy(cloned[i], v[i])
				}
			}
			return PromptValue{value: cloned}
		}
		return PromptValue{value: [][]uint32{}}

	default:
		// Fallback for unexpected types (should not happen in normal usage)
		return PromptValue{value: v}
	}
}

// Setter methods for CompletionRequest to support unified field assignment

func (r *CompletionRequest) SetKvTransferParams(params map[string]interface{}) {
	r.KvTransferParams = params
}

func (r *CompletionRequest) SetRid(rid string) {
	r.Rid = rid
}

func (r *CompletionRequest) SetBootStrapHost(host string) {
	r.BootStrapHost = host
}

func (r *CompletionRequest) SetBootStrapRoom(room int) {
	r.BootStrapRoom = room
}

func (r *CompletionRequest) SetMaxTokens(maxTokens int) {
	r.MaxTokens = &maxTokens
}

func (r *CompletionRequest) SetStream(stream bool) {
	r.Stream = stream
}
