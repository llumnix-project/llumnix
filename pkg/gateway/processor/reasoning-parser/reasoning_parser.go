package reasoning_parser

import (
	"fmt"
	"strings"
)

type ReasoningFormatDetector interface {
	ParseStreamingIncrement(currentText string) (reasoningContent string, content string)
	DetectAndParse(modelOutput string) (reasoningContent string, content string)
}

type BaseReasoningFormatDetector struct {
	thinkStartTag string
	thinkEndTag   string
	inReasoning   bool
	//If False, accumulates reasoning content until the end tag.
	//If True, streams reasoning content as it arrives.
	streamReasoning bool

	// inner status
	buffer             string
	strippedThinkStart bool
}

func NewBaseReasoningFormatDetector(thinkStartTag, thinkEndTag string, forceReasoning, streamReasoning bool) *BaseReasoningFormatDetector {
	reasoningParser := &BaseReasoningFormatDetector{
		thinkStartTag:   thinkStartTag,
		thinkEndTag:     thinkEndTag,
		inReasoning:     forceReasoning,
		streamReasoning: streamReasoning,

		buffer:             "",
		strippedThinkStart: false,
	}
	return reasoningParser
}

func (d *BaseReasoningFormatDetector) Reset() {
	d.buffer = ""
	d.inReasoning = false
	d.strippedThinkStart = false
}

func (d *BaseReasoningFormatDetector) Status() string {
	return fmt.Sprintf("thinkStartTag: %s, thinkEndTag: %s, previousText: %s, inReasoning: %t, strippedThinkStart: %t",
		d.thinkStartTag, d.thinkEndTag, d.buffer, d.inReasoning, d.strippedThinkStart)
}

func (d *BaseReasoningFormatDetector) DetectAndParse(modelOutput string) (reasoningContent string, content string) {
	inReasoning := d.inReasoning || strings.Contains(modelOutput, d.thinkStartTag)

	if !inReasoning {
		return "", modelOutput
	}

	processedText := strings.TrimSpace(strings.ReplaceAll(modelOutput, d.thinkStartTag, ""))

	splits := strings.SplitN(processedText, d.thinkEndTag, 2)
	reasoningContent = splits[0]
	if len(splits) > 1 {
		content = strings.TrimSpace(splits[1])
	} else {
		content = ""
	}

	return reasoningContent, content
}

func (d *BaseReasoningFormatDetector) ParseStreamingIncrement(newText string) (reasoningContent string, content string) {
	d.buffer += newText
	currentText := d.buffer

	tags := []string{d.thinkStartTag, d.thinkEndTag}
	// If the current text is a prefix of the think token, keep buffering
	for _, tag := range tags {
		if strings.HasPrefix(tag, currentText) && tag != currentText {
			return "", ""
		}
	}

	// Strip `<think>` token if present
	if !d.strippedThinkStart && strings.Contains(currentText, d.thinkStartTag) {
		currentText = strings.ReplaceAll(currentText, d.thinkStartTag, "")
		d.strippedThinkStart = true
		d.inReasoning = true
	}

	// Handle end of reasoning block
	if d.inReasoning && strings.Contains(currentText, d.thinkEndTag) {
		endIdx := strings.Index(currentText, d.thinkEndTag)
		reasoningContent = currentText[:endIdx]
		content = currentText[endIdx+len(d.thinkEndTag):]
		d.Reset()
		return reasoningContent, content
	}

	// Continue with reasoning content
	if d.inReasoning {
		if d.streamReasoning {
			d.buffer = ""
			return currentText, ""
		} else {
			return "", ""
		}
	}

	// If we're not in a reasoning block return as normal text
	if !d.inReasoning {
		d.buffer = ""
		return "", currentText
	}

	return "", ""

}

type DeepSeekR1Detector struct {
	*BaseReasoningFormatDetector
}

type Qwen3Detector struct {
	*BaseReasoningFormatDetector
}

type KimiDetector struct {
	*BaseReasoningFormatDetector
}

func NewDeepSeekR1Detector(streamReasoning bool) *DeepSeekR1Detector {
	return &DeepSeekR1Detector{NewBaseReasoningFormatDetector("<think>", "</think>", true, streamReasoning)}
}

func NewQwen3Detector(streamReasoning, forceReasoning bool) *Qwen3Detector {
	return &Qwen3Detector{NewBaseReasoningFormatDetector("<think>", "</think>", forceReasoning, streamReasoning)}
}

func NewKimiDetector(streamReasoning bool) *KimiDetector {
	return &KimiDetector{NewBaseReasoningFormatDetector(
		"◁think▷", "◁/think▷", false, streamReasoning)}
}

type ReasoningParser struct {
	detector ReasoningFormatDetector
}

func NewReasoningParser(reasoningParser string, streamReasoning, forceReasoning bool) (*ReasoningParser, error) {
	mt := strings.ToLower(reasoningParser)
	var det ReasoningFormatDetector

	switch mt {
	case "deepseek-r1", "kimi_k2", "step3", "deepseek":
		det = NewDeepSeekR1Detector(streamReasoning)
	case "qwen3", "qwen3-thinking", "deepseek-v3", "minimax", "glm45":
		det = NewQwen3Detector(streamReasoning, forceReasoning)
	case "kimi":
		det = NewKimiDetector(streamReasoning)
	case "":
		det = NewQwen3Detector(true, false)
	default:
		return nil, fmt.Errorf("Unsupported reasoning parser: %s", reasoningParser)
	}
	return &ReasoningParser{detector: det}, nil
}

func (r *ReasoningParser) ParseNonStream(modelOutput string) (reasoningContent string, content string) {
	return r.detector.DetectAndParse(modelOutput)
}

func (r *ReasoningParser) ParseStreamChunk(chunk string) (reasoningContent string, content string) {
	return r.detector.ParseStreamingIncrement(chunk)
}
