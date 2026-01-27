package consts

const (
	ROLE_SYSTEM    = "system"
	ROLE_USER      = "user"
	ROLE_ASSISTANT = "assistant"
	ROLE_TOOL      = "tool"

	CONTENT_TEXT        = "text"
	CONTENT_IMAGE       = "image"
	CONTENT_TOOL_USE    = "tool_use"
	CONTENT_TOOL_RESULT = "tool_result"

	TOOL             = "tool"
	TOOL_FUNCTION    = "function"
	TOOL_CHOICE_AUTO = "auto"

	STOP_END_TURN   = "end_turn"
	STOP_MAX_TOKENS = "max_tokens"
	STOP_TOOL_USE   = "tool_use"
	STOP_ERROR      = "error"

	EVENT_MESSAGE_START       = "message_start"
	EVENT_MESSAGE_STOP        = "message_stop"
	EVENT_MESSAGE_DELTA       = "message_delta"
	EVENT_CONTENT_BLOCK_START = "content_block_start"
	EVENT_CONTENT_BLOCK_STOP  = "content_block_stop"
	EVENT_CONTENT_BLOCK_DELTA = "content_block_delta"
	EVENT_PING                = "ping"

	DELTA_TEXT       = "text_delta"
	DELTA_INPUT_JSON = "input_json_delta"

	MESSAGE = "message"

	FINISH_REASON_LENGTH        = "length"
	FINISH_REASON_STOP          = "stop"
	FINISH_REASON_TOOL_CALLS    = "tool_calls"
	FINISH_REASON_FUNCTION_CALL = "function_call"
)
