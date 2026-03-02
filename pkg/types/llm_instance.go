package types

import (
	"fmt"
	"strings"

	"llumnix/pkg/consts"
)

// LLMInstance represents a single LLM inference worker instance.
// It contains all necessary information to identify and connect to the worker.
type LLMInstance struct {
	// Version may be a timestamp indicating the create time or a version identifier
	Version int64 `json:"version"`
	// ID is a unique identifier for the worker
	ID string `json:"id"`
	// Model is the name of the model served by this worker
	Model string `json:"model"`
	// InferType is the inference type (prefill/decode/neutral) of the worker
	InferType consts.InferType `json:"infer_type"`
	// Endpoint is the network address where the worker can be reached
	Endpoint Endpoint `json:"endpoint"`
	// AuxIp is the auxiliary ip of the worker for PD disaggregation
	AuxIp string `json:"aux_ip"`
	// AuxPort is the auxiliary port of the worker for PD disaggregation
	AuxPort int `json:"aux_port"`
	// DpRank is the data parallel rank of this worker (0-based)
	DPRank int `json:"dp_rank"`
	// DPSize is the total number of data parallel workers in the group
	DPSize int `json:"dp_size"`
}

type LLMInstanceSlice []LLMInstance

// Id returns a unique identifier for the worker.
// The identifier is based on the worker's string representation.
// Returns an empty string if the worker has no identifiable information.
func (w *LLMInstance) Id() string {
	if w.ID == "" {
		w.ID = w.String()
	}
	return w.ID
}

// String returns a human-readable string representation of the worker.
func (w *LLMInstance) String() string {
	var parts []string

	// Build the prefix part (Model and/or InferType)
	if w.Model != "" && w.InferType != "" {
		parts = append(parts, fmt.Sprintf("%s|%s", w.Model, w.InferType))
	} else if w.Model != "" {
		parts = append(parts, w.Model)
	} else if w.InferType != "" {
		parts = append(parts, string(w.InferType))
	}

	// Add endpoint
	endpointStr := w.Endpoint.String()
	if endpointStr != "" {
		parts = append(parts, endpointStr)
	}

	// Add Aux Ip
	if w.AuxIp != "" {
		parts = append(parts, w.AuxIp)
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
