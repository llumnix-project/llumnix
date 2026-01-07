package tool_parser

import (
	"crypto/rand"
	"easgo/pkg/llm-gateway/protocol"
	"encoding/hex"

	"k8s.io/klog/v2"
)

type MatchResult int

const (
	MatchAll MatchResult = iota
	MatchPartial
	NoMatch
)

func dynamicMatch(chunk, target string) (MatchResult, int) {
	if len(target) == 0 {
		return MatchAll, 0
	}
	if len(chunk) == 0 {
		return NoMatch, -1
	}

	if len(chunk) >= len(target) {
		suffixStart := len(chunk) - len(target)
		if chunk[suffixStart:] == target {
			return MatchAll, suffixStart
		}
	}

	maxCheckLen := min(len(chunk), len(target))
	for l := maxCheckLen; l > 0; l-- {
		suffixStart := len(chunk) - l
		if chunk[suffixStart:] == target[:l] {
			return MatchPartial, suffixStart
		}
	}

	return NoMatch, -1
}

func generateCallID() string {
	bytes := make([]byte, 12)
	_, err := rand.Read(bytes)
	if err != nil {
		klog.Error("generate call id error: %v", err)
		return ""
	}
	return "chatcmpl-tool-" + hex.EncodeToString(bytes)
}

func createFuncName(n, t string, id int) protocol.ToolCall {
	return protocol.ToolCall{
		ID:       generateCallID(),
		Index:    &id,
		Type:     protocol.ToolType(t),
		Function: protocol.FunctionCall{Name: n, Arguments: ""},
	}
}

func appendFunArgs(calls []protocol.ToolCall, args, t string, id int) []protocol.ToolCall {
	if args == "" {
		return calls
	}
	call := protocol.ToolCall{
		Index:    &id,
		Type:     protocol.ToolType(t),
		Function: protocol.FunctionCall{Arguments: args},
	}
	calls = append(calls, call)
	return calls
}
