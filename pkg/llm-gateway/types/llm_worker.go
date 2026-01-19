package types

import (
	"fmt"
	"strings"
)

type InferRole string

const (
	InferRoleNormal  InferRole = "normal"
	InferRolePrefill InferRole = "prefill"
	InferRoleDecode  InferRole = "decode"

	InferRoleAll InferRole = "all" // Only for filtering purposes, will not appear in llm worker
)

func (r InferRole) String() string {
	switch r {
	case InferRoleNormal:
		return "normal"
	case InferRolePrefill:
		return "prefill"
	case InferRoleDecode:
		return "decode"
	case InferRoleAll:
		return "all"
	default:
		return "unknown"
	}
}

// LLMWorker represents a single LLM inference worker instance.
// It contains all necessary information to identify and connect to the worker.
type LLMWorker struct {
	// Version may be a timestamp indicating the create time or a version identifier
	Version int64 `json:"version"`
	// ID is a unique identifier for the worker
	ID string `json:"id"`
	// Model is the name of the model served by this worker
	Model string `json:"model"`
	// Role is the inference mode (prefill/decode/normal) of the worker
	Role InferRole `json:"role"`
	// Endpoint is the network address where the worker can be reached
	Endpoint Endpoint `json:"endpoint"`
	// AuxPort is the auxiliary port of the worker for PD disaggregation
	AuxPort int `json:"aux_port"`
	// DpRank is the data parallel rank of this worker (0-based)
	DPRank int `json:"dp_rank"`
	// DPSize is the total number of data parallel workers in the group
	DPSize int `json:"dp_size"`
}

type LLMWorkerSlice []LLMWorker

// Id returns a unique identifier for the worker.
// The identifier is based on the worker's string representation.
// Returns an empty string if the worker has no identifiable information.
func (w *LLMWorker) Id() string {
	if w.ID == "" {
		w.ID = w.String()
	}
	return w.ID
}

// String returns a human-readable string representation of the worker.
func (w *LLMWorker) String() string {
	var parts []string

	// Build the prefix part (Model and/or Role)
	if w.Model != "" && w.Role != "" {
		parts = append(parts, fmt.Sprintf("%s|%s", w.Model, w.Role))
	} else if w.Model != "" {
		parts = append(parts, w.Model)
	} else if w.Role != "" {
		parts = append(parts, string(w.Role))
	}

	// Add endpoint
	endpointStr := w.Endpoint.String()
	if endpointStr != "" {
		parts = append(parts, endpointStr)
	}

	// Add Aux Port
	if w.AuxPort > 0 {
		parts = append(parts, fmt.Sprintf("%d", w.AuxPort))
	}

	// Add data parallel information if applicable
	if w.DPSize > 0 {
		parts = append(parts, fmt.Sprintf("%d/%d", w.DPRank, w.DPSize))
	}

	// Join all parts with hyphens
	result := strings.Join(parts, "-")

	// If we have no identifiable information, return at least the endpoint
	if result == "" && endpointStr != "" {
		return endpointStr
	}

	return result
}
