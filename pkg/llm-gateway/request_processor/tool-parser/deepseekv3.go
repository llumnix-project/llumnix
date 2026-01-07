package tool_parser

import (
	"easgo/pkg/llm-gateway/protocol"
	"easgo/pkg/llm-gateway/tokenizer"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"k8s.io/klog/v2"
)

const (
	CallsToolBeginStr = "<｜tool▁calls▁begin｜>"
	CallsToolEndStr   = "<｜tool▁calls▁end｜>"

	CallToolBeginStr = "<｜tool▁call▁begin｜>"
	CallToolEndStr   = "\n```<｜tool▁call▁end｜>"
)

var (
	// Pre-compile regex patterns for performance
	ToolCallPattern   = `(?s)<｜tool▁call▁begin｜>.*?<｜tool▁call▁end｜>`
	ToolCallExtractor = regexp.MustCompile(ToolCallPattern)

	FuncHeaderPattern   = "(?s)<｜tool▁call▁begin｜>(.*?)<｜tool▁sep｜>(.*?)\n```json\n"
	FuncHeaderExtractor = regexp.MustCompile(FuncHeaderPattern)

	FuncDetailPattern   = "(?s)<｜tool▁call▁begin｜>(.*?)<｜tool▁sep｜>(.*?)\n```json\n(.*?)\n```<｜tool▁call▁end｜>"
	FuncDetailExtractor = regexp.MustCompile(FuncDetailPattern)
)

// DeepSeekParser handles DeepSeek format for tool calls
// It uses Unicode tokens: `<｜tool▁calls▁begin｜><｜tool▁call▁begin｜>function<｜tool▁sep｜>{name}\n```json\n{args}\n```<｜tool▁call▁end｜><｜tool▁calls▁end｜>`
type DeepSeekParser struct {
	tokenizer tokenizer.Tokenizer
	// Regex patterns compiled once for performance
	toolCallExtractor   *regexp.Regexp
	funcHeaderExtractor *regexp.Regexp
	funcDetailExtractor *regexp.Regexp

	// State for streaming parsing
	buffer          string
	callsBegin      bool
	callsEnd        bool
	callToolStart   bool
	currentToolID   int
	currentFuncName string
	currentFuncType string
}

// NewDeepSeekParser creates a new DeepSeek parser with pre-compiled regex patterns
func NewDeepSeekParser() ToolParser {
	tk, _ := tokenizer.GetTokenizer()
	return &DeepSeekParser{
		tokenizer:           tk,
		toolCallExtractor:   ToolCallExtractor,
		funcHeaderExtractor: FuncHeaderExtractor,
		funcDetailExtractor: FuncDetailExtractor,
		currentToolID:       0,
	}
}

// ParseComplete parses complete tool calls from final output
// Returns (remaining_normal_text, tool_calls) tuple
func (p *DeepSeekParser) ParseComplete(text string) (*ParseResult, error) {
	if !strings.Contains(text, CallsToolBeginStr) || !strings.Contains(text, CallsToolEndStr) {
		klog.Warningf("deepseek parser: no tool calls found in text or incomplete markers")
		return &ParseResult{text, 0, nil}, nil
	}

	toolTokenLen := p.getTokenLen(CallsToolBeginStr)
	toolTokenLen += p.getTokenLen(CallsToolEndStr)

	// Find where tool calls begin
	idx := strings.Index(text, CallsToolBeginStr)
	normalText := text[:idx]

	// Extract and parse tool calls
	var tools []protocol.ToolCall
	matches := p.toolCallExtractor.FindAllString(text, -1)
	index := -1
	for _, match := range matches {
		index += 1
		toolTokenLen += p.getTokenLen(match)
		tool, err := p.parseToolCall(match, index)
		if err != nil {
			// Log warning but continue with other tool calls
			klog.Warningf("Failed to parse tool call: %v\n", err)
			continue
		}
		tools = append(tools, tool)
	}

	// If no tools were successfully parsed despite having markers, return entire text as fallback
	if len(tools) == 0 {
		return &ParseResult{normalText, 0, nil}, nil
	}

	return &ParseResult{normalText, toolTokenLen, tools}, nil
}

func (p *DeepSeekParser) getTokenLen(text string) uint64 {
	if p.tokenizer == nil {
		return 0
	}
	tokens, _ := p.tokenizer.Encode(text, false)
	return uint64(len(tokens))
}

// ParseStreaming parses tool calls from model output during streaming
func (p *DeepSeekParser) ParseStreaming(chunk string) (*ParseResult, error) {
	if p.callsEnd {
		klog.Warningf("deepseek parser: received chunk after tool calls end: %s", chunk)
		return &ParseResult{NormalText: chunk, Calls: nil}, nil
	}
	var (
		normalText   string
		funArgs      string
		calls        []protocol.ToolCall
		match        MatchResult
		index        int
		toolTokenLen uint64
	)

	p.buffer += chunk
	// fmt.Printf("deepseek parser buffer: ##%s##\n", p.buffer)
	if !p.callsBegin {
		match, index = dynamicMatch(p.buffer, CallsToolBeginStr)
		switch match {
		case MatchAll:
			normalText = p.buffer[:index]
			toolTokenLen = p.getTokenLen(CallsToolBeginStr)
			end := index + len(CallsToolBeginStr)
			p.buffer = p.buffer[end:]
			p.callsBegin = true
		case NoMatch:
			normalText = p.buffer
			p.buffer = ""
		case MatchPartial:
			// match partial <｜tool▁calls▁begin｜>, need to feed more chunks
		}
		goto RETURN
	}

	// try to match the tool call function header pattern
	if !p.callToolStart {
		// tool header: "<｜tool▁call▁begin｜>(.*?)<｜tool▁sep｜>(.*?)\n```json\n"
		matches := p.funcHeaderExtractor.FindStringSubmatch(p.buffer)
		if matches == nil {
			goto RETURN
		}

		p.currentFuncType = strings.TrimSpace(matches[1])
		p.currentFuncName = strings.TrimSpace(matches[2])
		if p.currentFuncName == "" {
			klog.Warningf("deepseekv3parser: empty function name in tool call: ##%s##", p.buffer)
			p.buffer = ""
			goto RETURN
		}
		if p.currentFuncType == "" {
			p.currentFuncType = "function"
		}

		// send tool name
		calls = append(calls, createFuncName(p.currentFuncName, p.currentFuncType, p.currentToolID))
		p.callToolStart = true
		// fmt.Printf("before buffer: ##%s##\n", p.buffer)
		// fmt.Printf("matched: ##%s##\n", matches[0])
		// ignore space char, like:  <｜tool▁call▁end｜>\n｜tool▁call▁begin｜
		index := strings.Index(p.buffer, CallToolBeginStr)
		offset := index + len(matches[0])
		p.buffer = p.buffer[offset:]
		toolTokenLen = p.getTokenLen(matches[0])
		// fmt.Printf("after buffer: ##%s##\n", p.buffer)
		goto RETURN
	}

	// extract function args until call_tool_end
	match, index = dynamicMatch(p.buffer, CallToolEndStr)
	switch match {
	case MatchAll:
		funArgs = p.buffer[:index]
		calls = appendFunArgs(calls, funArgs, p.currentFuncType, p.currentToolID)
		toolTokenLen = p.getTokenLen(funArgs)
		end := index + len(CallToolEndStr)
		p.buffer = p.buffer[end:]
		toolTokenLen += p.getTokenLen(CallToolEndStr)
		p.callToolStart = false
		p.currentToolID += 1
		goto END_TOOLS
	case NoMatch:
		funArgs = p.buffer
		calls = appendFunArgs(calls, funArgs, p.currentFuncType, p.currentToolID)
		toolTokenLen = p.getTokenLen(funArgs)
		p.buffer = ""
		goto RETURN
	case MatchPartial:
		// match partial <｜tool▁calls▁begin｜>, need to feed more chunks
		goto RETURN
	}

END_TOOLS:
	match, index = dynamicMatch(p.buffer, CallsToolEndStr)
	switch match {
	case MatchAll:
		end := index + len(CallsToolEndStr)
		toolTokenLen += p.getTokenLen(CallsToolEndStr)
		p.buffer = p.buffer[end:]
		if p.buffer != "" {
			klog.Warningf("deepseekv3parser: unexpected text after tool calls end: %s", p.buffer)
		}
		p.callsEnd = true
	case MatchPartial:
	case NoMatch:
	}

RETURN:
	return &ParseResult{NormalText: normalText, ToolTokensLen: toolTokenLen, Calls: calls}, nil
}

// parseToolCall parses a single tool call block
func (p *DeepSeekParser) parseToolCall(block string, idx int) (protocol.ToolCall, error) {
	matches := p.funcDetailExtractor.FindStringSubmatch(block)
	if matches == nil {
		return protocol.ToolCall{}, ParserError{Message: "Failed to match tool call pattern"}
	}

	// Get function type (should be "function")
	funcType := matches[1]
	if funcType != "function" {
		return protocol.ToolCall{}, ParserError{Message: fmt.Sprintf("Invalid function type: %s", funcType)}
	}

	// Get function name
	funcName := strings.TrimSpace(matches[2])
	if funcName == "" {
		return protocol.ToolCall{}, ParserError{Message: "Empty function name"}
	}

	// Get JSON arguments
	jsonArgs := strings.TrimSpace(matches[3])

	// Parse JSON arguments
	var value interface{}
	if err := json.Unmarshal([]byte(jsonArgs), &value); err != nil {
		return protocol.ToolCall{}, ParserError{Message: fmt.Sprintf("Invalid JSON: %v", err)}
	}

	// Create arguments object
	var args interface{}
	switch v := value.(type) {
	case map[string]interface{}:
		args = v
	default:
		// If not an object, wrap it
		args = map[string]interface{}{"value": v}
	}

	arguments, err := json.Marshal(args)
	if err != nil {
		return protocol.ToolCall{}, ParserError{Message: err.Error()}
	}

	return protocol.ToolCall{
		ID:   generateCallID(),
		Type: "function",
		Function: protocol.FunctionCall{
			Name:      funcName,
			Arguments: string(arguments),
		},
	}, nil
}
