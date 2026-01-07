package batch

import (
	"encoding/json"
	"fmt"
)

// BatchTaskStatus represents the status of a batch task
type BatchTaskStatus string

const (
	StatusValidating BatchTaskStatus = "validating"
	StatusFailed     BatchTaskStatus = "failed"
	StatusInProgress BatchTaskStatus = "in_progress"
	StatusFinalizing BatchTaskStatus = "finalizing"
	StatusFinalize   BatchTaskStatus = "finalize"
	StatusCompleted  BatchTaskStatus = "completed"
	StatusExpired    BatchTaskStatus = "expired"
	StatusCancelling BatchTaskStatus = "cancelling"
	StatusCancelled  BatchTaskStatus = "cancelled"
	StatusPending    BatchTaskStatus = "pending"
)

// ShardStatus represents the status of a file shard
type ShardStatus string

const (
	ShardStatusPending    ShardStatus = "pending"
	ShardStatusInProgress ShardStatus = "in_progress"
	ShardStatusCompleted  ShardStatus = "completed"
	ShardStatusFailed     ShardStatus = "failed"
)

// RequestCounts represents the counts of requests in a batch task
type RequestCounts struct {
	Total     int `json:"total"`
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
}

// BatchError represents an error in a batch task
type BatchError struct {
	Code    string `json:"code"`
	Line    int    `json:"line"`
	Message string `json:"message"`
	Param   string `json:"param"`
}

// BatchErrors represents the errors object for a batch task
type BatchErrors struct {
	Data []BatchError `json:"data"`
}

// MetadataValidationError represents an error when metadata validation fails
type MetadataValidationError struct {
	Field   string
	Message string
}

func (e *MetadataValidationError) Error() string {
	return fmt.Sprintf("metadata validation error in field '%s': %s", e.Field, e.Message)
}

// ValidateMetadata validates that the metadata conforms to the specified constraints:
// - Maximum of 16 key-value pairs
// - Keys are strings with maximum length of 64 characters
// - Values are strings with maximum length of 512 characters
func ValidateMetadata(metadata map[string]string) error {
	if len(metadata) > 16 {
		return &MetadataValidationError{
			Field:   "",
			Message: fmt.Sprintf("too many key-value pairs: %d (maximum is 16)", len(metadata)),
		}
	}

	for key, value := range metadata {
		if len(key) > 64 {
			return &MetadataValidationError{
				Field:   key,
				Message: fmt.Sprintf("key too long: %d characters (maximum is 64)", len(key)),
			}
		}

		// Convert value to string to check length
		valueStr := fmt.Sprintf("%v", value)
		if len(valueStr) > 512 {
			return &MetadataValidationError{
				Field:   key,
				Message: fmt.Sprintf("value too long: %d characters (maximum is 512)", len(valueStr)),
			}
		}
	}

	return nil
}

// Usage represents the token usage statistics for a batch task
type Usage struct {
	InputTokens         int            `json:"input_tokens"`
	InputTokensDetails  *TokensDetails `json:"input_tokens_details"`
	OutputTokens        int            `json:"output_tokens"`
	OutputTokensDetails *TokensDetails `json:"output_tokens_details"`
	TotalTokens         int            `json:"total_tokens"`
}

// TokensDetails represents details about token usage
type TokensDetails struct {
	CachedTokens    int `json:"cached_tokens"`
	ReasoningTokens int `json:"reasoning_tokens"`
}

// File represents a batch file
type File struct {
	ID        string `json:"id"`
	Object    string `json:"object"`
	Bytes     int64  `json:"bytes"`
	CreatedAt int64  `json:"created_at"`
	ExpiresAt int64  `json:"expires_at"`
	Filename  string `json:"filename"`
	Purpose   string `json:"purpose"`
}

// OutputExpiresAfter represents the expiration configuration for output files
type OutputExpiresAfter struct {
	Anchor  string `json:"anchor"`
	Seconds int64  `json:"seconds"`
}

// BatchTask represents a batch processing task
type BatchTask struct {
	ID               string            `json:"id"`
	Object           string            `json:"object"`
	Endpoint         string            `json:"endpoint"`
	Model            string            `json:"model"`
	Errors           *BatchErrors      `json:"errors"`
	InputFileID      string            `json:"input_file_id"`
	CompletionWindow string            `json:"completion_window"`
	Status           BatchTaskStatus   `json:"status"`
	OutputFileID     *string           `json:"output_file_id"`
	ErrorFileID      *string           `json:"error_file_id"`
	CreatedAt        *int64            `json:"created_at"`
	InProgressAt     *int64            `json:"in_progress_at"`
	ExpiresAt        int64             `json:"expires_at"`
	FinalizingAt     *int64            `json:"finalizing_at"`
	CompletedAt      *int64            `json:"completed_at"`
	FailedAt         *int64            `json:"failed_at"`
	ExpiredAt        *int64            `json:"expired_at"`
	CancellingAt     *int64            `json:"cancelling_at"`
	CancelledAt      *int64            `json:"cancelled_at"`
	RequestCounts    *RequestCounts    `json:"request_counts"`
	Usage            *Usage            `json:"usage"`
	Metadata         map[string]string `json:"metadata"`

	OutputExpiresAfter *OutputExpiresAfter `json:"output_expires_after,omitempty"`
	UpdatedAt          *int64              `json:"updated_at"`
	Token              *string             `json:"token,omitempty"`
}

// FileShard represents a file shard
type FileShard struct {
	ID            string         `json:"id"`
	CreatedAt     *int64         `json:"created_at"`
	RequestCounts *RequestCounts `json:"request_counts"`
	UpdatedAt     *int64         `json:"updated_at"`
}

// SingleLineRequest represents the expected format of each line in the batch input file
type SingleLineRequest struct {
	CustomID string          `json:"custom_id"`
	Method   string          `json:"method"`
	URL      string          `json:"url"`
	Body     json.RawMessage `json:"body"`
}

// BatchRequestError represents an error for a batch request
type BatchRequestError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// SingleLineResponse represents the response format for each request in a batch
type SingleLineResponse struct {
	ID       string             `json:"id"`
	CustomID string             `json:"custom_id"`
	Response *RespData          `json:"response,omitempty"`
	Error    *BatchRequestError `json:"error,omitempty"`
}

// RespData represents the response data structure
type RespData struct {
	StatusCode int             `json:"status_code"`
	RequestID  string          `json:"request_id"`
	Body       json.RawMessage `json:"body"`
}
