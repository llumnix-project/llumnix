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

func (p ProtocolType) String() string {
	switch p {
	case OpenAIChatCompletion:
		return "chat"
	case OpenAICompletion:
		return "completions"
	default:
		return "unknown"
	}
}

const (
	OpenAIChatCompletion ProtocolType = iota
	OpenAICompletion
)

// FieldSetter defines the interface for setting request fields.
// CompletionRequest and ChatCompletionRequest should implement this interface
// to support unified field assignment logic.
type FieldSetter interface {
	SetKvTransferParams(params map[string]interface{})
	SetRid(rid string)
	SetBootStrapHost(host string)
	SetBootStrapRoom(room int)
	SetMaxTokens(maxTokens int)
	SetStream(stream bool)
}

// ApplyRequestArgs applies key-value arguments to any request type that implements FieldSetter.
// This eliminates repetitive switch-case logic by using a dispatch table pattern.
// Example:
//
//	args := map[string]interface{}{"rid": "req-123", "max_tokens": 100}
//	ApplyRequestArgs(completionReq, args)
func ApplyRequestArgs[T FieldSetter](req T, args map[string]interface{}) {
	// Check if there are any arguments to process
	if len(args) == 0 {
		return
	}
	// Dispatch table: map field names to setter functions
	// This approach avoids deep switch nesting and improves readability.
	type fieldHandler func(T, interface{})
	dispatchTable := map[string]fieldHandler{
		"kv_transfer_params": func(r T, v interface{}) { r.SetKvTransferParams(v.(map[string]interface{})) },
		"rid":                func(r T, v interface{}) { r.SetRid(v.(string)) },
		"bootstrap_host":     func(r T, v interface{}) { r.SetBootStrapHost(v.(string)) },
		"bootstrap_room":     func(r T, v interface{}) { r.SetBootStrapRoom(v.(int)) },
		"max_tokens":         func(r T, v interface{}) { r.SetMaxTokens(v.(int)) },
		"stream":             func(r T, v interface{}) { r.SetStream(v.(bool)) },
	}

	for key, value := range args {
		if handler, exists := dispatchTable[key]; exists {
			handler(req, value)
		}
	}
}
