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

	// BaseURL is the base address of the external LLM service (e.g. https://ark.cn-beijing.volces.com/api/v3).
	// It does not include a specific path such as /completions or /chat/completions.
	// BaseURL and Endpoint are mutually exclusive: BaseURL is used for external service routing,
	// while Endpoint represents an internal worker address discovered via the scheduler.
	BaseURL string `json:"-"`
	// APIKey is the authentication key used to access the LLM service
	APIKey string `json:"-"`

	// Released indicates whether the worker resource has been released for use
	Released bool `json:"-"`
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

	// Add address: BaseURL and Endpoint are mutually exclusive.
	// BaseURL is used for external service routing; Endpoint for internal scheduler-discovered workers.
	var addrStr string
	if w.BaseURL != "" {
		addrStr = w.BaseURL
	} else {
		addrStr = w.Endpoint.String()
	}
	if addrStr != "" {
		parts = append(parts, addrStr)
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

	// If we have no identifiable information, return at least the address
	if result == "" && addrStr != "" {
		return addrStr
	}

	return result
}

// BuildRequestURL builds a full request URL by appending path to the worker's address.
// The address is resolved in priority order:
//  1. Endpoint (internal scheduler-discovered worker): returns http://<host:port><path>
//  2. BaseURL (external LLM service): appends path to BaseURL, stripping any redundant
//     version prefix from path when BaseURL already ends with a version segment
//     (e.g. BaseURL="/api/v3" + path="/v1/chat/completions" → "/api/v3/chat/completions").
//
// Returns an error if both Endpoint and BaseURL are empty.
func (w *LLMWorker) BuildRequestURL(path string) (string, error) {
	epStr := w.Endpoint.String()
	if epStr != "" {
		return fmt.Sprintf("http://%s%s", epStr, path), nil
	}

	base := strings.TrimSuffix(w.BaseURL, "/")
	if base == "" {
		return "", fmt.Errorf("worker %s has neither Endpoint nor BaseURL set", w.Id())
	}
	if hasVersionSuffix(base) && strings.HasPrefix(path, "/v") {
		path = trimVersionPrefix(path)
	}
	return base + path, nil
}

// hasVersionSuffix reports whether s ends with a /v<digits> segment (e.g. "/api/v3").
func hasVersionSuffix(s string) bool {
	idx := strings.LastIndex(s, "/v")
	if idx == -1 {
		return false
	}
	suffix := s[idx+2:] // characters after "/v"
	if len(suffix) == 0 {
		return false
	}
	for _, c := range suffix {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// trimVersionPrefix removes the leading /v<digits> segment from path.
// e.g. "/v1/chat/completions" → "/chat/completions", "/v1" → "/"
func trimVersionPrefix(path string) string {
	rest := path[2:] // skip leading "/v"
	idx := strings.Index(rest, "/")
	if idx == -1 {
		return "/"
	}
	return rest[idx:]
}
