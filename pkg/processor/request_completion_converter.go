package processor

import (
	"fmt"
	"llm-gateway/pkg/protocol"
	"llm-gateway/pkg/tokenizer"
	"llm-gateway/pkg/types"

	"k8s.io/klog/v2"
)

const defaultMaxTokens = 16384

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
	req.LLMRequest.InputTokensLen = uint64(processedReq.PromptTokens)
	defer processedReq.Free()
	return processedReq.TokenIDs, nil
}

func (rt *RequestCompletionConverter) generateMaxTokens(req *types.RequestContext, maxTokens *int) int {
	llmReq := req.LLMRequest
	max := 0
	if maxTokens != nil {
		max = *maxTokens
	}

	if max != 0 {
		llmReq.MaxTokensLimit = uint64(max)
	} else {
		// the default max_tokens in /completions api is 16k, in /chat/completions is none (get from model config), so we define it as 16k if user not set
		llmReq.MaxTokensLimit = defaultMaxTokens
	}
	if llmReq.MaxTokensLimit > defaultMaxTokens {
		return int(llmReq.MaxTokensLimit)
	} else {
		return defaultMaxTokens
	}
}

// PreProcess performs request transformation if needed.
func (rt *RequestCompletionConverter) PreProcess(req *types.RequestContext) error {
	oaiReq := req.LLMRequest
	oaiReq.BackendProtocol = protocol.OpenAICompletion
	switch oaiReq.Protocol {
	case protocol.OpenAICompletion:
		oaiReq.CompletionRequest.Stream = true
		oaiReq.CompletionRequest.Id = req.Id
		// only do tokenization for completions request with prompt as string or []string
		prompt, ok := oaiReq.CompletionRequest.Prompt.GetString()
		if ok {
			ids, err := rt.TokenizerEncode(prompt, true)
			if err != nil {
				klog.Warningf("[%s] Tokenize prompt to ids failed: %v, prompt: %s", req.Id, err, prompt)
				return fmt.Errorf("Tokenize prompt to ids failed")
			}
			oaiReq.CompletionRequest.Prompt.SetValue(ids)
			return nil
		}

		prompts, ok := oaiReq.CompletionRequest.Prompt.GetStringSlice()
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
			oaiReq.CompletionRequest.Prompt.SetValue(allIds)
			return nil
		}
		klog.Warningf("[%s] Unsupported prompt type in completion request: %v", req.Id, oaiReq.CompletionRequest.Prompt)
		// nothing todo when request with prompt as []int or [][]int
		return nil
	case protocol.OpenAIChatCompletion:
		tokenIds, err := rt.applyTokenizerTemplate(req)
		if err != nil {
			klog.Warningf("[%s] apply tokenizer template failed: %v, request: %v", req.Id, err, oaiReq.RawData)
			return fmt.Errorf("Failed to apply tokenizer template")
		}
		chatCompletions := oaiReq.ChatCompletionRequest
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
		oaiReq.CompletionRequest = completionsRequest
		return nil
	default:
		klog.Warningf("[%s] Unsupported protocol: %v", req.Id, oaiReq.Protocol)
		return fmt.Errorf("Unsupported protocol")
	}
}
