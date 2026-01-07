package reasoning_parser

import (
	"fmt"
	"strings"
)

type ReasoningParser struct {
	thinkStartToken    string
	thinkEndToken      string
	previousText       string
	inReasoning        bool
	strippedThinkStart bool
	tokenIdx           int
	bufIdx             int
}

func NewDefaultReasoningParser() *ReasoningParser {
	return NewReasoningParser("<think>", "</think>")
}

func NewReasoningParser(thinkStartToken string, thinkEndToken string) *ReasoningParser {
	// maybe need more think token
	reasoningParser := &ReasoningParser{
		thinkStartToken:    thinkStartToken,
		thinkEndToken:      thinkEndToken,
		previousText:       "",
		inReasoning:        false,
		strippedThinkStart: false,
		tokenIdx:           0,
		bufIdx:             0,
	}
	return reasoningParser
}

func (rsnp *ReasoningParser) Reset() {
	rsnp.previousText = ""
	rsnp.inReasoning = false
	rsnp.strippedThinkStart = false
	rsnp.tokenIdx = 0
	rsnp.bufIdx = 0
}

func (rsnp *ReasoningParser) String() string {
	return fmt.Sprintf("thinkStartToken: %s, thinkEndToken: %s, previousText: %s, inReasoning: %t, strippedThinkStart: %t, tokenIdx: %d, bufIdx: %d",
		rsnp.thinkStartToken, rsnp.thinkEndToken, rsnp.previousText, rsnp.inReasoning, rsnp.strippedThinkStart, rsnp.tokenIdx, rsnp.bufIdx)
}

// ExtractReasoningContentStreaming implements streaming parsing for reasoning content
func (rsnp *ReasoningParser) ExtractReasoningContentStreaming(currentText string) (reasoningContent string, content string) {

	// this is maybe too complex to understand
	rsnp.previousText = rsnp.previousText + currentText

	// state: searching start
	if !rsnp.strippedThinkStart {
		for rsnp.bufIdx < len(rsnp.previousText) && rsnp.tokenIdx < len(rsnp.thinkStartToken) && rsnp.previousText[rsnp.bufIdx] == rsnp.thinkStartToken[rsnp.tokenIdx] {
			rsnp.bufIdx++
			rsnp.tokenIdx++
		}

		// no prefix of thinkStartToken found
		if rsnp.tokenIdx == 0 || (len(rsnp.previousText) > rsnp.bufIdx && rsnp.tokenIdx < len(rsnp.thinkStartToken)) {
			return "", currentText
		} else if rsnp.tokenIdx < len(rsnp.thinkStartToken) {
			// the current text is a prefix of the think token, keep buffering
			return "", ""
		} else {
			// found thinkStartToken
			rsnp.inReasoning = true
			rsnp.strippedThinkStart = true
			rsnp.tokenIdx = 0
			if len(rsnp.thinkStartToken) == len(rsnp.previousText) {
				// the current text is exactly the think token, reset the reasoning content
				rsnp.previousText = ""
				return "", ""
			} else {
				// the current text is longer than the think token, strip the think token
				reasoningContent = rsnp.previousText[rsnp.bufIdx:]
				rsnp.previousText = reasoningContent
			}
			rsnp.bufIdx = 0
		}
	}

	// in reasoning
	if rsnp.inReasoning && len(rsnp.previousText) > 0 {
		for rsnp.bufIdx < len(rsnp.previousText) && rsnp.previousText[rsnp.bufIdx] != rsnp.thinkEndToken[rsnp.tokenIdx] {
			rsnp.bufIdx++
		}
		// no prefix of thinkEndToken found
		if rsnp.bufIdx >= len(rsnp.previousText) {
			reasoningContent = rsnp.previousText
			content = ""
			rsnp.previousText = ""
			rsnp.bufIdx = 0
			return
		}

		for rsnp.previousText[rsnp.bufIdx] == rsnp.thinkEndToken[rsnp.tokenIdx] {
			rsnp.tokenIdx++
			rsnp.bufIdx++
			if rsnp.bufIdx >= len(rsnp.previousText) || rsnp.tokenIdx >= len(rsnp.thinkEndToken) {
				break
			}
		}
		// no prefix of thinkEndToken found
		if rsnp.tokenIdx == 0 {
			return currentText, ""
		} else if rsnp.tokenIdx == len(rsnp.thinkEndToken) {
			// found thinkEndToken
			reasoningContent = rsnp.previousText[:rsnp.bufIdx-len(rsnp.thinkEndToken)]
			content = rsnp.previousText[rsnp.bufIdx:]
			rsnp.previousText = ""
			rsnp.inReasoning = false
			return
		} else {
			// found prefix of thinkEndToken
			if rsnp.bufIdx < len(rsnp.previousText) {
				reasoningContent = rsnp.previousText
				rsnp.previousText = ""
				rsnp.bufIdx, rsnp.tokenIdx = 0, 0
				return reasoningContent, ""
			} else {
				return "", ""
			}
		}
	} else {
		// not in reasoning, just return the current text
		return "", currentText
	}
}

// ExtractReasoningContent extracts reasoning content and content from model output
// "<think>abc</think>xyz" -> reasoningContent: "abc", content: "xyz"
func (rsnp *ReasoningParser) ExtractReasoningContent(modelOutput string) (reasoningContent string, content string) {

	// <think>abcde -> reasoningContent: "abcde"
	if strings.HasPrefix(modelOutput, rsnp.thinkStartToken) && !strings.Contains(modelOutput, rsnp.thinkEndToken) {
		return modelOutput[len(rsnp.thinkStartToken):], ""
	}

	// abcde</think>fghijk -> reasoningContent: "abcde", content: "fghijk"
	if !strings.Contains(modelOutput, rsnp.thinkStartToken) && strings.Contains(modelOutput, rsnp.thinkEndToken) {
		subParts := strings.SplitN(modelOutput, rsnp.thinkEndToken, 2)
		reasoningContent = subParts[0]
		content = subParts[1]
		return
	}

	if !strings.Contains(modelOutput, rsnp.thinkStartToken) && !strings.Contains(modelOutput, rsnp.thinkEndToken) {
		return "", modelOutput
	}

	parts := strings.SplitN(modelOutput, rsnp.thinkStartToken, 2)
	if len(parts) < 2 {
		return "", modelOutput
	}
	modelOutput = parts[1]

	if !strings.Contains(modelOutput, rsnp.thinkEndToken) {
		return "", modelOutput
	}

	// extract content between <think> and </think>
	subParts := strings.SplitN(modelOutput, rsnp.thinkEndToken, 2)
	reasoning := subParts[0]
	var cont string
	if len(subParts) > 1 && len(subParts[1]) > 0 {
		cont = subParts[1]
	} else {
		cont = ""
	}
	return reasoning, cont
}
