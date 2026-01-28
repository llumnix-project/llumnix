package utils

import (
	"fmt"
	"time"

	"k8s.io/klog/v2"

	"llumnix/pkg/llm-gateway/consts"
	"llumnix/pkg/llm-gateway/protocol"
	"llumnix/pkg/llm-gateway/types"
)

func LogAccess(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Printf("%s %s\n", time.Now().Format("2006-01-02 15:04:05.000"), msg)
}

var instanceType2InferModeMap = map[string]string{
	consts.LlumnixNeutralInstanceType: consts.NormalInferMode,
	consts.LlumnixPrefillInstanceType: consts.PrefillInferMode,
	consts.LlumnixDecodeInstanceType:  consts.DecodeInferMode,
}

func TransformInstanceType2InferMode(instanceType string) string {
	if mode, exists := instanceType2InferModeMap[instanceType]; exists {
		return mode
	}
	klog.Warningf("Unknown instance type: %s", instanceType)
	return ""
}

func GetResponseUsage(req *types.RequestContext) *protocol.Usage {
	var usage *protocol.Usage = nil
	switch req.LLMRequest.Protocol {
	case protocol.OpenAIChatCompletion:
		if req.LLMRequest.ClientStream {
			usage = req.LLMRequest.ChatCompletionStreamResponse.Usage
		} else {
			usage = req.LLMRequest.ChatCompletionResponse.Usage
		}
	case protocol.OpenAICompletion:
		usage = req.LLMRequest.CompletionResponse.Usage
	default:
		klog.Warningf("Unsupported protocol: %s", req.LLMRequest.Protocol)
		return nil
	}
	return usage
}

func GetChatCompletionStreamText(response *protocol.ChatCompletionStreamResponse) string {
	text := ""
	if response != nil && len(response.Choices) > 0 {
		choice := response.Choices[0]
		if len(choice.Delta.ReasoningContent) > 0 {
			text += choice.Delta.ReasoningContent
		} else {
			text += choice.Delta.Content
		}
	} else {
		klog.Warningf("Invalid chat completion response: %v", response)
	}
	return text
}

func GetChatCompletionText(response *protocol.ChatCompletionResponse) string {
	text := ""
	if response != nil && len(response.Choices) > 0 {
		choice := response.Choices[0]
		if len(choice.Message.ReasoningContent) > 0 {
			text += choice.Message.ReasoningContent
		} else {
			text += choice.Message.Content
		}
	} else {
		klog.Warningf("Invalid chat completion response: %v", response)
	}
	return text
}

func GetCompletionText(response *protocol.CompletionResponse) string {
	text := ""
	if response != nil && len(response.Choices) > 0 {
		choice := response.Choices[0]
		text += choice.Text
	} else {
		klog.Warningf("Invalid completion response: %v", response)
	}
	return text
}

func GetResponseText(req *types.RequestContext) string {
	var text string
	switch req.LLMRequest.Protocol {
	case protocol.OpenAIChatCompletion:
		if req.LLMRequest.ClientStream {
			text = GetChatCompletionStreamText(req.LLMRequest.ChatCompletionStreamResponse)
		} else {
			text = GetChatCompletionText(req.LLMRequest.ChatCompletionResponse)
		}
	case protocol.OpenAICompletion:
		text = GetCompletionText(req.LLMRequest.CompletionResponse)
	default:
		klog.Warningf("Unsupported protocol: %s", req.LLMRequest.Protocol)
		return ""
	}
	return text
}
