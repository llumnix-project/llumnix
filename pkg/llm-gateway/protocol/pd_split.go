package protocol

type PDSplitDecodeBase struct {
	ID                string  `json:"id,omitempty"`
	ChannelID         string  `json:"channel_id,omitempty"`
	PrefilledTokenIds []int   `json:"prefilled_token_ids,omitempty"`
	FirstTokenID      int     `json:"first_token_id"`
	FirstLogProb      float64 `json:"first_logprob,omitempty"`
}

type PDSplitDecodeRequest[T any] struct {
	PDSplitDecodeBase
	Request T `json:",inline"`
}

type PDSplitCompletionsDecodeRequest = PDSplitDecodeRequest[CompletionRequest]
type PDSplitChatCompletionsDecodeRequest = PDSplitDecodeRequest[ChatCompletionRequest]

type PDSplitCompletionsDecodeResponse struct {
	CompletionResponse
}

// TODO: remove some redundant information
type PDSplitCompletionsPrefillData struct {
	Index             int            `json:"index"`
	PrefilledTokenIds []int          `json:"prefilled_token_ids"`
	FirstTokenID      int            `json:"first_token_id"`
	FirstText         string         `json:"first_text"`
	ChannelID         string         `json:"channel_id"`
	FirstLogProb      *float64       `json:"first_logprob,omitempty"`
	LogProbs          *LogprobResult `json:"logprobs"`
	FinishReason      FinishReason   `json:"finish_reason,omitempty"`
	StopReason        *string        `json:"stop_reason,omitempty"`
}

type PDSplitCompletionsPrefillResponse struct {
	ID      string                          `json:"id"`
	Object  string                          `json:"object"`
	Created int64                           `json:"created"`
	Model   string                          `json:"model"`
	Data    []PDSplitCompletionsPrefillData `json:"data"`
	Usage   *Usage                          `json:"usage"`
}
