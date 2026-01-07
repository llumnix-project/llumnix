package protocol

import "easgo/pkg/llm-gateway/consts"

const CompletionsSuffix = "/completions"

func CheckPromptType(prompt interface{}) error {
	_, isString := prompt.(string)
	_, isStringSlice := prompt.([]string)
	if isString || isStringSlice {
		return nil
	}
	return consts.ErrorCompletionRequestPromptTypeNotSupported
}

// CompletionRequest represents a request structure for completion API.
type CompletionRequest struct {
	Model            string      `json:"model"`
	Prompt           interface{} `json:"prompt,omitempty"`
	BestOf           int         `json:"best_of,omitempty"`
	Echo             bool        `json:"echo,omitempty"`
	FrequencyPenalty float32     `json:"frequency_penalty,omitempty"`
	// LogitBias is must be a token id string (specified by their token ID in the tokenizer), not a word string.
	// incorrect: `"logit_bias":{"You": 6}`, correct: `"logit_bias":{"1639": 6}`
	// refs: https://platform.openai.com/docs/api-reference/completions/create#completions/create-logit_bias
	LogitBias       map[string]int `json:"logit_bias,omitempty"`
	LogProbs        int            `json:"logprobs,omitempty"`
	MaxTokens       int            `json:"max_tokens,omitempty"`
	N               int            `json:"n,omitempty"`
	PresencePenalty float32        `json:"presence_penalty,omitempty"`
	Seed            *int           `json:"seed,omitempty"`
	Stop            []string       `json:"stop,omitempty"`
	Stream          bool           `json:"stream,omitempty"`
	StreamOptions   *StreamOptions `json:"stream_options,omitempty"`
	Suffix          string         `json:"suffix,omitempty"`
	Temperature     float32        `json:"temperature,omitempty"`
	TopP            float32        `json:"top_p,omitempty"`
	User            string         `json:"user,omitempty"`
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
	ID                string             `json:"id"`
	Object            string             `json:"object"`
	Created           int64              `json:"created"`
	Model             string             `json:"model"`
	Choices           []CompletionChoice `json:"choices"`
	Usage             *Usage             `json:"usage"`
	ServiceTier       string             `json:"service_tier,omitempty"`
	KvTransferParams  interface{}        `json:"kv_transfer_params,omitempty"`
	SystemFingerprint string             `json:"system_fingerprint"`
}
