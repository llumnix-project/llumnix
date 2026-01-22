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
	Temperature         float32                `json:"temperature"`
	TopP                float32                `json:"top_p,omitempty"`
	User                string                 `json:"user,omitempty"`
	KvTransferParams    map[string]interface{} `json:"kv_transfer_params,omitempty"`
	BootStrapHost       string                 `json:"bootstrap_host,omitempty"` // sglang
	BootStrapRoom       string                 `json:"bootstrap_room,omitempty"` // sglang
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
}
