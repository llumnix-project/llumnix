package anthropic

type Message struct {
	Role    string `json:"role" validate:"required"`
	Content any    `json:"content" validate:"required"`
}

type Tool struct {
	Name        string         `json:"name" validate:"required"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema,omitempty"`
}

type Request struct {
	Model         string            `json:"model" validate:"required"`
	MaxTokens     int               `json:"max_tokens" validate:"required"`
	Messages      []Message         `json:"messages" validate:"required"`
	System        any               `json:"system,omitempty"`
	StopSequences []string          `json:"stop_sequences,omitempty"`
	Stream        bool              `json:"stream,omitempty"`
	Temperature   float32           `json:"temperature,omitempty"`
	TopP          float32           `json:"top_p,omitempty"`
	Metadata      map[string]any    `json:"metadata,omitempty"`
	Tools         []Tool            `json:"tools,omitempty"`
	ToolChoice    map[string]string `json:"tool_choice,omitempty"`
}

type TokenCountRequest struct {
	Model      string         `json:"model" validate:"required"`
	Messages   []Message      `json:"messages" validate:"required"`
	System     any            `json:"system,omitempty"`
	Tools      []Tool         `json:"tools,omitempty"`
	ToolChoice map[string]any `json:"tool_choice,omitempty"`
}

type Usage struct {
	InputTokens          int `json:"input_tokens" validate:"required"`
	OutputTokens         int `json:"output_tokens" validate:"required"`
	CacheReadInputTokens int `json:"cache_read_input_tokens" validate:"required"`

	CacheCreation            *any    `json:"cache_creation" validate:"required"`
	CacheCreationInputTokens *int    `json:"cache_creation_input_tokens" validate:"required"`
	ServerToolUse            *any    `json:"server_tool_use" validate:"required"`
	ServiceTier              *string `json:"service_tier" validate:"required"`
}

type Response struct {
	ID           string   `json:"id" validate:"required"`
	Type         string   `json:"type" validate:"required"`
	Role         string   `json:"role" validate:"required"`
	Model        string   `json:"model" validate:"required"`
	StopReason   *string  `json:"stop_reason" validate:"required"`
	StopSequence []string `json:"stop_sequence" validate:"required"`
	Usage        *Usage   `json:"usage" validate:"required"`
	Container    *any     `json:"container" validate:"required"`

	Content []any `json:"content"`
}

type MessageDelta struct {
	StopReason   string  `json:"stop_reason" validate:"required"`
	StopSequence *string `json:"stop_sequence" validate:"required"`
}

type ContentBlockDeltaText struct {
	Type string `json:"type" validate:"required"`
	Text string `json:"text" validate:"required"`
}

type ContentBlockDeltaInputJson struct {
	Type        string `json:"type" validate:"required"`
	PartialJson string `json:"partial_json" validate:"required"`
}

type ContentBlockText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type ContentBlockToolUse struct {
	Id    string `json:"id"`
	Name  string `json:"name"`
	Type  string `json:"type"`
	Input any    `json:"input"`
}

type EventMessageStart struct {
	Type    string   `json:"type" validate:"required"`
	Message Response `json:"message" validate:"required"`
}

type EventContentBlockStart struct {
	Type         string `json:"type" validate:"required"`
	Index        int    `json:"index" validate:"required"`
	ContentBlock any    `json:"content_block" validate:"required"`
}

type EventPing struct {
	Type string `json:"type" validate:"required"`
}

type EventContentBlockDelta struct {
	Type  string `json:"type" validate:"required"`
	Index int    `json:"index" validate:"required"`
	Delta any    `json:"delta" validate:"required"`
}

type EventContentBlockStop struct {
	Type  string `json:"type" validate:"required"`
	Index int    `json:"index" validate:"required"`
}

type EventMessageDelta struct {
	Type  string       `json:"type" validate:"required"`
	Delta MessageDelta `json:"delta" validate:"required"`
	Usage Usage        `json:"usage" validate:"required"`
}

type EventMessageStop struct {
	Type string `json:"type" validate:"required"`
}

type ToolCall struct {
	Id         string `json:"id"`
	Name       string `json:"name"`
	ArgsBuffer string `json:"args_buffer"`
	JSONSent   bool   `json:"json_sent"`
	Index      int    `json:"index"`
	Started    bool   `json:"started"`
}

type StreamingResponseBuffer struct {
	FinalStopReason string
	Usage           Usage
	ToolCounter     int
	ToolCalls       map[string]*ToolCall
}
