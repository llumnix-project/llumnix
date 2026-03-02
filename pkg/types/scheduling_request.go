package types

import (
	"fmt"

	"llumnix/pkg/consts"
)

type SchedulingMode string

const (
	SchedulingModeNormal   SchedulingMode = "normal"
	SchedulingModePDBatch  SchedulingMode = "pd_batch"
	SchedulingModePDStaged SchedulingMode = "pd_staged"
)

type SchedulingResult []LLMInstance

func (sr SchedulingResult) String() string {
	var str string
	for _, instance := range sr {
		if len(str) > 0 {
			str += ","
		}
		str += instance.String()
	}
	return str
}

func (sr SchedulingResult) GetInstanceByInferType(inferType consts.InferType) *LLMInstance {
	for _, instance := range sr {
		if instance.InferType == inferType {
			return &instance
		}
	}
	return nil
}

type SchedulingRequest struct {
	// Scheduling information
	Id              string                 `json:"id"`
	Model           string                 `json:"model"`
	GatewayId       string                 `json:"gateway_id,omitempty"`
	SchedulingMode  SchedulingMode         `json:"scheduling_mode"`
	SchedulingStage consts.SchedulingStage `json:"scheduling_stage"`

	// LLM Prompt
	PromptNumTokens int      `json:"prompt_num_tokens,omitempty"`
	PromptTokenIds  []uint32 `json:"prompt_token_ids"`

	// scheduling result
	SchedulingResult SchedulingResult `json:"scheduling_result,omitempty"`
}

func (req *SchedulingRequest) String() string {
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

	// Add scheduling mode and scheduling stage
	if req.SchedulingMode != "" {
		if len(str) > 0 {
			str += " "
		}
		str += string(req.SchedulingMode)
	}
	if req.SchedulingStage != "" {
		if len(str) > 0 {
			str += "/"
		}
		str += string(req.SchedulingStage)
	}

	// Add prompt info (truncated if too long)
	if len(req.PromptTokenIds) > 0 {
		if len(str) > 0 {
			str += " "
		}
		str += fmt.Sprintf("tokens:%d", len(req.PromptTokenIds))
	}

	// Add scheduling result if present
	if req.SchedulingResult != nil {
		if len(str) > 0 {
			str += " "
		}
		str += fmt.Sprintf("result:[%s]", req.SchedulingResult.String())
	}

	return str
}
