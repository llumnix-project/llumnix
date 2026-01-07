package tool_parser

import "easgo/pkg/llm-gateway/protocol"

// ParseResult represents the result of streaming parsing
type ParseResult struct {
	NormalText    string
	ToolTokensLen uint64
	Calls         []protocol.ToolCall
}

// ParserError represents parsing errors
type ParserError struct {
	Message string
}

func (e ParserError) Error() string {
	return e.Message
}
