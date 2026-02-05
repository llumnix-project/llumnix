package batch

import (
	"context"
	"encoding/json"
	"fmt"
	"llumnix/pkg/redis"
	"strconv"
	"time"

	"k8s.io/klog/v2"
)

// RedisStore provides Redis operations for batch processing
type RedisStore struct {
	redisClient redis.RedisClient
}

// NewRedisStore creates a new Redis store for batch processing
func NewRedisStore(redisClient redis.RedisClient) *RedisStore {
	return &RedisStore{
		redisClient: redisClient,
	}
}

// File operations

// CreateFile creates a new file record in Redis
func (r *RedisStore) CreateFile(ctx context.Context, file *File) error {
	key := r.getFileKey(file.ID)

	// Store individual fields in a Redis hash
	fields := map[string]string{
		"id":         file.ID,
		"object":     file.Object,
		"bytes":      fmt.Sprintf("%d", file.Bytes),
		"created_at": fmt.Sprintf("%d", file.CreatedAt),
		"expires_at": fmt.Sprintf("%d", file.ExpiresAt),
		"filename":   file.Filename,
		"purpose":    file.Purpose,
	}

	// Create the file record
	if err := r.redisClient.HSet(ctx, key, fields); err != nil {
		return err
	}

	// Add file to the file set for tracking
	return r.AddFileToSet(ctx, file.ID)
}

// GetFile retrieves a file record from Redis
func (r *RedisStore) GetFile(ctx context.Context, fileID string) (*File, error) {
	key := r.getFileKey(fileID)

	// Retrieve all fields from Redis hash
	values, err := r.redisClient.HGetAll(ctx, key)
	if err != nil {
		return nil, err
	}

	// Check if the file exists (empty map means key doesn't exist)
	if len(values) == 0 {
		return nil, fmt.Errorf("file not found: %s", fileID)
	}

	// Parse created_at
	createdAt, err := strconv.ParseInt(values["created_at"], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("failed to parse created_at: %v", err)
	}

	// Parse bytes
	bytes, err := strconv.ParseInt(values["bytes"], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("failed to parse bytes: %v", err)
	}

	// Parse expires_at
	expiresAt, err := strconv.ParseInt(values["expires_at"], 10, 64)
	if err != nil {
		// If expires_at is not a valid integer, set it to 0
		expiresAt = 0
	}

	return &File{
		ID:        values["id"],
		Object:    values["object"],
		Bytes:     bytes,
		CreatedAt: createdAt,
		ExpiresAt: expiresAt,
		Filename:  values["filename"],
		Purpose:   values["purpose"],
	}, nil
}

// DeleteFile deletes a file record from Redis
func (r *RedisStore) DeleteFile(ctx context.Context, fileID string) error {
	key := r.getFileKey(fileID)

	// Delete the file record
	if err := r.redisClient.Del(ctx, key); err != nil {
		return err
	}

	// Remove file from the file set
	return r.RemoveFileFromSet(ctx, fileID)
}

// AddFileToSet adds a file ID to the file set
func (r *RedisStore) AddFileToSet(ctx context.Context, fileID string) error {
	key := r.getFileSetKey()
	return r.redisClient.SAdd(ctx, key, fileID)
}

// RemoveFileFromSet removes a file ID from the file set
func (r *RedisStore) RemoveFileFromSet(ctx context.Context, fileID string) error {
	key := r.getFileSetKey()
	return r.redisClient.SRem(ctx, key, fileID)
}

// GetAllFiles returns all file IDs in the file set
func (r *RedisStore) GetAllFiles(ctx context.Context) ([]string, error) {
	key := r.getFileSetKey()
	return r.redisClient.SMembers(ctx, key)
}

// Task operations

// CreateTask creates a new batch task in Redis
func (r *RedisStore) CreateTask(ctx context.Context, task *BatchTask) error {
	key := r.getTaskKey(task.ID)

	// Store individual fields in a Redis hash
	fields := map[string]string{
		"id":                task.ID,
		"object":            task.Object,
		"endpoint":          task.Endpoint,
		"model":             task.Model,
		"errors":            r.marshalBatchErrors(task.Errors),
		"input_file_id":     task.InputFileID,
		"completion_window": task.CompletionWindow,
		"status":            string(task.Status),
		"created_at":        fmt.Sprintf("%d", *task.CreatedAt),
		"expires_at":        fmt.Sprintf("%d", task.ExpiresAt),
		"updated_at":        fmt.Sprintf("%d", *task.UpdatedAt),
	}

	// Handle optional fields
	if task.OutputFileID != nil {
		fields["output_file_id"] = *task.OutputFileID
	}
	if task.ErrorFileID != nil {
		fields["error_file_id"] = *task.ErrorFileID
	}
	if task.InProgressAt != nil {
		fields["in_progress_at"] = fmt.Sprintf("%d", *task.InProgressAt)
	}
	if task.FinalizingAt != nil {
		fields["finalizing_at"] = fmt.Sprintf("%d", *task.FinalizingAt)
	}
	if task.CompletedAt != nil {
		fields["completed_at"] = fmt.Sprintf("%d", *task.CompletedAt)
	}
	if task.FailedAt != nil {
		fields["failed_at"] = fmt.Sprintf("%d", *task.FailedAt)
	}
	if task.ExpiredAt != nil {
		fields["expired_at"] = fmt.Sprintf("%d", *task.ExpiredAt)
	}
	if task.CancellingAt != nil {
		fields["cancelling_at"] = fmt.Sprintf("%d", *task.CancellingAt)
	}
	if task.CancelledAt != nil {
		fields["cancelled_at"] = fmt.Sprintf("%d", *task.CancelledAt)
	}
	if len(task.Metadata) > 0 {
		metadataJSON, err := json.Marshal(task.Metadata)
		if err != nil {
			return fmt.Errorf("failed to marshal metadata: %v", err)
		}
		fields["metadata"] = string(metadataJSON)
	}

	// Store request counts as JSON
	if task.RequestCounts != nil {
		requestCountsJSON, err := json.Marshal(task.RequestCounts)
		if err != nil {
			return fmt.Errorf("failed to marshal request counts: %v", err)
		}
		fields["request_counts"] = string(requestCountsJSON)
	}

	// Store usage as JSON
	if task.Usage != nil {
		usageJSON, err := json.Marshal(task.Usage)
		if err != nil {
			return fmt.Errorf("failed to marshal usage: %v", err)
		}
		fields["usage"] = string(usageJSON)
	}

	// Store output_expires_after as JSON if present
	if task.OutputExpiresAfter != nil {
		outputExpiresAfterJSON, err := json.Marshal(task.OutputExpiresAfter)
		if err != nil {
			return fmt.Errorf("failed to marshal output_expires_after: %v", err)
		}
		fields["output_expires_after"] = string(outputExpiresAfterJSON)
	}

	// Store token if present
	if task.Token != nil {
		fields["token"] = *task.Token
	}

	// Create the task record
	if err := r.redisClient.HSet(ctx, key, fields); err != nil {
		return err
	}

	// Add task to the global task set for tracking
	if err := r.AddTaskToGlobalSet(ctx, task.ID); err != nil {
		return err
	}

	// Add task to pending list
	pendingKey := r.getTaskSetKey(StatusPending)
	return r.redisClient.SAdd(ctx, pendingKey, task.ID)
}

// marshalBatchErrors converts BatchErrors to a JSON string
func (r *RedisStore) marshalBatchErrors(errors *BatchErrors) string {
	if errors == nil {
		return ""
	}
	jsonData, err := json.Marshal(errors)
	if err != nil {
		return fmt.Sprintf("error marshaling batch errors: %v", err)
	}
	return string(jsonData)
}

// GetTask retrieves a batch task from Redis
func (r *RedisStore) GetTask(ctx context.Context, taskID string) (*BatchTask, error) {
	key := r.getTaskKey(taskID)

	// Retrieve all fields in the hash
	values, err := r.redisClient.HGetAll(ctx, key)
	if err != nil {
		return nil, err
	}

	// Check if the task exists (empty map means key doesn't exist)
	if len(values) == 0 {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}

	// Parse required fields
	createdAtVal, err := strconv.ParseInt(values["created_at"], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("failed to parse created_at: %v", err)
	}
	createdAt := &createdAtVal

	expiresAt, err := strconv.ParseInt(values["expires_at"], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("failed to parse expires_at: %v", err)
	}

	updatedAtVal, err := strconv.ParseInt(values["updated_at"], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("failed to parse updated_at: %v", err)
	}
	updatedAt := &updatedAtVal

	// Parse optional fields
	var inProgressAt *int64
	if val, exists := values["in_progress_at"]; exists && val != "" {
		if t, err := strconv.ParseInt(val, 10, 64); err == nil {
			inProgressAt = &t
		}
	}

	var finalizingAt *int64
	if val, exists := values["finalizing_at"]; exists && val != "" {
		if t, err := strconv.ParseInt(val, 10, 64); err == nil {
			finalizingAt = &t
		}
	}

	var completedAt *int64
	if val, exists := values["completed_at"]; exists && val != "" {
		if t, err := strconv.ParseInt(val, 10, 64); err == nil {
			completedAt = &t
		}
	}

	var failedAt *int64
	if val, exists := values["failed_at"]; exists && val != "" {
		if t, err := strconv.ParseInt(val, 10, 64); err == nil {
			failedAt = &t
		}
	}

	var expiredAt *int64
	if val, exists := values["expired_at"]; exists && val != "" {
		if t, err := strconv.ParseInt(val, 10, 64); err == nil {
			expiredAt = &t
		}
	}

	var cancellingAt *int64
	if val, exists := values["cancelling_at"]; exists && val != "" {
		if t, err := strconv.ParseInt(val, 10, 64); err == nil {
			cancellingAt = &t
		}
	}

	var cancelledAt *int64
	if val, exists := values["cancelled_at"]; exists && val != "" {
		if t, err := strconv.ParseInt(val, 10, 64); err == nil {
			cancelledAt = &t
		}
	}

	var outputFileID *string
	if val, exists := values["output_file_id"]; exists && val != "" {
		outputFileID = &val
	}

	var errorFileID *string
	if val, exists := values["error_file_id"]; exists && val != "" {
		errorFileID = &val
	}

	metadata := make(map[string]string)
	if val, exists := values["metadata"]; exists && val != "" {
		if err := json.Unmarshal([]byte(val), &metadata); err != nil {
			// If we can't unmarshal as JSON, try to parse as a simple string
			metadata["value"] = val
		}
	}

	// Parse request counts
	var requestCounts *RequestCounts
	if val, exists := values["request_counts"]; exists && val != "" {
		requestCounts = &RequestCounts{}
		if err := json.Unmarshal([]byte(val), requestCounts); err != nil {
			return nil, fmt.Errorf("failed to unmarshal request_counts: %v", err)
		}
	}

	// Parse usage
	var usage *Usage
	if val, exists := values["usage"]; exists && val != "" {
		usage = &Usage{}
		if err := json.Unmarshal([]byte(val), usage); err != nil {
			return nil, fmt.Errorf("failed to unmarshal usage: %v", err)
		}
	}

	// Parse errors
	var errors *BatchErrors
	if val, exists := values["errors"]; exists && val != "" {
		errors = &BatchErrors{}
		if err := json.Unmarshal([]byte(val), errors); err != nil {
			// If we can't unmarshal as BatchErrors, try to parse as a simple string error
			errors = &BatchErrors{
				Data: []BatchError{
					{
						Code:    "parsing_error",
						Message: val,
					},
				},
			}
		}
	}

	// Parse output_expires_after
	var outputExpiresAfter *OutputExpiresAfter
	if val, exists := values["output_expires_after"]; exists && val != "" {
		outputExpiresAfter = &OutputExpiresAfter{}
		if err := json.Unmarshal([]byte(val), outputExpiresAfter); err != nil {
			// If we can't unmarshal, we'll leave it as nil
			outputExpiresAfter = nil
		}
	}

	// Parse token
	var token *string
	if val, exists := values["token"]; exists && val != "" {
		token = &val
	}

	return &BatchTask{
		ID:                 values["id"],
		Object:             values["object"],
		Endpoint:           values["endpoint"],
		Model:              values["model"],
		Errors:             errors,
		InputFileID:        values["input_file_id"],
		CompletionWindow:   values["completion_window"],
		Status:             BatchTaskStatus(values["status"]),
		CreatedAt:          createdAt,
		ExpiresAt:          expiresAt,
		UpdatedAt:          updatedAt,
		InProgressAt:       inProgressAt,
		FinalizingAt:       finalizingAt,
		CompletedAt:        completedAt,
		FailedAt:           failedAt,
		ExpiredAt:          expiredAt,
		CancellingAt:       cancellingAt,
		CancelledAt:        cancelledAt,
		OutputFileID:       outputFileID,
		ErrorFileID:        errorFileID,
		Metadata:           metadata,
		RequestCounts:      requestCounts,
		Usage:              usage,
		OutputExpiresAfter: outputExpiresAfter,
		Token:              token,
	}, nil
}

// DeleteTask deletes a task record from Redis
func (r *RedisStore) DeleteTask(ctx context.Context, taskID string) error {
	key := r.getTaskKey(taskID)

	// Delete the task record
	if err := r.redisClient.Del(ctx, key); err != nil {
		return err
	}

	// Remove task from the global task set
	if err := r.RemoveTaskFromGlobalSet(ctx, taskID); err != nil {
		// Log the error but don't fail the operation
		klog.Warningf("Failed to remove task %s from global task set: %v", taskID, err)
	}

	// TODO: Remove from all possible status sets.

	return nil
}

// UpdateTaskStatus updates the status of a batch task
func (r *RedisStore) UpdateTaskStatus(ctx context.Context, taskID string, status BatchTaskStatus) error {
	taskKey := r.getTaskKey(taskID)

	now := time.Now().Unix()

	// Prepare fields to update
	fields := map[string]string{
		"status":     string(status),
		"updated_at": fmt.Sprintf("%d", now),
	}

	// Set specific timestamp fields based on status
	nowStr := fmt.Sprintf("%d", now)
	switch status {
	case StatusInProgress:
		// If in_progress_at is not set, set it
		fields["in_progress_at"] = nowStr
	case StatusFinalizing:
		// Set finalizing_at
		fields["finalizing_at"] = nowStr
	case StatusFinalize:
		// This is when finalization actually starts, but we already set finalizing_at
		// when moving to StatusFinalizing, so no additional action needed here
	case StatusCompleted:
		// Set completed_at
		fields["completed_at"] = nowStr
	case StatusFailed:
		// Set failed_at
		fields["failed_at"] = nowStr
	case StatusExpired:
		// Set expired_at
		fields["expired_at"] = nowStr
	case StatusCancelling:
		// Set cancelling_at
		fields["cancelling_at"] = nowStr
	case StatusCancelled:
		// Set cancelled_at
		fields["cancelled_at"] = nowStr
	}

	// Update all fields in a single HSet operation
	return r.redisClient.HSet(ctx, taskKey, fields)
}

// UpdateTaskFailure updates a task with failure information
func (r *RedisStore) UpdateTaskFailure(ctx context.Context, taskID string, batchErr *BatchErrors) error {
	taskKey := r.getTaskKey(taskID)

	now := time.Now().Unix()

	errorsJSON := r.marshalBatchErrors(batchErr)

	fields := map[string]string{
		"status":     string(StatusFailed),
		"errors":     errorsJSON,
		"failed_at":  fmt.Sprintf("%d", now),
		"updated_at": fmt.Sprintf("%d", now),
	}

	return r.redisClient.HSet(ctx, taskKey, fields)
}

// UpdateTaskFields updates specific fields of a task
func (r *RedisStore) UpdateTaskFields(ctx context.Context, taskID string, task *BatchTask) error {
	taskKey := r.getTaskKey(taskID)

	fields := map[string]string{
		"updated_at": fmt.Sprintf("%d", *task.UpdatedAt),
	}

	// Add request counts if present
	if task.RequestCounts != nil {
		requestCountsJSON, err := json.Marshal(task.RequestCounts)
		if err != nil {
			return fmt.Errorf("failed to marshal request counts: %v", err)
		}
		fields["request_counts"] = string(requestCountsJSON)
	}

	// Add usage if present
	if task.Usage != nil {
		usageJSON, err := json.Marshal(task.Usage)
		if err != nil {
			return fmt.Errorf("failed to marshal usage: %v", err)
		}
		fields["usage"] = string(usageJSON)
	}

	// Add output file ID if present
	if task.OutputFileID != nil {
		fields["output_file_id"] = *task.OutputFileID
	}

	// Add error file ID if present
	if task.ErrorFileID != nil {
		fields["error_file_id"] = *task.ErrorFileID
	}

	// Add output_expires_after if present
	if task.OutputExpiresAfter != nil {
		outputExpiresAfterJSON, err := json.Marshal(task.OutputExpiresAfter)
		if err != nil {
			return fmt.Errorf("failed to marshal output_expires_after: %v", err)
		}
		fields["output_expires_after"] = string(outputExpiresAfterJSON)
	}

	// Add status if provided
	fields["status"] = string(task.Status)

	// Add optional time fields if present
	if task.InProgressAt != nil {
		fields["in_progress_at"] = fmt.Sprintf("%d", *task.InProgressAt)
	}
	if task.FinalizingAt != nil {
		fields["finalizing_at"] = fmt.Sprintf("%d", *task.FinalizingAt)
	}
	if task.CompletedAt != nil {
		fields["completed_at"] = fmt.Sprintf("%d", *task.CompletedAt)
	}
	if task.FailedAt != nil {
		fields["failed_at"] = fmt.Sprintf("%d", *task.FailedAt)
	}
	if task.ExpiredAt != nil {
		fields["expired_at"] = fmt.Sprintf("%d", *task.ExpiredAt)
	}
	if task.CancellingAt != nil {
		fields["cancelling_at"] = fmt.Sprintf("%d", *task.CancellingAt)
	}
	if task.CancelledAt != nil {
		fields["cancelled_at"] = fmt.Sprintf("%d", *task.CancelledAt)
	}

	return r.redisClient.HSet(ctx, taskKey, fields)
}

// MoveTaskAndUpdateStatusAtomically moves a task from one set to another and updates its status atomically
func (r *RedisStore) MoveTaskAndUpdateStatusAtomically(ctx context.Context, taskID string, fromSet, toSet BatchTaskStatus) error {
	script := `
		local taskID = ARGV[1]
		local fromKey = ARGV[2]
		local toKey = ARGV[3]
		local taskKey = ARGV[4]
		local toSetName = ARGV[5]

		-- Add to destination set first and check if it was already there
		local added = redis.call("SADD", toKey, taskID)

		-- Remove from source set
		local removed = redis.call("SREM", fromKey, taskID)

		-- If task was not in the source set, return an error
		if removed == 0 then
			-- Clean up by removing from destination set since task wasn't in source set
			-- Only remove if we actually added it (added == 1)
			if added == 1 then
				redis.call("SREM", toKey, taskID)
			end
			return redis.error_reply("Task " .. taskID .. " not found in set " .. fromKey)
		end

		-- Update task status
		redis.call("HSET", taskKey, "status", toSetName)

		-- Update the UpdatedAt timestamp
		local now = ARGV[6]
		redis.call("HSET", taskKey, "updated_at", now)

		-- Set specific timestamp fields based on status
		if toSetName == "in_progress" then
			local inProgressAt = redis.call("HGET", taskKey, "in_progress_at")
			if not inProgressAt or inProgressAt == false then
				redis.call("HSET", taskKey, "in_progress_at", now)
			end
		elseif toSetName == "finalizing" then
			redis.call("HSET", taskKey, "finalizing_at", now)
		elseif toSetName == "completed" then
			redis.call("HSET", taskKey, "completed_at", now)
		elseif toSetName == "failed" then
			redis.call("HSET", taskKey, "failed_at", now)
		elseif toSetName == "expired" then
			redis.call("HSET", taskKey, "expired_at", now)
		elseif toSetName == "cancelling" then
			redis.call("HSET", taskKey, "cancelling_at", now)
		elseif toSetName == "cancelled" then
			redis.call("HSET", taskKey, "cancelled_at", now)
		end

		return 1
	`

	now := fmt.Sprintf("%d", time.Now().Unix())
	fromKey := r.getTaskSetKey(fromSet)
	toKey := r.getTaskSetKey(toSet)
	taskKey := r.getTaskKey(taskID)
	_, err := r.redisClient.Eval(ctx, script, []string{}, taskID, fromKey, toKey, taskKey, string(toSet), now)
	return err
}

// MoveTaskAtomically moves a task from one set to another atomically
func (r *RedisStore) MoveTaskAtomically(ctx context.Context, taskID string, fromSet, toSet BatchTaskStatus) error {
	script := `
		local taskID = ARGV[1]
		local fromKey = ARGV[2]
		local toKey = ARGV[3]

		-- Add to destination set first and check if it was already there
		local added = redis.call("SADD", toKey, taskID)

		-- Remove from source set
		local removed = redis.call("SREM", fromKey, taskID)

		-- If task was not in the source set, return an error
		if removed == 0 then
			-- Clean up by removing from destination set since task wasn't in source set
			-- Only remove if we actually added it (added == 1)
			if added == 1 then
				redis.call("SREM", toKey, taskID)
			end
			return redis.error_reply("Task " .. taskID .. " not found in set " .. fromKey)
		end

		return 1
	`

	fromKey := r.getTaskSetKey(fromSet)
	toKey := r.getTaskSetKey(toSet)
	_, err := r.redisClient.Eval(ctx, script, []string{}, taskID, fromKey, toKey)
	return err
}

// GetTasksInStatus returns all task IDs in a set
func (r *RedisStore) GetTasksInStatus(ctx context.Context, status BatchTaskStatus) ([]string, error) {
	key := r.getTaskSetKey(status)
	return r.redisClient.SMembers(ctx, key)
}

// GetExpiredTasks returns all tasks that have expired from the specified sets
func (r *RedisStore) GetExpiredTasks(ctx context.Context, status ...BatchTaskStatus) ([]string, error) {
	var expiredTaskIDs []string

	now := time.Now()
	// For each set, check all tasks for expiration
	for _, s := range status {
		taskIDs, err := r.GetTasksInStatus(ctx, s)
		if err != nil {
			return nil, err
		}

		// Check each task in the set
		for _, taskID := range taskIDs {
			// Get only the expiration time for efficiency
			expiresAt, err := r.GetTaskExpiresAt(ctx, taskID)
			if err != nil {
				// Skip tasks that can't be retrieved
				continue
			}

			// Check if task has expired
			if expiresAt.Before(now) {
				expiredTaskIDs = append(expiredTaskIDs, taskID)
			}
		}
	}

	return expiredTaskIDs, nil
}

// AddTaskToGlobalSet adds a task ID to the global task set
func (r *RedisStore) AddTaskToGlobalSet(ctx context.Context, taskID string) error {
	key := r.getGlobalTaskSetKey()
	return r.redisClient.SAdd(ctx, key, taskID)
}

// RemoveTaskFromGlobalSet removes a task ID from the global task set
func (r *RedisStore) RemoveTaskFromGlobalSet(ctx context.Context, taskID string) error {
	key := r.getGlobalTaskSetKey()
	return r.redisClient.SRem(ctx, key, taskID)
}

// GetAllTasks returns all task IDs in the global task set
func (r *RedisStore) GetAllTasks(ctx context.Context) ([]string, error) {
	key := r.getGlobalTaskSetKey()
	return r.redisClient.SMembers(ctx, key)
}

// GetTaskExpiresAt retrieves only the expiration time of a batch task from Redis
func (r *RedisStore) GetTaskExpiresAt(ctx context.Context, taskID string) (time.Time, error) {
	key := r.getTaskKey(taskID)

	// Retrieve only the expires_at field
	fields := []string{"expires_at"}
	values, err := r.redisClient.HGet(ctx, key, fields...)
	if err != nil {
		return time.Time{}, err
	}

	// Parse expires_at as integer timestamp
	expiresAtInt, err := strconv.ParseInt(values["expires_at"], 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to parse expires_at: %v", err)
	}

	// Convert to time.Time
	expiresAt := time.Unix(expiresAtInt, 0)
	return expiresAt, nil
}

// GetTaskStatus retrieves only the status of a batch task from Redis
func (r *RedisStore) GetTaskStatus(ctx context.Context, taskID string) (BatchTaskStatus, error) {
	key := r.getTaskKey(taskID)

	// Retrieve only the status field
	fields := []string{"status"}
	values, err := r.redisClient.HGet(ctx, key, fields...)
	if err != nil {
		return "", err
	}

	return BatchTaskStatus(values["status"]), nil
}

// File shard operations

// CreateOrUpdateFileShard creates or updates a file shard record
func (r *RedisStore) CreateOrUpdateFileShard(ctx context.Context, taskID, shardID string, shard *FileShard) error {
	key := r.getShardKey(taskID, shardID)

	// Store individual fields in a Redis hash
	fields := map[string]string{
		"id":         shard.ID,
		"created_at": fmt.Sprintf("%d", *shard.CreatedAt),
		"updated_at": fmt.Sprintf("%d", *shard.UpdatedAt),
	}

	// Store request counts as JSON if present
	if shard.RequestCounts != nil {
		requestCountsJSON, err := json.Marshal(shard.RequestCounts)
		if err != nil {
			return fmt.Errorf("failed to marshal request counts: %v", err)
		}
		fields["request_counts"] = string(requestCountsJSON)
	}

	return r.redisClient.HSet(ctx, key, fields)
}

// GetFileShard retrieves a file shard record
func (r *RedisStore) GetFileShard(ctx context.Context, taskID, shardID string) (*FileShard, error) {
	key := r.getShardKey(taskID, shardID)

	// Retrieve all fields from Redis hash
	values, err := r.redisClient.HGetAll(ctx, key)
	if err != nil {
		return nil, err
	}

	// Parse time fields
	createdAtVal, err := strconv.ParseInt(values["created_at"], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("failed to parse created_at: %v", err)
	}
	createdAt := &createdAtVal

	updatedAtVal, err := strconv.ParseInt(values["updated_at"], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("failed to parse updated_at: %v", err)
	}
	updatedAt := &updatedAtVal

	// Parse request counts
	var requestCounts *RequestCounts
	if val, exists := values["request_counts"]; exists && val != "" {
		requestCounts = &RequestCounts{}
		if err := json.Unmarshal([]byte(val), requestCounts); err != nil {
			return nil, fmt.Errorf("failed to unmarshal request_counts: %v", err)
		}
	}

	return &FileShard{
		ID:            values["id"],
		CreatedAt:     createdAt,
		RequestCounts: requestCounts,
		UpdatedAt:     updatedAt,
	}, nil
}

// AddShardToPendingList adds a shard to the pending list for a task
func (r *RedisStore) AddShardToPendingList(ctx context.Context, taskID, shardID string) error {
	key := r.getShardSetKey(taskID, ShardStatusPending)
	return r.redisClient.SAdd(ctx, key, shardID)
}

// MoveShardAtomically moves a shard from one status to another atomically
func (r *RedisStore) MoveShardAtomically(ctx context.Context, taskID, shardID string, fromStatus, toStatus ShardStatus) error {
	script := `
		local taskID = ARGV[1]
		local shardID = ARGV[2]
		local fromKey = ARGV[3]
		local toKey = ARGV[4]

		-- Add to destination status first and check if it was already there
		local added = redis.call("SADD", toKey, shardID)

		-- Remove from source status
		local removed = redis.call("SREM", fromKey, shardID)

		-- If shard was not in the source status, return an error
		if removed == 0 then
			-- Clean up by removing from destination status since shard wasn't in source status
			-- Only remove if we actually added it (added == 1)
			if added == 1 then
				redis.call("SREM", toKey, shardID)
			end
			return redis.error_reply("Shard " .. shardID .. " not found in status " .. fromKey)
		end

		return 1
	`

	fromKey := r.getShardSetKey(taskID, fromStatus)
	toKey := r.getShardSetKey(taskID, toStatus)
	_, err := r.redisClient.Eval(ctx, script, []string{}, taskID, shardID, fromKey, toKey)
	return err
}

// GetShardsInStatus returns all shard IDs with a specific status for a task
func (r *RedisStore) GetShardsInStatus(ctx context.Context, taskID string, status ShardStatus) ([]string, error) {
	key := r.getShardSetKey(taskID, status)
	return r.redisClient.SMembers(ctx, key)
}

// GetShardInStatus returns a random shard ID with a specific status for a task
func (r *RedisStore) GetShardInStatus(ctx context.Context, taskID string, status ShardStatus) (string, error) {
	key := r.getShardSetKey(taskID, status)
	shardIDs, err := r.redisClient.SRandMember(ctx, key, 1)
	if err != nil {
		return "", err
	}

	if len(shardIDs) == 0 {
		return "", fmt.Errorf("no shards in status %s", status)
	}

	return shardIDs[0], nil
}

// CheckIfAllShardsAreCompleted checks if all shards for a task have been completed
// (i.e., there are no pending or in_progress shards)
func (r *RedisStore) CheckIfAllShardsAreCompleted(ctx context.Context, taskID string) (bool, error) {
	script := `
		local pendingKey = ARGV[1]
		local inProgressKey = ARGV[2]

		-- Check if there are any pending shards
		local pendingShards = redis.call("SMEMBERS", pendingKey)

		-- Check if there are any in_progress shards
		local inProgressShards = redis.call("SMEMBERS", inProgressKey)

		-- Return true if no pending or in_progress shards (all completed)
		if #pendingShards == 0 and #inProgressShards == 0 then
			return 1
		end

		return 0
	`

	pendingKey := r.getShardSetKey(taskID, ShardStatusPending)
	inProgressKey := r.getShardSetKey(taskID, ShardStatusInProgress)
	result, err := r.redisClient.Eval(ctx, script, []string{}, pendingKey, inProgressKey)
	if err != nil {
		return false, err
	}

	// Convert result to boolean
	if result == nil {
		return false, nil
	}

	// Lua returns 1 for true and nil/false for false
	if result == int64(1) {
		return true, nil
	}

	return false, nil
}

// Lock operations

// AcquireTaskLock tries to acquire a lock for a task
func (r *RedisStore) AcquireTaskLock(ctx context.Context, taskID, instanceID string, expiration time.Duration) (bool, error) {
	key := r.getTaskLockKey(taskID)
	return r.redisClient.SetNX(ctx, key, instanceID, expiration)
}

// ReleaseTaskLock releases a task lock atomically
func (r *RedisStore) ReleaseTaskLock(ctx context.Context, taskID, instanceID string) error {
	script := `
		local key = ARGV[1]
		local instanceID = ARGV[2]

		local current = redis.call("GET", key)
		if current == false then
			return 1 -- Lock doesn't exist, that's fine
		end

		if current == instanceID then
			return redis.call("DEL", key)
		end

		return 1 -- Not our lock, don't release it
	`

	_, err := r.redisClient.Eval(ctx, script, []string{}, r.getTaskLockKey(taskID), instanceID)
	return err
}

// ExtendTaskLock extends the expiration time of an existing task lock
func (r *RedisStore) ExtendTaskLock(ctx context.Context, taskID, instanceID string, expiration time.Duration) error {
	script := `
		local key = ARGV[1]
		local instanceID = ARGV[2]
		local expiration = ARGV[3]

		local current = redis.call("GET", key)

		-- Check if lock exists and is held by the same instance
		if current == instanceID then
			-- Extend the expiration time
			redis.call("EXPIRE", key, expiration)
			return 1
		else
			return 0
		end
	`

	_, err := r.redisClient.Eval(ctx, script, []string{}, r.getTaskLockKey(taskID), instanceID, int64(expiration.Seconds()))
	return err
}

// AcquireShardLock tries to acquire a lock for a file shard
func (r *RedisStore) AcquireShardLock(ctx context.Context, taskID, shardID, instanceID, workerID string, expiration time.Duration) (bool, error) {
	key := r.getShardLockKey(taskID, shardID)
	return r.redisClient.SetNX(ctx, key, instanceID+":"+workerID, expiration)
}

// ExtendShardLock extends the expiration time of an existing file shard lock
func (r *RedisStore) ExtendShardLock(ctx context.Context, taskID, shardID, instanceID, workerID string, expiration time.Duration) error {
	script := `
		local key = ARGV[1]
		local instanceID = ARGV[2]
		local expiration = ARGV[3]

		local current = redis.call("GET", key)

		-- Check if lock exists and is held by the same instance
		if current == instanceID then
			-- Extend the expiration time
			redis.call("EXPIRE", key, expiration)
			return 1
		else
			return 0
		end
	`

	key := r.getShardLockKey(taskID, shardID)
	_, err := r.redisClient.Eval(ctx, script, []string{}, key, instanceID+":"+workerID, int64(expiration.Seconds()))
	return err
}

// AcquireOrRecoverShardLock atomically checks if a file shard lock exists and acquires it if:
// 1. No lock exists, or
// 2. The existing lock has expired (older than 5 minutes)
// Returns true if lock was acquired, false otherwise
func (r *RedisStore) AcquireOrRecoverShardLock(ctx context.Context, taskID, shardID, instanceID, workerID string, expiration time.Duration) (bool, error) {
	script := `
		local key = ARGV[1]
		local instanceID = ARGV[2]
		local expiration = ARGV[3]

		local current = redis.call("GET", key)

		-- If no lock exists, acquire it
		if not current or current == false then
			redis.call("SET", key, instanceID, "EX", expiration)
			return 1
		end

		-- If current instance already holds the lock, extend expiration
		if current == instanceID then
			redis.call("EXPIRE", key, expiration)
			return 1
		end

		-- Check if existing lock has expired (no TTL or TTL <= 0)
		local ttl = redis.call("TTL", key)
		if ttl <= 0 then
			-- Lock has expired, acquire it
			redis.call("SET", key, instanceID, "EX", expiration)
			return 1
		end

		-- Lock is still valid and held by another instance
		return 0
	`

	key := r.getShardLockKey(taskID, shardID)
	result, err := r.redisClient.Eval(ctx, script, []string{}, key, instanceID+":"+workerID, int64(expiration.Seconds()))
	if err != nil {
		return false, err
	}

	// Convert result to boolean
	if result == int64(1) {
		return true, nil
	}

	return false, nil
}

// ReleaseShardLock releases a file shard lock atomically
func (r *RedisStore) ReleaseShardLock(ctx context.Context, taskID, shardID, instanceID, workerID string) error {
	script := `
		local key = ARGV[1]
		local instanceID = ARGV[2]

		local current = redis.call("GET", key)
		if current == false then
			return 1 -- Lock doesn't exist, that's fine
		end

		if current == instanceID then
			return redis.call("DEL", key)
		end

		return 1 -- Not our lock, don't release it
	`

	_, err := r.redisClient.Eval(ctx, script, []string{}, r.getShardLockKey(taskID, shardID), instanceID+":"+workerID)
	return err
}

// GetTaskLockOwner returns the current owner of a task lock
func (r *RedisStore) GetTaskLockOwner(ctx context.Context, taskID string) (string, error) {
	key := r.getTaskLockKey(taskID)
	value, err := r.redisClient.Get(ctx, key)
	if err != nil {
		// Handle "redis: nil" error which occurs when key doesn't exist
		if err.Error() == "redis: nil" {
			return "", nil // No lock owner
		}
		return "", err
	}
	return value, nil
}

// GetShardLockOwner returns the current owner of a file shard lock
func (r *RedisStore) GetShardLockOwner(ctx context.Context, taskID, shardID string) (string, error) {
	key := r.getShardLockKey(taskID, shardID)
	value, err := r.redisClient.Get(ctx, key)
	if err != nil {
		// Handle "redis: nil" error which occurs when key doesn't exist
		if err.Error() == "redis: nil" {
			return "", nil // No lock owner
		}
		return "", err
	}
	return value, nil
}

// TaskInSet checks if a task is in a specific set
func (r *RedisStore) TaskInSet(ctx context.Context, set BatchTaskStatus, taskID string) (bool, error) {
	key := r.getTaskSetKey(set)
	return r.redisClient.SIsMember(ctx, key, taskID)
}

// AddToSet adds a task ID to a set
func (r *RedisStore) AddToSet(ctx context.Context, set BatchTaskStatus, taskID string) error {
	key := r.getTaskSetKey(set)
	return r.redisClient.SAdd(ctx, key, taskID)
}

// RemoveFromSet removes a task ID from a set
func (r *RedisStore) RemoveFromSet(ctx context.Context, set BatchTaskStatus, taskID string) error {
	key := r.getTaskSetKey(set)
	return r.redisClient.SRem(ctx, key, taskID)
}

// GetTaskInStatus returns one random task ID from a set
func (r *RedisStore) GetTaskInStatus(ctx context.Context, set BatchTaskStatus) (string, error) {
	key := r.getTaskSetKey(set)
	// Use SRandMember to get one random member
	members, err := r.redisClient.SRandMember(ctx, key, 1)
	if err != nil {
		return "", err
	}

	if len(members) == 0 {
		return "", fmt.Errorf("no tasks in set")
	}

	// Return the first (random) task
	return members[0], nil
}

func (r *RedisStore) RemoveShardInfo(ctx context.Context, taskID string, status ShardStatus) error {
	shardIDs, err := r.GetShardsInStatus(ctx, taskID, status)
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(shardIDs)+1)
	for _, shardID := range shardIDs {
		keys = append(keys, r.getShardKey(taskID, shardID))
	}

	keys = append(keys, r.getShardSetKey(taskID, status))

	return r.redisClient.Del(ctx, keys...)
}

func (r *RedisStore) getFileKey(fileID string) string {
	return fmt.Sprintf("file:%s", fileID)
}

// getFileSetKey returns the Redis key for the file set
func (r *RedisStore) getFileSetKey() string {
	return "files:all"
}

func (r *RedisStore) getTaskLockKey(taskID string) string {
	return fmt.Sprintf("task:lock:%s", taskID)
}

func (r *RedisStore) getShardLockKey(taskID, shardID string) string {
	return fmt.Sprintf("shard:lock:%s:%s", taskID, shardID)
}

// getTaskSetKey returns the Redis key for a given set name
func (r *RedisStore) getTaskSetKey(set BatchTaskStatus) string {
	return fmt.Sprintf("tasks:%s", string(set))
}

// getGlobalTaskSetKey returns the Redis key for the global task set
func (r *RedisStore) getGlobalTaskSetKey() string {
	return "tasks:all"
}

// getShardSetKey returns the Redis key for a given task's shard set with a specific status
func (r *RedisStore) getShardSetKey(taskID string, status ShardStatus) string {
	return fmt.Sprintf("task:%s:files:%s", taskID, string(status))
}

// getTaskKey returns the Redis key for a given task ID
func (r *RedisStore) getTaskKey(taskID string) string {
	return fmt.Sprintf("task:%s", taskID)
}

// getShardKey return the Redis key for give task ID and shard ID
func (r *RedisStore) getShardKey(taskID, ShardID string) string {
	return fmt.Sprintf("shard:%s:%s", taskID, ShardID)
}
