package types

import "fmt"

type ScheduleMode string

const (
	ScheduleModeNormal   ScheduleMode = "normal"
	ScheduleModePDBatch  ScheduleMode = "pd_batch"
	ScheduleModePDStaged ScheduleMode = "pd_staged"
)

type ScheduleStage string

const (
	ScheduleStagePrefill ScheduleStage = "prefill"
	ScheduleStageDecode  ScheduleStage = "decode"
)

type ScheduledResult []LLMInstance

func (sr ScheduledResult) String() string {
	var str string
	for _, instance := range sr {
		if len(str) > 0 {
			str += ","
		}
		str += instance.String()
	}
	return str
}

func (sr ScheduledResult) GetInstanceByRole(role InferRole) *LLMInstance {
	for _, instance := range sr {
		if instance.Role == role {
			return &instance
		}
	}
	return nil
}

type ScheduleRequest struct {
	// Schedule information
	Id            string        `json:"id"`
	Model         string        `json:"model"`
	GatewayId     string        `json:"gateway_id,omitempty"`
	ScheduleMode  ScheduleMode  `json:"schedule_mode"`
	ScheduleStage ScheduleStage `json:"schedule_stage"`

	// LLM Prompt
	PromptNumTokens int      `json:"prompt_num_tokens,omitempty"`
	PromptTokenIds  []uint32 `json:"prompt_token_ids"`

	// schedule result
	ScheduleResult ScheduledResult `json:"schedule_result,omitempty"`
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

	// Add schedule mode and schedule stage
	if req.ScheduleMode != "" {
		if len(str) > 0 {
			str += " "
		}
		str += string(req.ScheduleMode)
	}
	if req.ScheduleStage != "" {
		if len(str) > 0 {
			str += "/"
		}
		str += string(req.ScheduleStage)
	}

	// Add prompt info (truncated if too long)
	if len(req.PromptTokenIds) > 0 {
		if len(str) > 0 {
			str += " "
		}
		str += fmt.Sprintf("tokens:%d", len(req.PromptTokenIds))
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
