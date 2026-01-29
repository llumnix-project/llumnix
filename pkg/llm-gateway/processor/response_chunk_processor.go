package processor

import (
	"encoding/json"
	"fmt"
	"llumnix/cmd/llm-gateway/app/options"
	reasoning_parser "llumnix/pkg/llm-gateway/processor/reasoning-parser"
	"llumnix/pkg/llm-gateway/protocol"
	"llumnix/pkg/llm-gateway/tokenizer"
	"llumnix/pkg/llm-gateway/types"
	"strings"

	"github.com/sglang/sglang-go-grpc-sdk"
	"k8s.io/klog/v2"
)

const (
	chatPrefix          = "chatcmpl"
	completionsIdPrefix = "cmpl"

	chatChunkObjectString = "chat.completion.chunk"
	chatObjectString      = "chat.completion"

	assistantRole = "assistant"
)

type ResponseChunkProcessor struct {
	tokenizer *sglang.Tokenizer

	reasoningParser string
	toolParser      string
}

func NewResponseChunkProcessor(config *options.Config) *ResponseChunkProcessor {
	tk, err := tokenizer.GetTokenizer()
	if err != nil {
		klog.Errorf("Failed to get tokenizer: %v", err)
		return nil
	}
	klog.Infof("use reasoning parser: %s, tool parser: %s", config.ReasoningParser, config.ToolCallParser)
	return &ResponseChunkProcessor{
		tokenizer:       tk,
		reasoningParser: config.ReasoningParser,
		toolParser:      config.ToolCallParser,
	}
}

func (rp *ResponseChunkProcessor) Name() string {
	return "ResponseChunkProcessor"
}

func (rp *ResponseChunkProcessor) ChatCompletionStreamProcess(req *types.RequestContext, done bool) error {
	llmRequest := req.LLMRequest
	if done && llmRequest.LastChatStreamResp != nil {
		llmRequest.ChatCompletionStreamResponse = llmRequest.LastChatStreamResp
		return nil
	}

	cmpStreamResp := llmRequest.CompletionResponse
	if len(cmpStreamResp.Choices) == 0 {
		return nil
	}

	reasoningParser := llmRequest.ReasoningParser
	toolParser := llmRequest.ToolParser
	choice := cmpStreamResp.Choices[0]
	parseResult := &toolParseResult{}
	reasoningContent, content := "", ""
	stats := req.RequestStats
	stats.OutputTokensLen += getTokenLen(choice.Text)
	if choice.Text != "" {
		reasoningContent, content = reasoningParser.ParseStreamChunk(choice.Text)
		if toolParser != nil && content != "" {
			tools := getToolString(req)
			parseResultStr, err := toolParser.ParseStreamIncremental(content, tools)
			if err != nil {
				klog.Errorf("[%s] failed to parse tool stream: %v, content: %s, tools: %s", req.Id, err, content, tools)
				return fmt.Errorf("failed to parse tool")
			}
			parseResult, err = parseResultFromString(parseResultStr)
			if err != nil {
				klog.Errorf("[%s] failed to parse tool result: %v, result: %s", req.Id, err, parseResultStr)
				return fmt.Errorf("failed to parse tool")
			}
			content = parseResult.NormalText
		}
		if reasoningContent == "" && content == "" && len(parseResult.ToolCalls) == 0 {
			// buffering, Not set the output: req.LLMRequest.ChatCompletionResponse
			return nil
		}
	}

	// Reached this point, it means there must be output.
	// at the end, chat chatStreamResp will be set as the output result.
	chatStreamResp := &protocol.ChatCompletionStreamResponse{
		ID:                strings.ReplaceAll(cmpStreamResp.ID, completionsIdPrefix, chatPrefix),
		Object:            chatChunkObjectString,
		Created:           cmpStreamResp.Created,
		Model:             cmpStreamResp.Model,
		Choices:           []protocol.ChatCompletionStreamChoice{},
		Usage:             cmpStreamResp.Usage,
		SystemFingerprint: "fp", // TODO: maybe set fingerprint as instance
	}

	reasoningTokensLen := getTokenLen(reasoningContent)
	stats.ReasoningTokensLen += reasoningTokensLen
	if parseResult != nil && len(parseResult.ToolCalls) > 0 {
		stats.HasToolCalls = true
	}
	klog.V(3).Infof("[%s] ChatCompletionStreamProcess: output_len: %d, reasoning_content_len: %d, raw_content_len: %d, capacity: %d, max_len: %d",
		req.Id, stats.OutputTokensLen, stats.ReasoningTokensLen, stats.OutputTokensLen, stats.MaxTokensLimit)

	needSplitLastResp := false
	if choice.FinishReason != "" {
		if choice.Text != "" {
			needSplitLastResp = true
		} else if stats.HasToolCalls && choice.FinishReason == "stop" {
			choice.FinishReason = "tool_calls"
		}
	}

	klog.V(3).Infof("[%s] ChatCompletionStreamProcess: content: ##%s##, reasoningContent: ##%s##, finishReason: %s, needSplitLastResp: %v",
		req.Id, content, reasoningContent, choice.FinishReason, needSplitLastResp)

	// set the output choice
	chatStreamResp.Choices = []protocol.ChatCompletionStreamChoice{
		{
			Index:        choice.Index,
			FinishReason: choice.FinishReason,
			Delta: protocol.ChatCompletionStreamChoiceDelta{
				Role:             assistantRole,
				Content:          content,
				ToolCalls:        parseResult.ToolCalls,
				ReasoningContent: reasoningContent,
			},
		},
	}

	chatStreamResp.Usage = &protocol.Usage{
		PromptTokens:     stats.InputTokensLen,
		TotalTokens:      stats.InputTokensLen + stats.OutputTokensLen,
		CompletionTokens: stats.OutputTokensLen,
		CompletionTokensDetails: &protocol.CompletionTokensDetails{
			ReasoningTokens: stats.ReasoningTokensLen,
		},
	}

	// send one more response when stop (not in tool_calls)
	if needSplitLastResp {
		llmRequest.LastChatStreamResp = &protocol.ChatCompletionStreamResponse{
			ID:                chatStreamResp.ID,
			Object:            chatStreamResp.Object,
			Created:           chatStreamResp.Created,
			Model:             chatStreamResp.Model,
			SystemFingerprint: chatStreamResp.SystemFingerprint,
			Choices: []protocol.ChatCompletionStreamChoice{
				{
					Index:        0,
					FinishReason: choice.FinishReason,
					Delta: protocol.ChatCompletionStreamChoiceDelta{
						Content:          "",
						ReasoningContent: "",
						Role:             assistantRole,
						ToolCalls:        []protocol.ToolCall{},
					},
				},
			},
			Usage: chatStreamResp.Usage,
		}
		chatStreamResp.Choices[0].FinishReason = ""
	}

	llmRequest.ChatCompletionStreamResponse = chatStreamResp
	return nil
}

func (rp *ResponseChunkProcessor) ChatCompletionProcess(req *types.RequestContext, done bool) error {
	llmRequest := req.LLMRequest
	if done {
		llmRequest.ChatCompletionResponse = llmRequest.BufferChatResp
		return nil
	}

	cmpStreamResp := llmRequest.CompletionResponse
	if llmRequest.BufferChatResp == nil {
		llmRequest.BufferChatResp = &protocol.ChatCompletionResponse{
			ID:                strings.ReplaceAll(cmpStreamResp.ID, completionsIdPrefix, chatPrefix),
			Object:            chatObjectString,
			Created:           cmpStreamResp.Created,
			Model:             cmpStreamResp.Model,
			Usage:             cmpStreamResp.Usage,
			SystemFingerprint: "fp", // maybe set it later
			Choices: []protocol.ChatCompletionChoice{
				{
					Index: 0,
					Message: protocol.ChatCompletionMessage{
						Role:             assistantRole,
						Content:          "",
						ReasoningContent: "",
						ToolCalls:        []protocol.ToolCall{},
					},
				},
			},
		}
	}
	if len(cmpStreamResp.Choices) == 0 {
		return nil
	}

	reasoningParser := req.LLMRequest.ReasoningParser
	toolParser := req.LLMRequest.ToolParser
	stats := req.RequestStats
	choice := cmpStreamResp.Choices[0]
	parseResult := &toolParseResult{NormalText: "", ToolCalls: nil}
	reasoningContent, content := "", ""
	stats.OutputTokensLen += getTokenLen(choice.Text)
	if choice.Text != "" {
		reasoningContent, content = reasoningParser.ParseStreamChunk(choice.Text)
		if toolParser != nil && content != "" {
			tools := getToolString(req)
			parseResultStr, err := toolParser.ParseStreamIncremental(content, tools)
			if err != nil {
				klog.Errorf("failed to parse tool stream: %v, content: %s, tools: %s", err, content, tools)
				return fmt.Errorf("failed to parse tool")
			}
			parseResult, _ = parseResultFromString(parseResultStr)
			content = parseResult.NormalText
		}

		if reasoningContent == "" && content == "" && len(parseResult.ToolCalls) == 0 {
			// buffering
			return nil
		}
	}

	reasoningTokensLen := getTokenLen(reasoningContent)
	stats.ReasoningTokensLen += reasoningTokensLen

	if parseResult != nil && len(parseResult.ToolCalls) > 0 {
		choice.FinishReason = "tool_calls"
	}

	llmRequest.BufferChatResp.Choices[0].Message.Content += content
	llmRequest.BufferChatResp.Choices[0].Message.ReasoningContent += reasoningContent
	if parseResult != nil && len(parseResult.ToolCalls) > 0 {
		klog.V(4).Infof("after tool parser: ##%s##, func: %s, args: %s", content, parseResult.ToolCalls[0].Function.Name, parseResult.ToolCalls[0].Function.Arguments)
		toolCalls := &(llmRequest.BufferChatResp.Choices[0].Message.ToolCalls)
		mergeToolCalls(toolCalls, parseResult.ToolCalls)
	}
	llmRequest.BufferChatResp.Choices[0].FinishReason = choice.FinishReason

	llmRequest.BufferChatResp.Usage = &protocol.Usage{
		PromptTokens:     stats.InputTokensLen,
		TotalTokens:      stats.InputTokensLen + stats.OutputTokensLen,
		CompletionTokens: stats.OutputTokensLen,
		CompletionTokensDetails: &protocol.CompletionTokensDetails{
			ReasoningTokens: stats.ReasoningTokensLen,
		},
	}

	klog.V(3).Infof("[%s] ChatCompletionProcess: output_len: %d, reasoning_content_len: %d, raw_content_len: %d, max_len: %d",
		req.Id, stats.OutputTokensLen, stats.ReasoningTokensLen, stats.OutputTokensLen, stats.MaxTokensLimit)
	klog.V(3).Infof("[%s] ChatCompletionProcess: content: ##%s##, reasoningContent: ##%s##, finishReason: %s, response: %p",
		req.Id, content, reasoningContent, choice.FinishReason, llmRequest.BufferChatResp)

	return nil
}

func (rp *ResponseChunkProcessor) completionStreamProcess(req *types.RequestContext, done bool) error {
	cmplResp := req.LLMRequest.CompletionResponse
	if cmplResp == nil {
		return nil // done must be true
	}
	for _, choice := range cmplResp.Choices {
		if choice.Text != "" {
			tokens, err := rp.tokenizer.Encode(choice.Text, false)
			if err != nil {
				klog.Warningf("[%s] Tokenize completion choice text failed: %v, text: %s", req.Id, err, choice.Text)
				return nil
			}
			req.RequestStats.OutputTokensLen += uint64(len(tokens))
		}
	}
	return nil
}

func (rp *ResponseChunkProcessor) completionProcess(req *types.RequestContext, done bool) error {
	if done {
		req.LLMRequest.CompletionResponse = req.LLMRequest.BufferCompletionResponse
		return nil
	}

	klog.V(3).Infof("[%s] completionProcess: processing completion chunk", req.Id)

	if cmplResp := req.LLMRequest.CompletionResponse; cmplResp == nil {
		klog.Errorf("[%s] completion process: completion response is empty", req.Id)
		return fmt.Errorf("completion response is empty")
	}
	if len(req.LLMRequest.CompletionResponse.Choices) > 1 {
		klog.Errorf("[%s] completion response has more than one choice, not support now.", req.Id)
		return fmt.Errorf("completion response has more than one choice")
	}

	if req.LLMRequest.BufferCompletionResponse == nil {
		cmpStreamResp := req.LLMRequest.CompletionResponse
		req.LLMRequest.BufferCompletionResponse = &protocol.CompletionResponse{
			ID:      strings.ReplaceAll(cmpStreamResp.ID, completionsIdPrefix, chatPrefix),
			Object:  cmpStreamResp.Object,
			Created: cmpStreamResp.Created,
			Model:   cmpStreamResp.Model,
			Choices: []protocol.CompletionChoice{
				{
					Index:        0,
					Text:         "",
					FinishReason: "",
				},
			},
			Usage: cmpStreamResp.Usage,
		}
	}

	cmpStreamResp := req.LLMRequest.CompletionResponse
	for _, choice := range cmpStreamResp.Choices {
		if len(choice.Text) > 0 {
			tokens, err := rp.tokenizer.Encode(choice.Text, false)
			if err != nil {
				klog.Warningf("[%s] Tokenize completion choice text failed: %v, text: %s", req.Id, err, choice.Text)
			} else {
				req.RequestStats.OutputTokensLen += uint64(len(tokens))
			}
			req.LLMRequest.BufferCompletionResponse.Choices[0].Text += choice.Text
		}
		req.LLMRequest.BufferCompletionResponse.Choices[0].FinishReason = choice.FinishReason
	}
	req.LLMRequest.BufferCompletionResponse.Usage = cmpStreamResp.Usage

	req.LLMRequest.CompletionResponse = nil
	return nil
}

func (rp *ResponseChunkProcessor) trySetParser(req *types.RequestContext) error {
	if req.LLMRequest.ReasoningParser == nil {
		p, err := reasoning_parser.NewReasoningParser(rp.reasoningParser, true, false)
		if err != nil {
			klog.Errorf("[%s] Failed to create reasoning parser: %v", req.Id, err)
			return err
		}
		req.LLMRequest.ReasoningParser = p
	}
	if req.LLMRequest.ToolParser == nil {
		tp, err := sglang.NewToolParser(rp.toolParser)
		if err != nil {
			klog.Errorf("[%s] Failed to create tool parser: %v", req.Id, err)
			return err
		}
		req.LLMRequest.ToolParser = tp
	}
	return nil
}

func (rp *ResponseChunkProcessor) PostProcess(req *types.RequestContext, done bool) error {
	stream := req.LLMRequest.ClientStream
	p := req.LLMRequest.Protocol
	switch p {
	case protocol.OpenAIChatCompletion: // completion -> chat completion
		// Pre check, ensure completion response is unmarshaled
		cmpStreamResp := req.LLMRequest.CompletionResponse
		if cmpStreamResp == nil {
			klog.Errorf("[%s] chat completion stream process: completion response is empty", req.Id)
			return fmt.Errorf("chat completion response is empty")
		}
		if len(cmpStreamResp.Choices) > 1 {
			klog.Errorf("[%s] completion response has more than one choice, not support now.", req.Id)
			return fmt.Errorf("chat completion response has more than one choice")
		}

		if err := rp.trySetParser(req); err != nil {
			return err
		}

		if stream {
			return rp.ChatCompletionStreamProcess(req, done)
		} else {
			return rp.ChatCompletionProcess(req, done)
		}
	case protocol.OpenAICompletion:
		if stream {
			return rp.completionStreamProcess(req, done)
		} else {
			return rp.completionProcess(req, done)
		}
	default:
		klog.Warningf("Unknown protocol: %s", p)
		return fmt.Errorf("Unknown protocol")
	}
}

type toolParseResult struct {
	NormalText string              `json:"normal_text"`
	ToolCalls  []protocol.ToolCall `json:"tool_calls"`
}

func parseResultFromString(s string) (*toolParseResult, error) {
	var result toolParseResult
	err := json.Unmarshal([]byte(s), &result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func getTokenLen(s string) uint64 {
	if len(s) == 0 {
		return 0
	}
	tk, _ := tokenizer.GetTokenizer()
	ids, err := tk.Encode(s, false)
	if err != nil {
		klog.Warningf("failed to encode string %s, error: %v", s, err)
		return 0
	}
	return uint64(len(ids))
}

func getFirstNTokensContent(s string, n uint64) (content string, length uint64) {
	if n == 0 {
		return "", n
	}

	tk, _ := tokenizer.GetTokenizer()
	ids, err := tk.Encode(s, false)
	if err != nil {
		klog.Warningf("failed to encode string %s, error: %v", s, err)
		return "", n
	}
	length = uint64(len(ids))

	if length < n {
		return s, length
	}
	ids = ids[:n]
	content, err = tk.Decode(ids, false)
	if err != nil {
		klog.Warningf("failed to decode ids %v, error: %v", ids, err)
		return s, n
	}
	return
}

// get tool from ChatCompletionRequest and marshal it to string
func getToolString(req *types.RequestContext) string {
	chatCompReq := req.LLMRequest.ChatCompletionRequest
	if chatCompReq == nil {
		return ""
	}
	if len(chatCompReq.Tools) > 0 {
		tool, err := json.Marshal(chatCompReq.Tools)
		if err != nil {
			klog.Warningf("req %v failed to marshal tools, err: %v", req.Id, err)
			return ""
		} else {
			return string(tool)
		}
	}
	return ""
}

func mergeToolCalls(toolCalls *[]protocol.ToolCall, exts []protocol.ToolCall) {
	for _, ext := range exts {
		if ext.Function.Name != "" {
			*toolCalls = append(*toolCalls, ext)
		}
		if ext.Function.Arguments != "" && len(*toolCalls) > 0 {
			len := len(*toolCalls)
			(*toolCalls)[len-1].Function.Arguments += ext.Function.Arguments
		}
	}
}
