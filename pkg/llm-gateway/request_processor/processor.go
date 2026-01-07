package request_processor

import (
	"fmt"
	"strings"
	"time"

	jsoniter "github.com/json-iterator/go"

	"github.com/valyala/fastjson"
	"k8s.io/klog/v2"

	"easgo/pkg/llm-gateway/consts"
	"easgo/pkg/llm-gateway/protocol"
	reasoning_parser "easgo/pkg/llm-gateway/request_processor/reasoning-parser"
	tool_parser "easgo/pkg/llm-gateway/request_processor/tool-parser"
	"easgo/pkg/llm-gateway/structs"
	"easgo/pkg/llm-gateway/tokenizer"
)

var json = jsoniter.ConfigCompatibleWithStandardLibrary

const (
	sseDataPrefix = "data: "
	sseNewline    = "\n\n"
	sseDone       = "data: [DONE]\n\n"
)

var (
	sseDataPrefixBytes = []byte(sseDataPrefix) // pre-allocated to avoid repeated allocations
	sseNewlineBytes    = []byte(sseNewline)
	sseDoneBytes       = []byte(sseDone)
)

type Timer struct {
	startTime time.Time
	name      string
}

func NewTimer(name string) *Timer {
	return &Timer{
		startTime: time.Now(),
		name:      name,
	}
}

func (t *Timer) LogElapsed() time.Duration {
	elapsed := time.Since(t.startTime)
	if elapsed > time.Millisecond {
		klog.V(3).Infof("[PERF] %s took %v", t.name, elapsed)
	}

	return elapsed
}

func (t *Timer) LogElapsedWithDetails(details string) time.Duration {
	elapsed := time.Since(t.startTime)
	if elapsed > time.Millisecond {
		klog.V(3).Infof("[PERF] %s (%s) took %v", t.name, details, elapsed)
	}
	return elapsed
}

type RequestProcessor struct {
	tokenizer       tokenizer.Tokenizer
	mode            string
	toolParser      tool_parser.ToolParser
	reasoningParser *reasoning_parser.ReasoningParser
}

const assistantRole = "assistant"
const firstChunkChoiceStr = `{"role": "assistant", "content": "", "reasoning_content": ""}`
const chatChunkObjectStr = "chat.completion.chunk"
const chatObjectStr = "chat.completion"

const chatPrefix = "chatcmpl"
const completionsIdPrefix = "cmpl"
const completionsURL = "/v1/completions"
const chatURL = "/v1/chat/completions"

const TokenizerModeFormatter = "formatter"

func NewRequestProcessor(tokenizerName, tokenizerPath, tokenizerMode, toolParserName string) (*RequestProcessor, error) {
	tk, err := tokenizer.GetTokenizer()
	if err != nil {
		return nil, fmt.Errorf("failed to load tokenizer from %s: %v", tokenizerPath, err)
	}
	var toolParser tool_parser.ToolParser
	if toolParserName != "" {
		toolParser = tool_parser.CreateToolParser(toolParserName)
	}

	return &RequestProcessor{
		tokenizer:       tk,
		mode:            tokenizerMode,
		reasoningParser: reasoning_parser.NewDefaultReasoningParser(),
		toolParser:      toolParser,
	}, nil
}

func (rp *RequestProcessor) encode(str string, addSepcialTokens bool) ([]uint32, error) {
	timer := NewTimer("encode")
	defer timer.LogElapsedWithDetails(fmt.Sprintf("len: %d", len(str)))

	return rp.tokenizer.Encode(str, addSepcialTokens)
}

func (rp *RequestProcessor) decode(ids []uint32, skipSpecialTokens bool) string {
	timer := NewTimer("decode")
	defer timer.LogElapsedWithDetails(fmt.Sprintf("len: %d", len(ids)))

	return rp.tokenizer.Decode(ids, skipSpecialTokens)
}

func (rp *RequestProcessor) applyChatTemplate(messages, tools, params string) (string, error) {
	timer := NewTimer("applyChatTemplate")
	defer timer.LogElapsedWithDetails(fmt.Sprintf("len: %d", len(messages)))

	// TODO: support tools
	messagesPrompt, err := rp.tokenizer.ApplyChatTemplate(messages, tools, params)
	if err != nil {
		return "", err
	}
	return messagesPrompt, nil
}

func (rp *RequestProcessor) applyChatTemplateAndEncode(messages, tools, params string) ([]uint32, error) {
	timer := NewTimer("applyChatTemplateAndEncode")
	defer timer.LogElapsed()

	messagesPrompt, err := rp.applyChatTemplate(messages, tools, params)
	if err != nil {
		return nil, err
	}
	klog.V(4).Info("apply chat template prompt: ", messagesPrompt)
	ids, _ := rp.encode(messagesPrompt, false)
	return ids, nil
}

func (rp *RequestProcessor) getTokenLen(s string) uint64 {
	if len(s) == 0 {
		return 0
	}
	ids, err := rp.encode(s, false)
	if err != nil {
		klog.Warningf("failed to encode string %s, error: %v", s, err)
		return 0
	}
	return uint64(len(ids))
}

func (rp *RequestProcessor) getFirstNTokensContent(s string, n uint64) (content string, length uint64) {
	if n == 0 {
		return "", n
	}

	ids, err := rp.encode(s, false)
	if err != nil {
		klog.Warningf("failed to encode string %s, error: %v", s, err)
		return "", n
	}
	length = uint64(len(ids))

	if length < n {
		return s, length
	}

	ids = ids[:n]
	content = rp.decode(ids, false)
	return
}

func (rp *RequestProcessor) IsApplicable(req *structs.Request) bool {
	return (req.Protocol == protocol.ProtocolTokenizedChat || rp.mode == TokenizerModeFormatter)
}

func (rp *RequestProcessor) Type() string {
	return consts.TokenizerProcessor
}

func (rp *RequestProcessor) tokenizePromptToTokenIds(req *structs.Request) ([]uint32, error) {
	if req.Prompt == nil || len(req.Prompt) == 0 {
		return nil, nil
	}

	ids, err := rp.encode(string(req.Prompt), true)
	if err != nil {
		klog.Warningf("req %v failed to encode prompt to tokens, err: %v", req.Id, err)
		return nil, fmt.Errorf("req %v failed to encode prompt to tokens", req.Id)
	}
	req.InputTokensLen = uint64(len(ids))
	return ids, nil
}

func (rp *RequestProcessor) ReWriteMaxTokens(req *structs.Request) error {
	if req.ReqObj.Get("max_tokens").Exists() {
		req.MaxTokensLimit = req.ReqObj.GetUint64("max_tokens")
	} else {
		// the default max_tokens in /completions api is 16, in /chat/completions is none (get from model config), so we define it as 16k if user not set
		req.MaxTokensLimit = 16384
	}
	if req.MaxTokensLimit > 16384 {
		req.ReqObj.Set("max_tokens", fastjson.MustParse(fmt.Sprintf("%d", req.MaxTokensLimit)))
	} else {
		req.ReqObj.Set("max_tokens", fastjson.MustParse("16384"))
	}
	req.ReqObj.Set("stream", fastjson.MustParse("true"))
	req.Data = req.ReqObj.MarshalTo(nil)
	klog.V(3).Infof("req %v rewrite max_tokens, req is %s", req.Id, string(req.Data))
	return nil
}

func (rp *RequestProcessor) tokenizeChatMessages(req *structs.Request) ([]uint32, error) {

	messages := req.ReqObj.Get("messages")
	if !messages.Exists() || messages.String() == "" {
		return nil, fmt.Errorf("req %v failed to get request", req.Id)
	}
	toolsStr := ""
	tools := req.ReqObj.Get("tools")
	if tools.Exists() {
		toolsStr = tools.String()
	}

	params := make(map[string]interface{})
	if reasoningEffort := req.ReqObj.Get("reasoning_effort"); reasoningEffort != nil {
		params["reasoning_effort"] = reasoningEffort.String()
	}

	// for Thinking & Non-Thinking Modes like DeepSeek-V3.1 or Qwen3,
	// there may be chat_template_kwargs in request like: {"chat_template_kwargs": {"thinking": False}} in ds31
	// or {"chat_template_kwargs": {"enable_thinking": False}} in qwen3
	// see: https://qwen.readthedocs.io/en/latest/deployment/vllm.html#thinking-non-thinking-modes
	// or: https://docs.vllm.ai/projects/recipes/en/latest/DeepSeek/DeepSeek-V3_1.html#openai-client-example
	if chatTemplateKwargs := req.ReqObj.Get("chat_template_kwargs"); chatTemplateKwargs != nil {
		chatTemplateKwargs.GetObject().Visit(func(key []byte, val *fastjson.Value) {
			switch val.Type() {
			case fastjson.TypeObject, fastjson.TypeArray:
				params[string(key)] = val.String()
			case fastjson.TypeString:
				params[string(key)] = val.String()
			case fastjson.TypeNumber:
				if v, err := val.Float64(); err == nil {
					params[string(key)] = v
				} else if v, err := val.Int64(); err == nil {
					params[string(key)] = v
				} else {
					params[string(key)] = val.String()
				}
			case fastjson.TypeTrue:
				params[string(key)] = true
			case fastjson.TypeFalse:
				params[string(key)] = false
			case fastjson.TypeNull:
				params[string(key)] = nil
			default:
				params[string(key)] = val.String()
			}
		})
	}
	paramsStr := ""
	if len(params) > 0 {
		if paramsBytes, err := json.Marshal(params); err == nil {
			paramsStr = string(paramsBytes)
		} else {
			klog.Warningf("req %v failed to marshal params, err: %v", req.Id, err)
		}
	}

	ids, err := rp.applyChatTemplateAndEncode(messages.String(), toolsStr, paramsStr)
	if err != nil {
		klog.Warningf("req %v failed to encode messages, err: %v, req: %v", req.Id, err, string(req.RawData))
		return nil, err
	}
	req.InputTokensLen = uint64(len(ids))

	return ids, nil
}

func (rp *RequestProcessor) TokenizeToCmplRequest(req *structs.Request) error {

	if req.Req.URL.Path == completionsURL {
		// preprocess: tokenize completions request prompt to ids
		ids, err := rp.tokenizePromptToTokenIds(req)
		if err != nil {
			return err
		}

		arena := fastjson.Arena{}
		arrayValue := arena.NewArray()
		for i, tokenId := range ids {
			arrayValue.SetArrayItem(i, arena.NewNumberInt(int(tokenId)))
		}
		req.ReqObj.Set("prompt", arrayValue)

		if req.InferStream { // enable token usage for stream request
			options, _ := fastjson.Parse(`{"include_usage": true, "continuous_usage_stats": true}`)
			req.ReqObj.Set("stream_options", options)
		}
		req.Data = req.ReqObj.MarshalTo(nil)
		klog.V(4).Infof("req %v encode prompt to tokens, req is %s", req.Id, string(req.Data))

	} else if req.Req.URL.Path == chatURL {
		// preprocess: chat/completions request → completions request
		ids, err := rp.tokenizeChatMessages(req)
		if err != nil {
			return err
		}

		arena := fastjson.Arena{}
		arrayValue := arena.NewArray()
		for i, tokenId := range ids {
			arrayValue.SetArrayItem(i, arena.NewNumberInt(int(tokenId)))
		}

		req.Req.URL.Path = "/v1/completions"
		req.Protocol = protocol.ProtocolTokenizedChat

		req.ReqObj.Set("prompt", arrayValue)
		req.ReqObj.Del("messages")
		req.ReqObj.Set("echo", fastjson.MustParse("false"))
		req.ReqObj.Set("n", fastjson.MustParse("1"))
		req.InferStream = true
		rp.ReWriteMaxTokens(req)
		klog.V(3).Infof("req %v encode messages to tokens, req is %s", req.Id, string(req.Data))

	}
	return nil
}

// Preprocess the request, encode the prompt or messages to tokens
// and set the req.ReqObj with the encoded prompt
func (rp *RequestProcessor) Preprocess(req *structs.Request) error {
	timer := NewTimer("Preprocess")
	defer func() {
		req.PreprocessCost = timer.LogElapsedWithDetails(fmt.Sprintf("reqId: %s, renLen: %d", req.Id, len(req.Data)))
	}()

	if req.Req == nil || req.Req.URL == nil || req.ReqObj == nil {
		return nil
	}

	if rp.mode == TokenizerModeFormatter {
		if req.Req.URL.Path == chatURL {
			if _, err := rp.tokenizeChatMessages(req); err != nil {
				return err
			}
			return rp.ReWriteMaxTokens(req)
		} else if req.Req.URL.Path == completionsURL {
			if _, err := rp.tokenizePromptToTokenIds(req); err != nil {
				return err
			}
		}
	} else {
		return rp.TokenizeToCmplRequest(req)
	}

	return nil
}

func (rp *RequestProcessor) BufferStreamToResp(req *structs.Request, data []byte, isFirstChunk bool) ([]byte, error) {
	timer := NewTimer("BufferStreamToResp")
	defer func() {
		req.PostprocessCost += timer.LogElapsedWithDetails(fmt.Sprintf("reqId: %s, renLen: %d", req.Id, len(data)))
	}()

	if rp.mode == TokenizerModeFormatter {
		return rp.bufferChatStreamToResp(req, data, isFirstChunk)
	} else {
		return rp.bufferCmplStreamToChatResp(req, data, isFirstChunk)
	}
}
func (rp *RequestProcessor) PostProcessStream(req *structs.Request, data []byte, isFirstChunk bool) ([]byte, error) {
	timer := NewTimer("PostProcessStream")
	defer func() {
		req.PostprocessCost += timer.LogElapsedWithDetails(fmt.Sprintf("reqId: %s, renLen: %d", req.Id, len(data)))
	}()
	klog.V(5).Infof("PostProcessStream: %s", string(data))
	if rp.mode == TokenizerModeFormatter {
		return rp.postProcessToChatStream(req, data, isFirstChunk)
	} else {
		return rp.postProcessCmplToChatStream(req, data, isFirstChunk)
	}
}

func (rp *RequestProcessor) bufferChatStreamToResp(req *structs.Request, data []byte, isFirstChunk bool) ([]byte, error) {
	klog.V(3).Infof("BufferStreamToResp: %s", string(data))
	respStr := string(data)
	if strings.HasSuffix(respStr, "[DONE]") {
		// if the response is [DONE], we need to return the buffered response
		respData, err := json.Marshal(req.ChatCompletionResponse)
		if err != nil {
			klog.Errorf("failed to marshal chat stream response: %v", err)
			return data, err
		}
		return respData, nil
	}

	chatStreamResp := new(protocol.ChatCompletionStreamResponse)
	if err := json.Unmarshal([]byte(respStr), chatStreamResp); err != nil {
		klog.Warningf("unmarshal json error: %v, data: %s", err, respStr)
		return data, err
	}

	if isFirstChunk || req.ChatCompletionResponse == nil {
		req.ChatCompletionResponse = &protocol.ChatCompletionResponse{
			ID:                chatStreamResp.ID,
			Object:            chatStreamResp.Object,
			Created:           chatStreamResp.Created,
			Model:             chatStreamResp.Model,
			Usage:             chatStreamResp.Usage,
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
					// FinishReason: cmpStreamResp.Choices,
				},
			},
		}

	}

	if len(chatStreamResp.Choices) == 0 {
		return []byte{}, nil
	}

	aborted := false
	reasoningContent, content := "", ""
	for _, choice := range chatStreamResp.Choices {
		reasoningContent = choice.Delta.ReasoningContent
		content = choice.Delta.Content

		reasoningTokensLen := rp.getTokenLen(reasoningContent)
		req.ReasoningTokensLen += reasoningTokensLen
		capacity := req.MaxTokensLimit - (req.OutputTokensLen - req.ReasoningTokensLen)
		content, rawContentTokensLen := rp.getFirstNTokensContent(content, capacity)
		if rawContentTokensLen >= capacity {
			// reach max tokens limit, set finish reason to length
			choice.FinishReason = "length"
			req.OutputTokensLen += (reasoningTokensLen + capacity)
		} else {
			// if contentTokensLen < capacity, it means the content is not truncated, so we keep the finish reason from backend
			req.OutputTokensLen += (reasoningTokensLen + rawContentTokensLen)
		}

		aborted = (choice.FinishReason == protocol.FinishReasonAbort)

		req.ChatCompletionResponse.Choices[0].Message.Content += content
		req.ChatCompletionResponse.Choices[0].Message.ReasoningContent += reasoningContent

		req.ChatCompletionResponse.Choices[0].FinishReason = choice.FinishReason
		//if choice.StopReason != nil {
		//	req.ChatCompletionResponse.Choices[0].StopReason = choice.StopReason
		//}
	}

	// TODO: implement usage info in backend, because this is not accurate
	req.ChatCompletionResponse.Usage = &protocol.Usage{
		PromptTokens:     req.InputTokensLen,
		TotalTokens:      req.InputTokensLen + req.OutputTokensLen,
		CompletionTokens: req.OutputTokensLen,
		CompletionTokensDetails: &protocol.CompletionTokensDetails{
			ReasoningTokens: req.ReasoningTokensLen,
		},
	}

	var retErr error
	if aborted {
		retErr = consts.ErrorRequestAbortedByEngine
	}

	if req.OutputExceedMaxTokens() {
		// exceed max tokens limit, return response and stop
		respData, err := json.Marshal(req.ChatCompletionResponse)
		if err != nil {
			klog.Errorf("failed to marshal chat stream response: %v", err)
			return data, err
		}
		return respData, retErr
	} else {
		return []byte{}, retErr
	}
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

// PostProcess completions style response -> chat/completions style
func (rp *RequestProcessor) bufferCmplStreamToChatResp(req *structs.Request, data []byte, isFirstChunk bool) ([]byte, error) {

	respStr := string(data)
	klog.V(3).Infof("BufferStreamToResp: %s", respStr)
	if strings.HasSuffix(respStr, "[DONE]") {
		// if the response is [DONE], we need to return the buffered response
		respData, err := json.Marshal(req.ChatCompletionResponse)
		if err != nil {
			klog.Errorf("failed to marshal chat stream response: %v", err)
			return data, err
		}
		return respData, nil
	}

	cmpStreamResp := new(protocol.CompletionResponse)
	if err := json.Unmarshal([]byte(respStr), cmpStreamResp); err != nil {
		klog.Warningf("unmarshal json error: %v, data: %s", err, respStr)
		return data, err
	}

	if isFirstChunk || req.ChatCompletionResponse == nil {
		req.ChatCompletionResponse = &protocol.ChatCompletionResponse{
			ID:                strings.ReplaceAll(cmpStreamResp.ID, completionsIdPrefix, chatPrefix),
			Object:            chatObjectStr,
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
					// FinishReason: cmpStreamResp.Choices,
				},
			},
		}

	}

	if len(cmpStreamResp.Choices) == 0 {
		return []byte{}, nil
	}

	aborted := false
	reasoningContent, content := "", ""
	for _, choice := range cmpStreamResp.Choices {
		parseResult := &tool_parser.ParseResult{NormalText: "", Calls: nil}
		if choice.Text == "" {
			reasoningContent, content = "", ""
		} else {
			reasoningContent, content = rp.reasoningParser.ExtractReasoningContentStreaming(choice.Text)
			if rp.toolParser != nil && content != "" {
				klog.V(4).Infof("tool parser input: ##%s##", content)
				parseResult, _ = rp.toolParser.ParseStreaming(content)
				content = parseResult.NormalText
			}

			if reasoningContent == "" && content == "" && len(parseResult.Calls) == 0 {
				// buffering
				return []byte{}, nil // buffering, no content to return
			}
		}

		reasoningTokensLen := rp.getTokenLen(reasoningContent)
		req.ReasoningTokensLen += reasoningTokensLen
		capacity := req.MaxTokensLimit - (req.OutputTokensLen - req.ReasoningTokensLen)
		content, rawContentTokensLen := rp.getFirstNTokensContent(content, capacity)
		if rawContentTokensLen >= capacity {
			// reach max tokens limit, set finish reason to length
			choice.FinishReason = "length"
			req.OutputTokensLen += (reasoningTokensLen + capacity)
		} else {
			// if contentTokensLen < capacity, it means the content is not truncated, so we keep the finish reason from backend
			req.OutputTokensLen += (reasoningTokensLen + rawContentTokensLen)
			if parseResult != nil {
				req.OutputTokensLen += parseResult.ToolTokensLen
			}
		}

		klog.V(3).Infof("finish reason: %s", choice.FinishReason)
		aborted = (choice.FinishReason == protocol.FinishReasonAbort)

		req.ChatCompletionResponse.Choices[0].Message.Content += content
		req.ChatCompletionResponse.Choices[0].Message.ReasoningContent += reasoningContent
		if parseResult != nil && len(parseResult.Calls) > 0 {
			klog.V(4).Infof("after tool parser: ##%s##, func: %s, args: %s", content, parseResult.Calls[0].Function.Name, parseResult.Calls[0].Function.Arguments)
			toolCalls := &(req.ChatCompletionResponse.Choices[0].Message.ToolCalls)
			mergeToolCalls(toolCalls, parseResult.Calls)
		}
		req.ChatCompletionResponse.Choices[0].FinishReason = choice.FinishReason
		//if choice.StopReason != nil {
		//	req.ChatCompletionResponse.Choices[0].StopReason = choice.StopReason
		//}
	}

	// TODO: implement usage info in backend, because this is not accurate
	req.ChatCompletionResponse.Usage = &protocol.Usage{
		PromptTokens:     req.InputTokensLen,
		TotalTokens:      req.InputTokensLen + req.OutputTokensLen,
		CompletionTokens: req.OutputTokensLen,
		CompletionTokensDetails: &protocol.CompletionTokensDetails{
			ReasoningTokens: req.ReasoningTokensLen,
		},
	}

	var retError error
	if aborted {
		retError = consts.ErrorRequestAbortedByEngine
	}
	if req.OutputExceedMaxTokens() {
		// exceed max tokens limit, return response and stop
		respData, err := json.Marshal(req.ChatCompletionResponse)
		if err != nil {
			klog.Errorf("failed to marshal chat stream response: %v", err)
			return data, nil
		}
		return respData, retError
	} else {
		return []byte{}, retError
	}
}

// PostProcessStream completions style stream response -> chat/completions style
func (rp *RequestProcessor) postProcessToChatStream(req *structs.Request, data []byte, isFirstChunk bool) ([]byte, error) {

	respStr := string(data)
	klog.V(3).Infof("PostProcessStream: %s", respStr)
	if strings.HasSuffix(respStr, "[DONE]") {
		return []byte("data: " + respStr + "\n\n"), nil
	}

	rawChatStreamResp := new(protocol.ChatCompletionStreamResponse)
	if err := json.Unmarshal([]byte(respStr), rawChatStreamResp); err != nil {
		klog.Warningf("unmarshal json error: %v, data: %s", err, respStr)
		return []byte("data: " + respStr + "\n\n"), err
	}

	if len(rawChatStreamResp.Choices) == 0 {
		return []byte{}, nil
	}

	chatStreamResp := &protocol.ChatCompletionStreamResponse{
		ID:                rawChatStreamResp.ID,
		Object:            rawChatStreamResp.Object,
		Created:           rawChatStreamResp.Created,
		Model:             rawChatStreamResp.Model,
		Choices:           []protocol.ChatCompletionStreamChoice{},
		Usage:             rawChatStreamResp.Usage,
		SystemFingerprint: "fp", // TODO: maybe set fingerprint as instance
	}

	isLastResp := false
	aborted := false

	for _, choice := range rawChatStreamResp.Choices {
		reasoningContent := choice.Delta.ReasoningContent
		content := choice.Delta.Content

		reasoningTokensLen := rp.getTokenLen(reasoningContent)
		req.ReasoningTokensLen += reasoningTokensLen
		capacity := req.MaxTokensLimit - (req.OutputTokensLen - req.ReasoningTokensLen)
		contentInCapacity, rawContentTokensLen := rp.getFirstNTokensContent(content, capacity)
		if rawContentTokensLen >= capacity {
			// reach max tokens limit, set finish reason to length
			choice.FinishReason = "length"
			req.OutputTokensLen += (reasoningTokensLen + capacity)
		} else {
			// if contentTokensLen < capacity, it means the content is not truncated, so we keep the finish reason from backend
			req.OutputTokensLen += (reasoningTokensLen + rawContentTokensLen)
		}

		if choice.FinishReason != "" && choice.Delta.Content != "" {
			isLastResp = true
		}

		aborted = (choice.FinishReason == protocol.FinishReasonAbort)

		if choice.Delta.ToolCalls == nil {
			choice.Delta.ToolCalls = []protocol.ToolCall{}
		}
		chatStreamResp.Choices = append(chatStreamResp.Choices, protocol.ChatCompletionStreamChoice{
			Index:        choice.Index,
			FinishReason: choice.FinishReason,
			Delta: protocol.ChatCompletionStreamChoiceDelta{
				Role:             assistantRole,
				Content:          contentInCapacity,
				ToolCalls:        choice.Delta.ToolCalls,
				ReasoningContent: reasoningContent,
			},
		})
	}

	// TODO: implement usage info in backend, this maybe inaccurate
	chatStreamResp.Usage = &protocol.Usage{
		PromptTokens:     req.InputTokensLen,
		TotalTokens:      req.InputTokensLen + req.OutputTokensLen,
		CompletionTokens: req.OutputTokensLen,
		CompletionTokensDetails: &protocol.CompletionTokensDetails{
			ReasoningTokens: req.ReasoningTokensLen,
		},
	}

	lastRespData := []byte{}
	if isLastResp {
		lastStreamResp := &protocol.ChatCompletionStreamResponse{
			ID:                chatStreamResp.ID,
			Object:            chatStreamResp.Object,
			Created:           chatStreamResp.Created,
			Model:             chatStreamResp.Model,
			SystemFingerprint: chatStreamResp.SystemFingerprint,
			Choices: []protocol.ChatCompletionStreamChoice{
				{
					Index:        0,
					FinishReason: chatStreamResp.Choices[0].FinishReason,
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

		lastRespBytes, err := json.Marshal(lastStreamResp)
		if err != nil {
			klog.Errorf("failed to marshal chat stream response: %v", err)
			//return []byte("data: " + respStr + "\n\n")
		}

		lastRespData = append(lastRespData, sseDataPrefixBytes...)
		lastRespData = append(lastRespData, lastRespBytes...)
		lastRespData = append(lastRespData, sseNewlineBytes...)
	}

	respData, err := json.Marshal(chatStreamResp)
	if err != nil {
		klog.Errorf("failed to marshal chat stream response: %v", err)
		return []byte("data: " + respStr + "\n\n"), err
	}

	err = nil
	if aborted {
		err = consts.ErrorRequestAbortedByEngine
	}
	if req.OutputExceedMaxTokens() {
		// exceed max tokens limit, return [DONE] and stop
		resp := make([]byte, 0, len(respData)+len(sseDataPrefixBytes)+len(sseNewlineBytes)+len(sseDoneBytes)+len(lastRespData))
		resp = append(resp, sseDataPrefixBytes...)
		resp = append(resp, respData...)
		resp = append(resp, sseNewlineBytes...)
		resp = append(resp, lastRespData...)
		resp = append(resp, sseDoneBytes...)
		return resp, err
	} else {
		resp := make([]byte, 0, len(respData)+len(sseDataPrefixBytes)+len(sseNewlineBytes)+len(lastRespData))
		resp = append(resp, sseDataPrefixBytes...)
		resp = append(resp, respData...)
		resp = append(resp, sseNewlineBytes...)
		resp = append(resp, lastRespData...)
		return resp, err
	}
}

// PostProcessStream completions style stream response -> chat/completions style
func (rp *RequestProcessor) postProcessCmplToChatStream(req *structs.Request, data []byte, isFirstChunk bool) ([]byte, error) {

	respStr := string(data)
	klog.V(3).Infof("PostProcessStream: %s", respStr)
	if strings.HasSuffix(respStr, "[DONE]") {
		return []byte("data: " + respStr + "\n\n"), nil
	}

	cmpStreamResp := new(protocol.CompletionResponse)
	if err := json.Unmarshal([]byte(respStr), cmpStreamResp); err != nil {
		klog.Warningf("unmarshal json error: %v, data: %s", err, respStr)
		return []byte("data: " + respStr + "\n\n"), err
	}

	if len(cmpStreamResp.Choices) == 0 {
		return []byte{}, nil
	}

	chatStreamResp := &protocol.ChatCompletionStreamResponse{
		ID:                strings.ReplaceAll(cmpStreamResp.ID, completionsIdPrefix, chatPrefix),
		Object:            chatChunkObjectStr,
		Created:           cmpStreamResp.Created,
		Model:             cmpStreamResp.Model,
		Choices:           []protocol.ChatCompletionStreamChoice{},
		Usage:             cmpStreamResp.Usage,
		SystemFingerprint: "fp", // TODO: maybe set fingerprint as instance
	}

	isLastResp := false
	aborted := false

	for _, choice := range cmpStreamResp.Choices {
		parseResult := &tool_parser.ParseResult{NormalText: "", Calls: nil}
		reasoningContent, content := "", ""
		if choice.Text == "" {
			reasoningContent, content = "", ""
		} else {
			reasoningContent, content = rp.reasoningParser.ExtractReasoningContentStreaming(choice.Text)
			if rp.toolParser != nil && content != "" {
				parseResult, _ = rp.toolParser.ParseStreaming(content)
				content = parseResult.NormalText
			}
			if reasoningContent == "" && content == "" && len(parseResult.Calls) == 0 {
				// buffering
				return []byte{}, nil // buffering, no content to return
			}
		}

		reasoningTokensLen := rp.getTokenLen(reasoningContent)
		req.ReasoningTokensLen += reasoningTokensLen
		capacity := req.MaxTokensLimit - (req.OutputTokensLen - req.ReasoningTokensLen)
		contentInCapacity, rawContentTokensLen := rp.getFirstNTokensContent(content, capacity)
		if rawContentTokensLen >= capacity {
			// reach max tokens limit, set finish reason to length
			choice.FinishReason = "length"
			req.OutputTokensLen += (reasoningTokensLen + capacity)
		} else {
			// if contentTokensLen < capacity, it means the content is not truncated, so we keep the finish reason from backend
			req.OutputTokensLen += (reasoningTokensLen + rawContentTokensLen)
			if parseResult != nil {
				req.OutputTokensLen += parseResult.ToolTokensLen
			}
		}

		if choice.FinishReason != "" && choice.Text != "" {
			isLastResp = true
		}

		klog.V(3).Infof("finish reason: %s", choice.FinishReason)
		aborted = (choice.FinishReason == protocol.FinishReasonAbort)

		chatStreamResp.Choices = append(chatStreamResp.Choices, protocol.ChatCompletionStreamChoice{
			Index:        choice.Index,
			FinishReason: choice.FinishReason,
			Delta: protocol.ChatCompletionStreamChoiceDelta{
				Role:             assistantRole,
				Content:          contentInCapacity,
				ToolCalls:        parseResult.Calls,
				ReasoningContent: reasoningContent,
			},
		})
	}

	// TODO: implement usage info in backend, this maybe inaccurate
	chatStreamResp.Usage = &protocol.Usage{
		PromptTokens:     req.InputTokensLen,
		TotalTokens:      req.InputTokensLen + req.OutputTokensLen,
		CompletionTokens: req.OutputTokensLen,
		CompletionTokensDetails: &protocol.CompletionTokensDetails{
			ReasoningTokens: req.ReasoningTokensLen,
		},
	}

	lastRespData := []byte{}
	if isLastResp {
		lastStreamResp := &protocol.ChatCompletionStreamResponse{
			ID:                chatStreamResp.ID,
			Object:            chatStreamResp.Object,
			Created:           chatStreamResp.Created,
			Model:             chatStreamResp.Model,
			SystemFingerprint: chatStreamResp.SystemFingerprint,
			Choices: []protocol.ChatCompletionStreamChoice{
				{
					Index:        0,
					FinishReason: chatStreamResp.Choices[0].FinishReason,
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

		lastRespBytes, err := json.Marshal(lastStreamResp)
		if err != nil {
			klog.Errorf("failed to marshal chat stream response: %v", err)
			//return []byte("data: " + respStr + "\n\n")
		}

		lastRespData = append(lastRespData, sseDataPrefixBytes...)
		lastRespData = append(lastRespData, lastRespBytes...)
		lastRespData = append(lastRespData, sseNewlineBytes...)
	}

	respData, err := json.Marshal(chatStreamResp)
	if err != nil {
		klog.Errorf("failed to marshal chat stream response: %v", err)
		return []byte("data: " + respStr + "\n\n"), err
	}

	err = nil
	if aborted {
		err = consts.ErrorRequestAbortedByEngine
	}
	if req.OutputExceedMaxTokens() {
		// exceed max tokens limit, return [DONE] and stop
		resp := make([]byte, 0, len(respData)+len(sseDataPrefixBytes)+len(sseNewlineBytes)+len(sseDoneBytes)+len(lastRespData))
		resp = append(resp, sseDataPrefixBytes...)
		resp = append(resp, respData...)
		resp = append(resp, sseNewlineBytes...)
		resp = append(resp, lastRespData...)
		resp = append(resp, sseDoneBytes...)
		return resp, err
	} else {
		resp := make([]byte, 0, len(respData)+len(sseDataPrefixBytes)+len(sseNewlineBytes)+len(lastRespData))
		resp = append(resp, sseDataPrefixBytes...)
		resp = append(resp, respData...)
		resp = append(resp, sseNewlineBytes...)
		resp = append(resp, lastRespData...)
		return resp, err
	}
}

// legacy, not used, keep it for reference
// PostProcess completions style response -> chat/completions style
func (rp *RequestProcessor) PostProcess(req *structs.Request, data []byte) []byte {
	timer := NewTimer("PostProcess")
	defer func() {
		req.PostprocessCost = timer.LogElapsedWithDetails(fmt.Sprintf("reqId: %s, renLen: %d", req.Id, len(data)))
	}()

	cmpResp := new(protocol.CompletionResponse)
	if err := json.Unmarshal([]byte(data), cmpResp); err != nil {
		klog.Errorf("unmarshal json error: %v, data: %s", err, string(data))
		return data
	}

	chatResp := &protocol.ChatCompletionResponse{
		ID:                strings.ReplaceAll(cmpResp.ID, completionsIdPrefix, chatPrefix),
		Object:            chatObjectStr,
		Created:           cmpResp.Created,
		Model:             cmpResp.Model,
		Usage:             cmpResp.Usage,
		SystemFingerprint: "fp", // maybe set it later
	}

	for _, choice := range cmpResp.Choices {
		reasoningContent, content := rp.reasoningParser.ExtractReasoningContent(choice.Text)

		reasoningTokensLen := rp.getTokenLen(reasoningContent)
		contentTokensLen := rp.getTokenLen(content)
		req.ReasoningTokensLen = reasoningTokensLen
		req.OutputTokensLen = contentTokensLen + reasoningTokensLen
		chatResp.Choices = append(chatResp.Choices, protocol.ChatCompletionChoice{
			Index: choice.Index,
			Message: protocol.ChatCompletionMessage{
				Role:             assistantRole,
				Content:          content,
				ReasoningContent: reasoningContent,
				ToolCalls:        []protocol.ToolCall{},
			},
			FinishReason: choice.FinishReason,
		})
	}

	// TODO: implement usage info in backend, this maybe inaccurate
	chatResp.Usage = &protocol.Usage{
		PromptTokens:     req.InputTokensLen,
		TotalTokens:      req.InputTokensLen + req.OutputTokensLen,
		CompletionTokens: req.OutputTokensLen,
		CompletionTokensDetails: &protocol.CompletionTokensDetails{
			ReasoningTokens: req.ReasoningTokensLen,
		},
	}

	respData, err := json.Marshal(chatResp)
	if err != nil {
		klog.Errorf("failed to marshal chat response: %v", err)
		return data
	}
	return respData
}
