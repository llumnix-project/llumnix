package anthropic

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"k8s.io/klog/v2"

	"llm-gateway/pkg/consts"
	"llm-gateway/pkg/gateway/protocol"
	"llm-gateway/pkg/gateway/protocol/anthropic"
)

func ConvertOpenAIResponseToAnthropic(openaiResp *protocol.ChatCompletionResponse, messageId string, originModel string) ([]byte, error) {
	if len(openaiResp.Choices) == 0 {
		return nil, fmt.Errorf("openai response has no choices")
	}
	choice := openaiResp.Choices[0]
	var contentBlocks []any

	if choice.Message.Content != "" {
		contentBlocks = append(contentBlocks, anthropic.ContentBlockText{
			Type: consts.CONTENT_TEXT,
			Text: choice.Message.Content,
		})
	}

	for _, toolCall := range choice.Message.ToolCalls {
		if toolCall.Type != consts.TOOL_FUNCTION {
			continue
		}
		contentBlocks = append(contentBlocks, anthropic.ContentBlockToolUse{
			Type:  consts.CONTENT_TOOL_USE,
			Id:    toolCall.ID,
			Name:  toolCall.Function.Name,
			Input: toolCall.Function.Arguments,
		})
	}
	if len(contentBlocks) == 0 {
		contentBlocks = append(contentBlocks, anthropic.ContentBlockText{
			Type: consts.CONTENT_TEXT,
			Text: "",
		})
	}
	stopReason := convertFinalStopReason(string(choice.FinishReason))
	var usage anthropic.Usage
	convertUsage(&usage, openaiResp.Usage)
	response := anthropic.Response{
		ID:           messageId,
		Type:         consts.MESSAGE,
		Role:         consts.ROLE_ASSISTANT,
		Model:        originModel,
		Content:      contentBlocks,
		StopReason:   &stopReason,
		StopSequence: nil,
		Usage:        &usage,
	}
	data, err := json.Marshal(response)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal anthropic response: %v", err)
	}
	return data, nil
}

func convertFinalStopReason(finishReason string) string {
	switch finishReason {
	case consts.FINISH_REASON_LENGTH:
		return consts.STOP_MAX_TOKENS
	case consts.FINISH_REASON_TOOL_CALLS, consts.FINISH_REASON_FUNCTION_CALL:
		return consts.STOP_TOOL_USE
	default:
		return consts.STOP_END_TURN
	}
}

func convertToolCall(openaiToolCall *protocol.ToolCall, buffer *anthropic.StreamingResponseBuffer) string {
	var result string

	if openaiToolCall.Index == nil {
		klog.Error("Tool call index is nil")
		return ""
	}

	toolCallIndex := strconv.Itoa(*(openaiToolCall.Index))
	if buffer.ToolCalls[toolCallIndex] == nil {
		buffer.ToolCalls[toolCallIndex] = &anthropic.ToolCall{}
	}
	anthropicToolCall := buffer.ToolCalls[toolCallIndex]

	if id := openaiToolCall.ID; id != "" {
		anthropicToolCall.Id = id
	}

	if name := openaiToolCall.Function.Name; name != "" {
		anthropicToolCall.Name = name
	}

	if anthropicToolCall.Started == false &&
		anthropicToolCall.Id != "" &&
		anthropicToolCall.Name != "" {

		buffer.ToolCounter++
		anthropicToolCall.Started = true
		anthropicToolCall.Index = buffer.ToolCounter

		result += fmt.Sprintf("event: %s\ndata: %s\n\n",
			consts.EVENT_CONTENT_BLOCK_START,
			getEventContentBlockToolUseStartData(anthropicToolCall.Index, anthropicToolCall.Id, anthropicToolCall.Name))
	}

	// Append incoming arguments to buffer
	if anthropicToolCall.Started && openaiToolCall.Function.Arguments != "" {
		anthropicToolCall.ArgsBuffer += openaiToolCall.Function.Arguments

		// Send tool input delta event when JSON is complete and not yet sent
		if !anthropicToolCall.JSONSent && isArgumentsCompleted(anthropicToolCall.ArgsBuffer) {
			anthropicToolCall.JSONSent = true
			result += fmt.Sprintf("event: %s\ndata: %s\n\n",
				consts.EVENT_CONTENT_BLOCK_DELTA,
				getEventContentBlockDeltaInputJsonData(anthropicToolCall.Index, anthropicToolCall.ArgsBuffer))
		}
	}

	return result
}

func convertOpenAIStreamingChunk(openaiStreamResp *protocol.ChatCompletionStreamResponse, buffer *anthropic.StreamingResponseBuffer) string {
	var result string

	// convert anthropic usage
	if openaiStreamResp.Usage != nil {
		convertUsage(&buffer.Usage, openaiStreamResp.Usage)
	}

	if len(openaiStreamResp.Choices) > 0 {
		choice := openaiStreamResp.Choices[0]

		if choice.FinishReason != "" {
			buffer.FinalStopReason = convertFinalStopReason(string(choice.FinishReason))
		}

		if choice.Delta.Content != "" {
			result += fmt.Sprintf("event: %s\ndata: %s\n\n",
				consts.EVENT_CONTENT_BLOCK_DELTA,
				getEventContentBlockDeltaTextData(choice.Delta.Content))
		}

		if len(choice.Delta.ToolCalls) > 0 {
			for _, toolCall := range choice.Delta.ToolCalls {
				result += convertToolCall(&toolCall, buffer)
			}
		}
	}

	return result
}

// Stream event flow:
//  1. message_start: contains a Message object with empty content
//  2. Content blocks: each content block has the following events:
//     - content_block_start
//     - One or more content_block_delta events
//     - content_block_stop
//     Each content block has an index that corresponds to its index in the final Message content array
//  3. One or more message_delta events: indicating top-level changes to the final Message object
//  4. message_stop: final event
//
// Ping events:
//   - Event streams may also include any number of ping events.
//
// Error events:
//   - We may occasionally send errors in the event stream
func ConvertOpenAIStreamingResponseToAnthropic(openaiStreamResp *protocol.ChatCompletionStreamResponse, isFirst bool, isLast bool, messageId string, originalModel string, buffer *anthropic.StreamingResponseBuffer) ([]byte, error) {
	var result string

	// for the first chunk, send initial SSE events
	if isFirst {
		buffer.FinalStopReason = consts.STOP_END_TURN

		// send message_start event
		result += fmt.Sprintf("event: %s\ndata: %s\n\n",
			consts.EVENT_MESSAGE_START,
			getEventMessageStartData(messageId, originalModel))

		// content block index for the first text block
		result += fmt.Sprintf("event: %s\ndata: %s\n\n",
			consts.EVENT_CONTENT_BLOCK_START,
			getEventContentBlockTextStartData())

		// send a ping to keep the connection alive (Anthropic does this)
		result += fmt.Sprintf("event: %s\ndata: %s\n\n",
			consts.EVENT_PING,
			getEventPingData())
	}

	// check if this is the last chunk
	if isLast {
		result += fmt.Sprintf("event: %s\ndata: %s\n\n",
			consts.EVENT_CONTENT_BLOCK_STOP,
			getEventContentBlockStopData(0))

		for _, toolCall := range buffer.ToolCalls {
			result += fmt.Sprintf("event: %s\ndata: %s\n\n",
				consts.EVENT_CONTENT_BLOCK_STOP,
				getEventContentBlockStopData(toolCall.Index))
		}

		result += fmt.Sprintf("event: %s\ndata: %s\n\n",
			consts.EVENT_MESSAGE_DELTA,
			getEventMessageDeltaData(buffer.FinalStopReason, buffer.Usage))

		result += fmt.Sprintf("event: %s\ndata: %s\n\n",
			consts.EVENT_MESSAGE_STOP,
			getEventMessageStopData())

		result += fmt.Sprintf("data: %s\n\n", getEventDoneData())
		return []byte(result), nil
	}

	// parse the OpenAI stream response
	if openaiStreamResp == nil {
		return []byte(result), nil
	}
	result += convertOpenAIStreamingChunk(openaiStreamResp, buffer)

	// Return empty for other cases
	return []byte(result), nil
}

func getEventMessageStartData(messageId, originalModel string) string {
	event := anthropic.EventMessageStart{
		Type: consts.EVENT_MESSAGE_START,
		Message: anthropic.Response{
			ID:           messageId,
			Type:         consts.MESSAGE,
			Role:         consts.ROLE_ASSISTANT,
			Model:        originalModel,
			Content:      []any{},
			StopReason:   nil,
			StopSequence: nil,
			Usage: &anthropic.Usage{
				InputTokens:  0,
				OutputTokens: 0,
			},
		},
	}

	eventData, err := json.Marshal(event)
	if err != nil {
		klog.Errorf("failed to marshal event message start: %v", err)
		return ""
	}

	return string(eventData)
}

func getEventContentBlockTextStartData() string {
	event := anthropic.EventContentBlockStart{
		Type:  consts.EVENT_CONTENT_BLOCK_START,
		Index: 0,
		ContentBlock: anthropic.ContentBlockText{
			Type: consts.CONTENT_TEXT,
			Text: "",
		},
	}

	eventData, err := json.Marshal(event)
	if err != nil {
		klog.Errorf("failed to marshal event message start: %v", err)
		return ""
	}

	return string(eventData)
}

func getEventContentBlockToolUseStartData(index int, id, name string) string {
	event := anthropic.EventContentBlockStart{
		Type:  consts.EVENT_CONTENT_BLOCK_START,
		Index: index,
		ContentBlock: anthropic.ContentBlockToolUse{
			Type:  consts.CONTENT_TOOL_USE,
			Id:    id,
			Name:  name,
			Input: map[string]any{},
		},
	}

	eventData, err := json.Marshal(event)
	if err != nil {
		klog.Errorf("failed to marshal event message start: %v", err)
		return ""
	}

	return string(eventData)
}

func getEventMessageStopData() string {
	event := anthropic.EventMessageStop{
		Type: consts.EVENT_MESSAGE_STOP,
	}

	eventData, err := json.Marshal(event)
	if err != nil {
		klog.Errorf("failed to marshal event message start: %v", err)
		return ""
	}

	return string(eventData)
}

func getEventPingData() string {
	event := anthropic.EventPing{
		Type: consts.EVENT_PING,
	}

	eventData, err := json.Marshal(event)
	if err != nil {
		klog.Errorf("failed to marshal event message start: %v", err)
		return ""
	}

	return string(eventData)
}

func getEventContentBlockStopData(index int) string {
	event := anthropic.EventContentBlockStop{
		Type:  consts.EVENT_CONTENT_BLOCK_STOP,
		Index: index,
	}

	eventData, err := json.Marshal(event)
	if err != nil {
		klog.Errorf("failed to marshal event message start: %v", err)
		return ""
	}

	return string(eventData)
}

func getEventDoneData() string {
	return "[DONE]"
}

func getEventMessageDeltaData(stopReason string, usage anthropic.Usage) string {
	event := anthropic.EventMessageDelta{
		Type: consts.EVENT_MESSAGE_DELTA,
		Delta: anthropic.MessageDelta{
			StopReason:   stopReason,
			StopSequence: nil,
		},
		Usage: usage,
	}

	eventData, err := json.Marshal(event)
	if err != nil {
		klog.Errorf("failed to marshal event message start: %v", err)
		return ""
	}

	return string(eventData)
}

func getEventContentBlockDeltaTextData(content string) string {
	event := anthropic.EventContentBlockDelta{
		Type:  consts.EVENT_CONTENT_BLOCK_DELTA,
		Index: 0,
		Delta: anthropic.ContentBlockDeltaText{
			Type: consts.DELTA_TEXT,
			Text: content,
		},
	}

	eventData, err := json.Marshal(event)
	if err != nil {
		klog.Errorf("failed to marshal event message start: %v", err)
		return ""
	}

	return string(eventData)
}

func getEventContentBlockDeltaInputJsonData(index int, partialJson string) string {
	event := anthropic.EventContentBlockDelta{
		Type:  consts.EVENT_CONTENT_BLOCK_DELTA,
		Index: index,
		Delta: anthropic.ContentBlockDeltaInputJson{
			Type:        consts.DELTA_INPUT_JSON,
			PartialJson: partialJson,
		},
	}

	eventData, err := json.Marshal(event)
	if err != nil {
		klog.Errorf("failed to marshal event message start: %v", err)
		return ""
	}

	return string(eventData)
}

func convertUsage(dst *anthropic.Usage, src *protocol.Usage) {
	if src.PromptTokens > 0 {
		dst.InputTokens = int(src.PromptTokens)
	}

	if src.CompletionTokens > 0 {
		dst.OutputTokens = int(src.CompletionTokens)
	}

	if src.PromptTokensDetails != nil && src.PromptTokensDetails.CachedTokens > 0 {
		dst.CacheReadInputTokens = int(src.PromptTokensDetails.CachedTokens)
	}
}

func isLastMessage(message string) bool {
	return strings.Contains(message, "[DONE]")
}

func isArgumentsCompleted(args string) bool {
	// Try to parse the arguments as JSON to check if it's complete
	var temp interface{}
	err := json.Unmarshal([]byte(args), &temp)
	return err == nil
}
