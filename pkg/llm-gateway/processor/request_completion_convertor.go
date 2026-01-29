package processor

import (
	"fmt"
	"llumnix/pkg/llm-gateway/protocol"
	"llumnix/pkg/llm-gateway/tokenizer"
	"llumnix/pkg/llm-gateway/types"

	"k8s.io/klog/v2"
)

type RequestCompletionConverter struct{}

func NewRequestCompletionConverter() *RequestCompletionConverter {
	return &RequestCompletionConverter{}
}

func (rt *RequestCompletionConverter) Name() string {
	return "RequestCompletionConverter"
}

func (rt *RequestCompletionConverter) TokenizerEncode(prompt string, addSpecialTokens bool) ([]uint32, error) {
	tk, _ := tokenizer.GetTokenizer()
	return tk.Encode(prompt, addSpecialTokens)
}

func (rt *RequestCompletionConverter) applyTokenizerTemplate(req *types.RequestContext) ([]uint32, error) {
	tk, _ := tokenizer.GetTokenizer()
	processedReq, err := tk.PreProcessChatRequest(req.LLMRequest.RawData)
	if err != nil {
		klog.Warningf("PreProcess chat messages failed: %v", err)
		return nil, err
	}
	req.RequestStats.InputTokensLen = uint64(processedReq.PromptTokens)
	defer processedReq.Free()
	return processedReq.TokenIDs, nil
}

func (rt *RequestCompletionConverter) generateMaxTokens(req *types.RequestContext, maxTokens int) uint64 {
	stats := req.RequestStats
	if maxTokens != 0 {
		stats.MaxTokensLimit = uint64(maxTokens)
	} else {
		// the default max_tokens in /completions api is 16, in /chat/completions is none (get from model config), so we define it as model_max_len if user not set
		stats.MaxTokensLimit = tokenizer.GetModelMaxLen() - stats.InputTokensLen
	}
	return uint64(stats.MaxTokensLimit)
}

// PreProcess performs request transformation if needed.
func (rt *RequestCompletionConverter) PreProcess(req *types.RequestContext) error {
	switch req.LLMRequest.Protocol {
	case protocol.OpenAICompletion:
		req.LLMRequest.ClientStream = req.LLMRequest.CompletionRequest.Stream
		req.LLMRequest.CompletionRequest.Stream = true
		req.LLMRequest.CompletionRequest.Id = req.Id
		// only do tokenization for completions request with prompt as string or []string
		prompt, ok := req.LLMRequest.CompletionRequest.Prompt.GetString()
		if ok {
			ids, err := rt.TokenizerEncode(prompt, true)
			if err != nil {
				klog.Warningf("[%s] Tokenize prompt to ids failed: %v, prompt: %s", req.Id, err, prompt)
				return fmt.Errorf("Tokenize prompt to ids failed")
			}
			req.LLMRequest.CompletionRequest.Prompt.SetValue(ids)
			return nil
		}

		prompts, ok := req.LLMRequest.CompletionRequest.Prompt.GetStringSlice()
		if ok {
			var allIds [][]uint32
			for _, prompt := range prompts {
				ids, err := rt.TokenizerEncode(prompt, true)
				if err != nil {
					klog.Warningf("[%s] Tokenize prompt to ids failed: %v, prompt: %v", req.Id, err, prompts)
					return fmt.Errorf("Tokenize prompt to ids failed")
				}
				allIds = append(allIds, ids)
			}
			req.LLMRequest.CompletionRequest.Prompt.SetValue(allIds)
			return nil
		}
		klog.Warningf("[%s] Unsupported prompt type in completion request: %v", req.Id, req.LLMRequest.CompletionRequest.Prompt)
		// nothing todo when request with prompt as []int or [][]int
		return nil
	case protocol.OpenAIChatCompletion:
		req.LLMRequest.ClientStream = req.LLMRequest.ChatCompletionRequest.Stream
		tokenIds, err := rt.applyTokenizerTemplate(req)
		if err != nil {
			klog.Warningf("[%s] apply tokenizer template failed: %v, request: %v", req.Id, err, req.LLMRequest.RawData)
			return fmt.Errorf("Failed to apply tokenizer template")
		}
		chatCompletions := req.LLMRequest.ChatCompletionRequest
		completionsRequest := &protocol.CompletionRequest{
			Id:               req.Id,
			Model:            chatCompletions.Model,
			FrequencyPenalty: chatCompletions.PresencePenalty,
			N:                chatCompletions.N,
			PresencePenalty:  chatCompletions.PresencePenalty,
			Stop:             chatCompletions.Stop,
			Seed:             chatCompletions.Seed,
			Stream:           true,
			StreamOptions:    &protocol.StreamOptions{IncludeUsage: true, IncludeContinuousUsage: true},
			Temperature:      chatCompletions.Temperature,
			TopP:             chatCompletions.TopP,
			User:             chatCompletions.User,
		}
		maxTokens := rt.generateMaxTokens(req, chatCompletions.MaxTokens)
		completionsRequest.MaxTokens = &maxTokens
		completionsRequest.Prompt.SetValue(tokenIds)
		req.LLMRequest.CompletionRequest = completionsRequest
		return nil
	default:
		klog.Warningf("[%s] Unsupported protocol: %v", req.Id, req.LLMRequest.Protocol)
		return fmt.Errorf("Unsupported protocol")
	}
}
