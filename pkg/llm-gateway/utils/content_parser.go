package utils

import (
	"easgo/pkg/llm-gateway/consts"
	"easgo/pkg/llm-gateway/protocol"
	"encoding/json"

	"k8s.io/klog/v2"
)

// UnmarshalResponse unmarshal response data to concrete struct type
func UnmarshalResponse(p protocol.ProtocolType, data []byte) (interface{}, error) {
	switch p {
	case protocol.ProtocolCompletions:
		var completionResponse protocol.CompletionResponse
		err := json.Unmarshal(data, &completionResponse)
		if err != nil {
			klog.Errorf("unmarshal completion response failed: %v", string(data))
			return nil, err
		}
		return &completionResponse, nil
	case protocol.ProtocolChat, protocol.ProtocolTokenizedChat:
		var chatCompletionResponse protocol.ChatCompletionResponse
		err := json.Unmarshal(data, &chatCompletionResponse)
		if err != nil {
			klog.Errorf("unmarshal chat completion response failed: %v", string(data))
			return nil, err
		}
		return &chatCompletionResponse, nil
	}
	return nil, consts.ErrorUnSupportedProtocol
}

// GetCompletionResponse get completion response text
func GetCompletionText(response *protocol.CompletionResponse) string {
	text := ""
	for _, choice := range response.Choices {
		text += choice.Text
	}
	return text
}

// GetChatCompletionResponse get chat completion response text include reasoning and content
func GetChatCompletionText(response *protocol.ChatCompletionResponse) string {
	text := ""
	for _, choice := range response.Choices {
		if len(choice.Message.ReasoningContent) > 0 {
			text += choice.Message.ReasoningContent
		} else {
			text += choice.Message.Content
		}
	}
	return text
}

// GetResponseText get response text include chat and not chat
func GetResponseText(p protocol.ProtocolType, response interface{}) string {
	var text string
	switch p {
	case protocol.ProtocolCompletions:
		text = GetCompletionText(response.(*protocol.CompletionResponse))
	case protocol.ProtocolChat, protocol.ProtocolTokenizedChat:
		text = GetChatCompletionText(response.(*protocol.ChatCompletionResponse))
	default:
		panic("not support this protocol type")
	}
	return text
}

// GetResponseUsage get response usage
func GetResponseUsage(response interface{}) *protocol.Usage {
	var usage *protocol.Usage
	switch p := response.(type) {
	case *protocol.CompletionResponse:
		usage = p.Usage
	case *protocol.ChatCompletionResponse:
		usage = p.Usage
	default:
		panic("not support this protocol type")
	}
	return usage
}
