package types

import "fmt"

type ScheduleMode string

const (
	ScheduleModeNormal   ScheduleMode = "normal"
	ScheduleModePDBatch  ScheduleMode = "pd_batch"
	ScheduleModePDStaged ScheduleMode = "pd_staged"
)

type InferStage string

const (
	InferStagePrefill InferStage = "prefill"
	InferStageDecode  InferStage = "decode"
)

type ScheduledResult []*LLMWorker

func (sr ScheduledResult) String() string {
	var str string
	for _, worker := range sr {
		if len(str) > 0 {
			str += ","
		}
		str += worker.String()
	}
	return str
}

func (sr ScheduledResult) GetWorkerByRole(role InferRole) *LLMWorker {
	for _, worker := range sr {
		if worker.Role == role {
			return worker
		}
	}
	return nil
}

func (sr ScheduledResult) RemoveWorker(worker *LLMWorker) ScheduledResult {
	var result ScheduledResult
	for _, w := range sr {
		if w.Id() != worker.Id() {
			result = append(result, w)
		}
	}
	return result
}

type ScheduleRequest struct {
	// Schedule information
	Id           string       `json:"id"`
	Model        string       `json:"model"`
	GatewayId    string       `json:"gateway_id,omitempty"`
	ScheduleMode ScheduleMode `json:"schedule_mode"`
	InferStage   InferStage   `json:"infer_stage"`

	// Excluded instances for retry scheduling
	// Exclude scope controls whether to exclude only the instance or the entire host
	ExcludedInstances []string `json:"excluded_instances,omitempty"`
	RetryExcludeScope string   `json:"exclude_scope,omitempty"`

	// UseTokenIds indicates whether to use token IDs for scheduling.
	UseTokenIds bool `json:"use_token_ids,omitempty"`
	// LLM Prompt Tokens
	PromptNumTokens int      `json:"prompt_num_tokens,omitempty"`
	PromptTokenIds  []uint32 `json:"prompt_token_ids"`
	// LLM Prompt Text
	PromptTextLen int    `json:"prompt_text_len,omitempty"`
	PromptText    string `json:"prompt_text,omitempty"`

	// schedule result
	ScheduleResult ScheduledResult `json:"schedule_result,omitempty"`
}

// GetPromptLen returns the prompt length for scheduling and rate limiting.
// It returns PromptNumTokens when tokenizer is enabled (>0), otherwise returns PromptTextLen.
func (req *ScheduleRequest) GetPromptLen() int {
	if req.UseTokenIds {
		return req.PromptNumTokens
	} else {
		return req.PromptTextLen
	}
}

// GetPromptPrefix returns the prompt string for prefix matching.
// When PromptNumTokens > 0, it returns the string representation of PromptTokenIds without the last ']'.
// Otherwise, it returns PromptText directly.
func (req *ScheduleRequest) GetPromptPrefix() string {
	if req.UseTokenIds {
		if req.PromptNumTokens > 0 && len(req.PromptTokenIds) > 0 {
			// fmt.Sprintf("%v", slice) always outputs "[...]" format, remove the trailing ']'
			jsonData := fmt.Sprintf("%v", req.PromptTokenIds)
			return jsonData[:len(jsonData)-1]
		} else {
			return ""
		}
	} else {
		return req.PromptText
	}
}

func (req *ScheduleRequest) String() string {
	var str string

	// Add basic identification info
	if req.Id != "" {
		str += req.Id
	}
	if req.Model != "" {
		if len(str) > 0 {
			str += "|"
		}
		str += req.Model
	}
	if req.GatewayId != "" {
		if len(str) > 0 {
			str += "@"
		}
		str += req.GatewayId
	}

	// Add schedule mode and infer stage
	if req.ScheduleMode != "" {
		if len(str) > 0 {
			str += " "
		}
		str += string(req.ScheduleMode)
	}
	if req.InferStage != "" {
		if len(str) > 0 {
			str += "/"
		}
		str += string(req.InferStage)
	}

	// Add prompt info
	if len(str) > 0 {
		str += " "
	}
	str += fmt.Sprintf("use_token_ids:%v", req.UseTokenIds)
	if req.PromptNumTokens > 0 {
		if len(str) > 0 {
			str += " "
		}
		str += fmt.Sprintf("prompt_tokens_len:%d/%d", len(req.PromptTokenIds), req.PromptNumTokens)
	}
	if req.PromptTextLen > 0 {
		if len(str) > 0 {
			str += " "
		}
		str += fmt.Sprintf("text_len:%d/%d", len(req.PromptText), req.PromptTextLen)
	}

	// Add excluded instances for retry scheduling
	if len(req.ExcludedInstances) > 0 {
		if len(str) > 0 {
			str += " "
		}
		str += fmt.Sprintf("excluded:%v, exclude_scope:%s", req.ExcludedInstances, req.RetryExcludeScope)
	}

	// Add schedule result if present
	if req.ScheduleResult != nil {
		if len(str) > 0 {
			str += " "
		}
		str += fmt.Sprintf("result:[%s]", req.ScheduleResult.String())
	}

	return str
}
