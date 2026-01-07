// openai protocol refer:  https://github.com/sashabaranov/go-openai/blob/master/chat.go

package protocol

type CompletionTokensDetails struct {
	ReasoningTokens uint64 `json:"reasoning_tokens"`
}
type PromptTokensDetails struct {
	CachedTokens uint64 `json:"cached_tokens"`
}

// common.go defines common types used throughout the OpenAI API.
// Usage Represents the total token usage per request to OpenAI.
type Usage struct {
	PromptTokens            uint64                   `json:"prompt_tokens"`
	CompletionTokens        uint64                   `json:"completion_tokens"`
	TotalTokens             uint64                   `json:"total_tokens"`
	CompletionTokensDetails *CompletionTokensDetails `json:"completion_tokens_details"`
	PromptTokensDetails     *PromptTokensDetails     `json:"prompt_tokens_details,omitempty"`
}

type ProtocolType int

const (
	ProtocolChat ProtocolType = iota
	ProtocolCompletions
	ProtocolTokenizedChat
	ProtocolUnknown
)
