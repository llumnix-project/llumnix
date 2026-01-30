package batch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"llumnix/cmd/gateway/app/options"
	"math/rand"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"k8s.io/klog/v2"
)

var (
	errTaskStatusChanged = errors.New("task status changed")
)

// TaskReactor handles batch task processing
type TaskReactor struct {
	redisStore    *RedisStore
	ossClient     *OSSClient
	instanceID    string
	ossPrefix     string
	config        *options.GatewayConfig
	port          int
	linesPerShard int
}

// startTaskLockRenewalAndStatusMonitoring starts a goroutine to periodically renew task locks
// and monitor if task is still in the expected set
func (tr *TaskReactor) startTaskLockRenewalAndStatusMonitoring(
	ctx context.Context,
	taskID string,
	expectedStatus BatchTaskStatus,
	taskStatusChanged chan<- struct{},
	done <-chan struct{},
) {
	ticker := time.NewTicker(DefaultLockRenewalInterval)
	defer ticker.Stop()

	nextRenewLock := time.Now().Add(DefaultLockRenewalInterval)

	go func() {
		for {
			select {
			case <-ticker.C:
				if time.Now().After(nextRenewLock) {
					nextRenewLock = time.Now().Add(DefaultLockRenewalInterval)
					// Extend the task lock expiration
					err := tr.redisStore.ExtendTaskLock(ctx, taskID, tr.instanceID, DefaultLockExpiration)
					if err != nil {
						klog.Errorf("Failed to extend task lock for task %s: %v", taskID, err)
					} else {
						klog.V(4).Infof("Extended task lock for task %s", taskID)
					}
				}

				// Check if task is still in expected set
				inSet, err := tr.redisStore.TaskInSet(ctx, expectedStatus, taskID)
				if err != nil {
					klog.Errorf("Failed to check if task %s is in %s set: %v", taskID, expectedStatus, err)
					continue
				}

				if !inSet {
					klog.Infof("Task %s is no longer in %s set, stopping processing", taskID, expectedStatus)
					taskStatusChanged <- struct{}{}
					return
				}
			case <-done:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
}

// startShardLockRenewalAndStatusMonitoring starts a goroutine to periodically renew shard locks
// and monitor if task is still in the in_progress set
func (tr *TaskReactor) startShardLockRenewalAndStatusMonitoring(
	ctx context.Context,
	taskID string,
	shardID string,
	workerID string,
	expectedStatus BatchTaskStatus,
	taskStatusChanged chan<- struct{},
	done <-chan struct{},
) {
	ticker := time.NewTicker(DefaultStatusCheckInterval)
	defer ticker.Stop()

	nextRenewLock := time.Now().Add(DefaultLockRenewalInterval)

	go func() {
		for {
			select {
			case <-ticker.C:
				if time.Now().After(nextRenewLock) {
					nextRenewLock = time.Now().Add(DefaultLockRenewalInterval)
					// Extend the shard lock expiration
					err := tr.redisStore.ExtendShardLock(ctx, taskID, shardID, tr.instanceID, workerID, DefaultLockExpiration)
					if err != nil {
						klog.Errorf("Failed to extend file lock for shard %s of task %s: %v", shardID, taskID, err)
					} else {
						klog.V(4).Infof("Extended file lock for shard %s of task %s", shardID, taskID)
					}
				}

				// Check if task is still in in_progress set
				inSet, err := tr.redisStore.TaskInSet(ctx, expectedStatus, taskID)
				if err != nil {
					klog.Errorf("Failed to check if task %s is in %s set: %v", taskID, expectedStatus, err)
					continue
				}

				if !inSet {
					klog.Infof("Task %s is no longer in %s set, stopping shard processing", taskID, expectedStatus)
					taskStatusChanged <- struct{}{}
					return
				}
			case <-done:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
}

// NewTaskReactor creates a new task reactor
func NewTaskReactor(redisStore *RedisStore, ossClient *OSSClient, instanceID, ossPrefix string, config *options.GatewayConfig) *TaskReactor {
	return &TaskReactor{
		redisStore: redisStore,
		ossClient:  ossClient,
		instanceID: instanceID,
		ossPrefix:  ossPrefix,
		config:     config,
	}
}

// ValidatingTaskReactor handles tasks in the validating phase
func (tr *TaskReactor) ValidatingTaskReactor(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			// Process a single validating task with proper lock management
			tr.processSingleValidatingTask(ctx)
		}
	}
}

// processSingleValidatingTask handles processing of a single validating task with proper lock management
func (tr *TaskReactor) processSingleValidatingTask(ctx context.Context) {
	var taskID string

	// Try to get a task from pending set
	taskID, err := tr.redisStore.GetTaskInStatus(ctx, StatusPending)
	if err != nil {
		// If no pending tasks, check validating tasks for failover
		recoveredTaskID := tr.handleValidatingFailover(ctx)
		if recoveredTaskID != "" {
			// Use the recovered task ID (lock already acquired)
			taskID = recoveredTaskID
		} else {
			time.Sleep(5 * time.Second)
			return
		}
	} else {
		// Try to acquire lock for the task
		acquired, err := tr.redisStore.AcquireTaskLock(ctx, taskID, tr.instanceID, DefaultLockExpiration)
		if err != nil || !acquired {
			time.Sleep(1 * time.Second)
			return
		}
	}

	// Ensure lock is released if we acquired it
	defer tr.redisStore.ReleaseTaskLock(ctx, taskID, tr.instanceID)

	// Process the task in validating phase
	if err := tr.processValidatingTask(ctx, taskID); err != nil {
		klog.Errorf("Failed to process validating task %s: %v", taskID, err)
	}
}

// processValidatingTask processes a task in the validating phase
func (tr *TaskReactor) processValidatingTask(ctx context.Context, taskID string) error {
	klog.Infof("Processing validating task: %s", taskID)

	task, err := tr.redisStore.GetTask(ctx, taskID)
	if err != nil {
		return fmt.Errorf("failed to get task: %v", err)
	}

	// If task is already in validating status, we don't need to move it
	// This happens when recovering a stale validating task
	if task.Status != StatusValidating {
		// Move task from pending to validating set and update status atomically
		if err := tr.redisStore.MoveTaskAndUpdateStatusAtomically(ctx, taskID, StatusPending, StatusValidating); err != nil {
			return fmt.Errorf("failed to move task to validating set and update status atomically: %v", err)
		}
	} else {
		// clean up for failover task
		if err := tr.cleanupTaskShardMeta(ctx, taskID); err != nil {
			return fmt.Errorf("failed to cleanup task %s shard info: %v", taskID, err)
		}
	}

	done := make(chan struct{}, 1)
	defer func() { done <- struct{}{} }()
	taskStatusChanged := make(chan struct{}, 1)
	tr.startTaskLockRenewalAndStatusMonitoring(ctx, taskID, StatusValidating, taskStatusChanged, done)

	inputFileLines, err := tr.ossClient.ReadLines(ctx, tr.getFileOSSPath(task.InputFileID))
	if err != nil {
		return err
	}

	if err := tr.validateInputFile(ctx, taskID, inputFileLines, task.InputFileID); err != nil {
		return err
	}

	// Do real shard job
	lineCount, _, err := tr.shardInputFile(ctx, taskID, inputFileLines, taskStatusChanged)
	if err != nil {
		// If the error is due to task status change, return nil to stop processing
		if errors.Is(err, errTaskStatusChanged) {
			klog.Infof("Task %s status changed during validation, stopping processing", taskID)
			return nil
		}
		return fmt.Errorf("failed to shard input file: %v", err)
	}

	// Update task with request counts
	task.RequestCounts = &RequestCounts{
		Total:     lineCount,
		Completed: 0,
		Failed:    0,
	}
	now := time.Now().Unix()
	task.UpdatedAt = &now

	// Save updated task fields to Redis
	if err := tr.redisStore.UpdateTaskFields(ctx, taskID, task); err != nil {
		klog.Errorf("Failed to update task fields: %v", err)
		// Continue processing even if field update fails
	}

	// Move task to in_progress status and update status atomically
	if err := tr.redisStore.MoveTaskAndUpdateStatusAtomically(ctx, taskID, StatusValidating, StatusInProgress); err != nil {
		return fmt.Errorf("failed to move task to in_progress set and update status atomically: %v", err)
	}

	klog.Infof("Finished validating task: %s", taskID)
	return nil
}

// validateInputFile validates the input file format before processing
func (tr *TaskReactor) validateInputFile(ctx context.Context, taskID string, fileLines [][]byte, fileID string) error {
	if err := tr.ValidateBatchFile(ctx, fileLines); err != nil {
		klog.Errorf("Failed to validate batch file %s for task %s: %v", fileID, taskID, err)

		batchErr := &BatchErrors{
			Data: []BatchError{
				{
					Code:    "validation_error",
					Message: err.Error(),
				},
			},
		}

		// Update task in Redis with failure information
		if updateErr := tr.redisStore.UpdateTaskFailure(ctx, taskID, batchErr); updateErr != nil {
			klog.Errorf("Failed to update task %s status to failed: %v", taskID, updateErr)
		}

		// Move task from validating to failed set and update status atomically
		if moveErr := tr.redisStore.MoveTaskAtomically(ctx, taskID, StatusValidating, StatusFailed); moveErr != nil {
			klog.Errorf("Failed to move task %s from validating to failed set: %v", taskID, moveErr)
		}

		return fmt.Errorf("batch file validation failed: %v", err)
	}
	return nil
}

// shardInputFile shards the input file and creates Redis records for each shard
func (tr *TaskReactor) shardInputFile(ctx context.Context, taskID string, inputFileLines [][]byte, taskStatusChanged <-chan struct{}) (lineCount int, shardCount int, err error) {
	shardIndex := 0
	var currentShardLines [][]byte

	for _, line := range inputFileLines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		select {
		case <-taskStatusChanged:
			klog.Infof("Task %s is no longer in in_progress status, stopping shard processing", taskID)
			err = errTaskStatusChanged
			return
		default:
		}

		currentShardLines = append(currentShardLines, line)
		lineCount++

		// If we've reached the lines per shard limit, create a shard
		if len(currentShardLines) >= tr.config.BatchLinesPerShard {
			if err = tr.createFileShard(ctx, taskID, strconv.Itoa(shardIndex), currentShardLines); err != nil {
				return
			}

			shardIndex++
			// Reset for next shard
			currentShardLines = nil
		}
	}

	// Handle remaining lines in the last shard (if any)
	if len(currentShardLines) > 0 {
		if err = tr.createFileShard(ctx, taskID, strconv.Itoa(shardIndex), currentShardLines); err != nil {
			return
		}
		shardIndex++
	}

	shardCount = shardIndex

	return lineCount, shardCount, nil
}

// createFileShard creates Redis records for a shard without using a callback
func (tr *TaskReactor) createFileShard(ctx context.Context, taskID, shardID string, lines [][]byte) error {
	if err := tr.ossClient.WriteLines(ctx, tr.getShardFileInputOSSPath(taskID, shardID), lines); err != nil {
		return fmt.Errorf("failed to write shard file to OSS: %v", err)
	}

	now := time.Now().Unix()
	shard := &FileShard{
		ID:            shardID,
		CreatedAt:     &now,
		RequestCounts: &RequestCounts{Total: len(lines), Completed: 0, Failed: 0},
		UpdatedAt:     &now,
	}

	// Create shard record
	if err := tr.redisStore.CreateOrUpdateFileShard(ctx, taskID, shardID, shard); err != nil {
		return fmt.Errorf("failed to create file shard: %v", err)
	}

	// Add shard to pending list
	if err := tr.redisStore.AddShardToPendingList(ctx, taskID, shardID); err != nil {
		return fmt.Errorf("failed to add shard to pending list: %v", err)
	}

	return nil
}

// InProcessTaskReactor handles tasks in the in_progress phase
func (tr *TaskReactor) InProcessTaskReactor(ctx context.Context, workerID string) {
	// Random sleep
	time.Sleep(time.Duration(rand.Intn(3000)) * time.Millisecond)

	for {
		select {
		case <-ctx.Done():
			return
		default:
			// Process a single task with proper lock management
			tr.processSingleInProgressTask(ctx, workerID)
		}
	}
}

// CheckShardsAndMoveToFinalizing checks if all shards are completed and moves the task to finalizing atomically
func (tr *TaskReactor) CheckShardsAndMoveToFinalizing(ctx context.Context, taskID string) (bool, error) {
	// First check if all shards are completed
	allCompleted, err := tr.redisStore.CheckIfAllShardsAreCompleted(ctx, taskID)
	if err != nil {
		return false, err
	}

	if !allCompleted {
		return false, nil
	}

	acquired, err := tr.redisStore.AcquireTaskLock(ctx, taskID, tr.instanceID, DefaultLockExpiration)
	if err != nil || !acquired {
		return false, nil
	}

	defer tr.redisStore.ReleaseTaskLock(ctx, taskID, tr.instanceID)

	// If all shards are completed, move task from in_progress to finalizing
	err = tr.redisStore.MoveTaskAndUpdateStatusAtomically(ctx, taskID, StatusInProgress, StatusFinalizing)
	if err != nil {
		return false, err
	}

	return true, nil
}

// processSingleInProgressTask handles processing of a single in-progress task with proper lock management
func (tr *TaskReactor) processSingleInProgressTask(ctx context.Context, workerID string) {
	// Get a task from in_progress set
	taskID, err := tr.redisStore.GetTaskInStatus(ctx, StatusInProgress)
	if err != nil {
		time.Sleep(5 * time.Second)
		return
	}

	// Check that task is not also in validating set (to avoid abnormal conditions)
	inValidating, err := tr.redisStore.TaskInSet(ctx, StatusValidating, taskID)
	if err != nil || inValidating {
		time.Sleep(1 * time.Second)
		return
	}

	// Process shards for this task
	if err := tr.processTaskShards(ctx, taskID, workerID); err != nil {
		klog.Errorf("Failed to process task shards for %s: %v", taskID, err)
		return
	}

	// Check if all shards are completed and move to finalizing atomically
	moved, err := tr.CheckShardsAndMoveToFinalizing(ctx, taskID)
	if err != nil {
		klog.Errorf("Failed to check shards and move task to finalizing: %v", err)
		return
	}

	if moved {
		klog.Infof("Task %s moved to finalizing", taskID)
	} else {
		time.Sleep(time.Second)
	}
}

// processTaskShards processes shards for a task
func (tr *TaskReactor) processTaskShards(ctx context.Context, taskID, workerID string) error {
	var shardID string
	var needToMoveToInProgress bool

	// First, try to get a random pending shard for this task
	shardID, err := tr.redisStore.GetShardInStatus(ctx, taskID, ShardStatusPending)
	if err == nil {
		needToMoveToInProgress = true
	} else {
		// If no pending shards, check for in-progress shards with expired locks
		shardID, err = tr.handleInProgressShardFailover(ctx, taskID, workerID)
		if err != nil {
			klog.Errorf("Failed to handle in-progress shard failover for task %s: %v", taskID, err)
			return nil
		}
		if shardID == "" {
			// If no pending shards and no recovered shards, return nil to continue processing other tasks
			return nil
		}
		// For recovered shards, we don't need to move to in_progress
		// But we do need to release the lock acquired by handleInProgressShardFailover
		needToMoveToInProgress = false
	}

	// For pending shards, we need to acquire a file lock before processing
	if needToMoveToInProgress {
		// Acquire file lock for the shard
		acquired, err := tr.redisStore.AcquireShardLock(ctx, taskID, shardID, tr.instanceID, workerID, DefaultLockExpiration)
		if err != nil || !acquired {
			klog.Warningf("Failed to acquire lock for pending shard %s of task %s", shardID, taskID)
			time.Sleep(1 * time.Second)
			return nil
		}

		if err := tr.redisStore.MoveShardAtomically(ctx, taskID, shardID, ShardStatusPending, ShardStatusInProgress); err != nil {
			// Release the lock if we fail to move the shard
			tr.redisStore.ReleaseShardLock(ctx, taskID, shardID, tr.instanceID, workerID)
			return fmt.Errorf("failed to move shard %s of task %s to in_progress: %v", shardID, taskID, err)
		}
	} else {
		// Already acquired shard lock
		// Check if task is still in in_progress set before processing
		inProgress, err := tr.redisStore.TaskInSet(ctx, StatusInProgress, taskID)
		if err != nil {
			klog.Errorf("Failed to check if task %s is in in_progress set: %v", taskID, err)
			return nil
		}
		if !inProgress {
			klog.Infof("Task %s is no longer in in_progress set, aborting shard %s processing", taskID, shardID)
			return nil
		}
	}

	// Ensure lock is released when we're done (for both pending and recovered shards)
	defer tr.redisStore.ReleaseShardLock(ctx, taskID, shardID, tr.instanceID, workerID)

	// Process the shard
	if err := tr.processShard(ctx, taskID, shardID, workerID); err != nil {
		if errors.Is(err, errTaskStatusChanged) {
			return tr.redisStore.MoveShardAtomically(ctx, taskID, shardID, ShardStatusInProgress, ShardStatusFailed)
		}

		return fmt.Errorf("failed to process shard %s of task %s: %v", shardID, taskID, err)
	}

	// Move shard to completed status
	if err := tr.redisStore.MoveShardAtomically(ctx, taskID, shardID, ShardStatusInProgress, ShardStatusCompleted); err != nil {
		return fmt.Errorf("failed to move shard %s of task %s to completed: %v", shardID, taskID, err)
	} else {
		klog.Infof("Successfully moved shard %s of task %s to completed status", shardID, taskID)
	}

	return nil
}

// processRequest processes a single request line and returns the response
func (tr *TaskReactor) processRequest(ctx context.Context, httpClient *http.Client, line []byte, task *BatchTask, taskID string) ([]byte, bool) {
	fullURL := fmt.Sprintf("http://127.0.0.1:%d%s", tr.config.Port, task.Endpoint)
	requestID := uuid.New().String()
	var request SingleLineRequest
	var resp *http.Response
	var req *http.Request
	var respBody []byte
	var errMessage string
	var err error
	maxRetries := tr.config.BatchRequestRetryTimes

	if err := json.Unmarshal(line, &request); err != nil {
		klog.Errorf("Unexpected JSON parsing error for validated file %s: %v", line, err)
		// Since the file was already validated, this should not happen, but handle gracefully
		// Try to extract custom_id even if the full parsing fails

		// Extract just the custom_id field from the JSON
		var customIDMap map[string]any
		if mapErr := json.Unmarshal(line, &customIDMap); mapErr == nil {
			if id, ok := customIDMap["custom_id"].(string); ok {
				request.CustomID = id
			}
		}

		errMessage = fmt.Sprintf("unexpected parsing error: %v", err)
		goto doneRequest
	}

	// Send request to the LLM service with retry logic
	req, err = http.NewRequestWithContext(ctx, "POST", fullURL, bytes.NewReader(request.Body))
	if err != nil {
		errMessage = fmt.Sprintf("failed to create request: %v", err)
		goto doneRequest
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-request-id", requestID)
	req.Header.Set("Authorization", "Bearer "+*task.Token)

	// Retry logic for HTTP requests
	for retryCount := range maxRetries + 1 {
		resp, err = httpClient.Do(req)
		if err == nil && resp.StatusCode < 500 {
			// Success - no retry needed
			break
		}

		// If we've exhausted retries, break and handle the error
		if retryCount == maxRetries {
			break
		}

		// Log retry attempt
		if err != nil {
			klog.Warningf("HTTP request failed (attempt %d/%d): %v", retryCount+1, maxRetries+1, err)
		} else {
			klog.Warningf("HTTP request failed with status %d (attempt %d/%d)", resp.StatusCode, retryCount+1, maxRetries+1)
			resp.Body.Close() // Close body before retrying
		}

		// Exponential backoff
		backoff := time.Duration(100*(1<<uint(retryCount))) * time.Millisecond
		time.Sleep(backoff)

		// Reset request body for retry (as it may have been consumed)
		req.Body = io.NopCloser(bytes.NewReader(request.Body))
	}

	if err != nil {
		errMessage = err.Error()
		goto doneRequest
	}

	// Check for HTTP error status codes (5xx)
	if resp.StatusCode >= 500 {
		errMessage = fmt.Sprintf("server error: %d", resp.StatusCode)
		goto doneRequest
	}

	defer resp.Body.Close()

	// TODO: use sync.Pool
	// Read response body
	respBody, err = io.ReadAll(resp.Body)
	if err != nil {
		errMessage = fmt.Sprintf("failed to read response: %v", err)
		goto doneRequest
	}

doneRequest:
	responseLine, batchError := tr.createResponse(taskID, request.CustomID, requestID, resp, respBody, errMessage)

	// Return the response line and whether it's an error
	isError := batchError != nil

	return responseLine, isError
}

// createResponse creates a response object and marshals it to JSON
func (tr *TaskReactor) createResponse(taskID, customID, requestID string, resp *http.Response, respBody []byte, errMessage string) ([]byte, *BatchRequestError) {
	var statusCode int
	if resp != nil {
		statusCode = resp.StatusCode
	}
	response := SingleLineResponse{
		ID:       taskID,
		CustomID: customID,
		Response: &RespData{
			StatusCode: statusCode,
			RequestID:  requestID,
			Body:       json.RawMessage(respBody),
		},
		Error: nil,
	}

	var batchError *BatchRequestError
	if errMessage != "" {
		batchError = &BatchRequestError{
			Code:    "request_error",
			Message: errMessage,
		}
		response.Error = batchError
	}

	responseLine, marshalErr := json.Marshal(response)
	if marshalErr != nil {
		klog.Errorf("Failed to marshal successful response: %v", marshalErr)
		errMessage = fmt.Sprintf("failed to marshal response: %v", marshalErr)
		batchError = &BatchRequestError{
			Code:    "marshal_error",
			Message: errMessage,
		}
		response.Error = batchError
		response.Response = nil
		responseLine, _ = json.Marshal(response)
	}

	return responseLine, batchError
}
func (tr *TaskReactor) processShard(ctx context.Context, taskID, shardID, workerID string) error {
	klog.Infof("Processing shard %s for task %s", shardID, taskID)

	// Verify task is still in in_progress set
	inProgress, err := tr.redisStore.TaskInSet(ctx, StatusInProgress, taskID)
	if err != nil {
		return fmt.Errorf("failed to check if task is in in_progress set: %v", err)
	}
	if !inProgress {
		klog.Infof("Task %s is no longer in in_progress set, skipping shard processing", taskID)
		return nil
	}

	// Get task details to get the endpoint
	task, err := tr.redisStore.GetTask(ctx, taskID)
	if err != nil {
		return fmt.Errorf("failed to get task: %v", err)
	}

	// Clean existing files on OSS to avoid issues when we only have one of output or error file (Cannot override existing)
	outputPath := tr.getShardFileOutputOSSPath(taskID, shardID)
	errorPath := tr.getShardFileOutputErrorOSSPath(taskID, shardID)
	if err := tr.ossClient.DeleteFiles(ctx, []string{outputPath, errorPath}); err != nil {
		return fmt.Errorf("failed to clean files (%s, %s) on OSS: %v", outputPath, errorPath, err)
	}

	// Read the shard file from OSS
	shardPath := tr.getShardFileInputOSSPath(taskID, shardID)
	lines, err := tr.ossClient.ReadLines(ctx, shardPath)
	if err != nil {
		return fmt.Errorf("failed to read shard file from OSS: %v", err)
	}

	// Create buffers for output and error files
	var outputLines, errorLines [][]byte
	successCount := 0
	failureCount := 0

	// Process each request line by line
	// Use a shared HTTP client with connection pooling for better performance
	httpClient := &http.Client{
		Timeout: tr.config.BatchRequestTimeout,
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
		},
	}

	// Channel to signal when processing is done
	done := make(chan struct{}, 1)
	defer func() { done <- struct{}{} }()

	// Channel to signal when task status changes
	taskStatusChanged := make(chan struct{}, 1)

	// Start shard lock renewal and status monitoring goroutine using common function
	tr.startShardLockRenewalAndStatusMonitoring(ctx, taskID, shardID, workerID, StatusInProgress, taskStatusChanged, done)

	for _, line := range lines {
		// Check if task status has changed in the lock renewal goroutine
		select {
		case <-taskStatusChanged:
			klog.Infof("Task %s is no longer in in_progress status, stopping shard processing", taskID)
			return errTaskStatusChanged
		default:
			// Continue processing
		}

		if len(line) == 0 {
			continue
		}

		responseLine, isError := tr.processRequest(ctx, httpClient, line, task, taskID)

		if isError {
			errorLines = append(errorLines, responseLine)
			failureCount++
		} else {
			outputLines = append(outputLines, responseLine)
			successCount++
		}
	}

	// Write output file to OSS
	if len(outputLines) > 0 {
		if err := tr.ossClient.WriteLines(ctx, outputPath, outputLines); err != nil {
			return fmt.Errorf("failed to write output file to OSS: %v", err)
		}
	}
	if len(errorLines) > 0 {
		if err := tr.ossClient.WriteLines(ctx, errorPath, errorLines); err != nil {
			return fmt.Errorf("failed to write output error file to OSS: %v", err)
		}
	}

	// Update shard progress
	shard, err := tr.redisStore.GetFileShard(ctx, taskID, shardID)
	if err != nil {
		return fmt.Errorf("failed to get shard: %v", err)
	}

	// Initialize RequestCounts if it's nil
	if shard.RequestCounts == nil {
		shard.RequestCounts = &RequestCounts{}
	}
	shard.RequestCounts.Total = successCount + failureCount
	shard.RequestCounts.Completed = successCount
	shard.RequestCounts.Failed = failureCount

	now := time.Now().Unix()
	shard.UpdatedAt = &now

	if err := tr.redisStore.CreateOrUpdateFileShard(ctx, taskID, shardID, shard); err != nil {
		return fmt.Errorf("failed to update shard: %v", err)
	}

	klog.Infof("Finished processing shard %s for task %s: %d success, %d failures", shardID, taskID, successCount, failureCount)
	return nil
}

// handleInProgressShardFailover checks for in-progress shards with expired locks and recovers them
func (tr *TaskReactor) handleInProgressShardFailover(ctx context.Context, taskID, workerID string) (string, error) {
	// Get all in-progress shards for this task
	shardIDs, err := tr.redisStore.GetShardsInStatus(ctx, taskID, ShardStatusInProgress)
	if err != nil {
		return "", fmt.Errorf("failed to get in-progress shards: %v", err)
	}

	// Shuffle the shard IDs to ensure better distribution across instances
	// This helps avoid multiple instances trying to recover the same shards
	rand.Shuffle(len(shardIDs), func(i, j int) {
		shardIDs[i], shardIDs[j] = shardIDs[j], shardIDs[i]
	})

	// Check each in-progress shard for expired locks using atomic operation
	for _, shardID := range shardIDs {
		// Atomically check if lock exists and acquire it if:
		// 1. No lock exists
		// 2. Current instance already holds the lock
		// 3. Existing lock has expired
		acquired, err := tr.redisStore.AcquireOrRecoverShardLock(ctx, taskID, shardID, tr.instanceID, workerID, DefaultLockExpiration)
		if err != nil {
			klog.Errorf("Failed to check/acquire lock for in-progress shard %s of task %s: %v", shardID, taskID, err)
			continue
		}

		if acquired {
			klog.Infof("Successfully acquired lock for in-progress shard %s of task %s", shardID, taskID)
			return shardID, nil
		}
	}

	// No recoverable shards found
	return "", nil
}

// FinalizeTaskReactor handles tasks in the finalizing phase
func (tr *TaskReactor) FinalizeTaskReactor(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			// Process a single finalizing task with proper lock management
			tr.processSingleFinalizingTask(ctx)
		}
	}
}

// processSingleFinalizingTask handles processing of a single finalizing task with proper lock management
func (tr *TaskReactor) processSingleFinalizingTask(ctx context.Context) {
	// Get a task from finalizing set
	taskID, err := tr.redisStore.GetTaskInStatus(ctx, StatusFinalizing)
	if err != nil {
		time.Sleep(5 * time.Second)
		return
	}

	// Try to acquire lock for the task
	acquired, err := tr.redisStore.AcquireTaskLock(ctx, taskID, tr.instanceID, DefaultLockExpiration)
	if err != nil || !acquired {
		time.Sleep(1 * time.Second)
		return
	}

	// Ensure lock is released when function exits
	defer tr.redisStore.ReleaseTaskLock(ctx, taskID, tr.instanceID)

	// Process the task in finalizing phase
	if err := tr.processFinalizingTask(ctx, taskID); err != nil {
		klog.Errorf("Failed to process finalizing task %s: %v", taskID, err)
	}

	if err := tr.cleanupTaskShardMeta(ctx, taskID); err != nil {
		klog.Errorf("Failed to clean task %s shard meta info: %v", taskID, err)
	}
}

// CancelTaskReactor handles tasks in the cancelling phase
func (tr *TaskReactor) CancelTaskReactor(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			// Process a single cancelling task with proper lock management
			tr.processSingleCancellingTask(ctx)
		}
	}
}

// ExpireTaskReactor handles tasks that have expired
func (tr *TaskReactor) ExpireTaskReactor(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			// Process expired tasks
			tr.processExpiredTasks(ctx)
			// Check for expired tasks every minute
			time.Sleep(1 * time.Minute)
		}
	}
}

// processExpiredTasks checks for and processes expired tasks
func (tr *TaskReactor) processExpiredTasks(ctx context.Context) {
	// Check for expired tasks in the following sets:
	// - pending: Tasks that were created but never started
	// - validating: Tasks that started validation but didn't complete
	// - in_progress: Tasks that started processing but didn't complete
	// - finalizing: Tasks that started finalization but didn't complete
	expiredTaskIDs, err := tr.redisStore.GetExpiredTasks(ctx, StatusPending, StatusValidating, StatusInProgress, StatusFinalizing)
	if err != nil {
		klog.Errorf("Failed to get expired tasks: %v", err)
		return
	}

	// Process each expired task
	for _, taskID := range expiredTaskIDs {
		if err := tr.processExpiredTask(ctx, taskID); err != nil {
			klog.Errorf("Failed to process expired task %s: %v", taskID, err)
		}
	}
}

// processExpiredTask moves an expired task to the expired status
func (tr *TaskReactor) processExpiredTask(ctx context.Context, taskID string) error {
	klog.Infof("Processing expired task: %s", taskID)

	acquired, err := tr.redisStore.AcquireTaskLock(ctx, taskID, tr.instanceID, DefaultLockExpiration)
	if err != nil || !acquired {
		return fmt.Errorf("failed to lock task")
	}

	defer tr.redisStore.ReleaseTaskLock(ctx, taskID, tr.instanceID)

	taskStatus, err := tr.redisStore.GetTaskStatus(ctx, taskID)
	if err != nil {
		return fmt.Errorf("failed to get task %s stats: %v", taskID, err)
	}

	// Move task to expired status atomically based on current status
	if err := tr.redisStore.MoveTaskAndUpdateStatusAtomically(ctx, taskID, taskStatus, StatusExpired); err != nil {
		return fmt.Errorf("failed to move task from %s to %s: %v", taskStatus, StatusExpired, err)
	}

	klog.Infof("Moved expired task %s from %s to %s", taskID, taskStatus, StatusExpired)
	return nil
}

// processSingleCancellingTask handles processing of a single cancelling task with proper lock management
func (tr *TaskReactor) processSingleCancellingTask(ctx context.Context) {
	// Get a task from cancelling set
	taskID, err := tr.redisStore.GetTaskInStatus(ctx, StatusCancelling)
	if err != nil {
		time.Sleep(5 * time.Second)
		return
	}

	// Try to acquire lock for the task
	acquired, err := tr.redisStore.AcquireTaskLock(ctx, taskID, tr.instanceID, DefaultLockExpiration)
	if err != nil || !acquired {
		time.Sleep(1 * time.Second)
		return
	}

	// Ensure lock is released when function exits
	defer tr.redisStore.ReleaseTaskLock(ctx, taskID, tr.instanceID)

	// Check that task is not also in validating or in_progress sets
	// These checks are done after acquiring the lock to prevent race conditions
	inValidating, err := tr.redisStore.TaskInSet(ctx, StatusValidating, taskID)
	if err != nil {
		klog.Errorf("Failed to check if task %s is in validating set: %v", taskID, err)
		time.Sleep(1 * time.Second)
		return
	}

	inProgress, err := tr.redisStore.TaskInSet(ctx, StatusInProgress, taskID)
	if err != nil {
		klog.Errorf("Failed to check if task %s is in progress set: %v", taskID, err)
		time.Sleep(1 * time.Second)
		return
	}

	// If task is still in validating or in_progress sets, skip it
	if inValidating || inProgress {
		klog.Infof("Task %s is still in validating or in_progress set, skipping cancellation", taskID)
		time.Sleep(1 * time.Second)
		return
	}

	// Process the task cancellation
	if err := tr.processCancellingTask(ctx, taskID); err != nil {
		klog.Errorf("Failed to process cancelling task %s: %v", taskID, err)
	}
}

// processFinalizingTask processes a task in the finalizing phase
func (tr *TaskReactor) processFinalizingTask(ctx context.Context, taskID string) error {
	klog.Infof("Processing finalizing task: %s", taskID)

	// Move task from finalizing to finalize set and update status atomically
	if err := tr.redisStore.MoveTaskAndUpdateStatusAtomically(ctx, taskID, StatusFinalizing, StatusFinalize); err != nil {
		return fmt.Errorf("failed to move task to finalize set: %v", err)
	}

	// Channel to signal when processing is done
	done := make(chan struct{}, 1)
	defer func() { done <- struct{}{} }()

	// Channel to signal when task status changes
	taskStatusChanged := make(chan struct{}, 1)

	// Start task lock renewal and status monitoring goroutine using common function
	tr.startTaskLockRenewalAndStatusMonitoring(ctx, taskID, StatusFinalize, taskStatusChanged, done)

	// Get task details
	task, err := tr.redisStore.GetTask(ctx, taskID)
	if err != nil {
		return fmt.Errorf("failed to get task: %v", err)
	}

	// Get all completed shards
	shardIDs, err := tr.redisStore.GetShardsInStatus(ctx, taskID, ShardStatusCompleted)
	if err != nil {
		return fmt.Errorf("failed to get completed shards: %v", err)
	}

	shardOutputPrefix := tr.getShardFileOutputOSSPrefix(taskID)
	shardOutputKeys, err := tr.ossClient.ListObjectsWithPrefix(ctx, shardOutputPrefix)
	if err != nil {
		return fmt.Errorf("failed to list prefix %s: %v", shardOutputPrefix, err)
	}

	outputFileID := "batch_output_" + uuid.New().String()
	errorFileID := "batch_error_" + uuid.New().String()
	outputPath := tr.getFileOSSPath(outputFileID)
	errorPath := tr.getFileOSSPath(errorFileID)

	var outputPosition, errorPosition int64
	var requestCounts RequestCounts

	shardOutputKeySet := make(map[string]struct{})
	for _, key := range shardOutputKeys {
		shardOutputKeySet[key] = struct{}{}
	}

	// delete oss object outputPath and errorPath if they exist
	err = tr.ossClient.DeleteFiles(ctx, []string{outputPath, errorPath})
	// ignore not found error
	if err != nil && !strings.Contains(err.Error(), "NoSuchKey") {
		return fmt.Errorf("failed to delete %s and %s: %v", outputPath, errorPath, err)
	}

	for _, shardID := range shardIDs {
		// Check if task status has changed
		select {
		case <-taskStatusChanged:
			klog.Infof("Task %s is no longer in finalize status, stopping finalization processing", taskID)
			return nil
		default:
			// Continue processing
		}

		// Read shard result file
		found := false
		shardOutputPath := tr.getShardFileOutputOSSPath(taskID, shardID)
		shardErrorPath := tr.getShardFileOutputErrorOSSPath(taskID, shardID)
		if _, ok := shardOutputKeySet[shardOutputPath]; ok {
			nextOutputPosition, err := tr.ossClient.AppendToObject(ctx, shardOutputPath, outputPath, outputPosition)
			if err != nil {
				klog.Errorf("Failed to read shard output file %s: %v", shardOutputPath, err)
				return fmt.Errorf("failed to read shard output file %s: %v", shardOutputPath, err)
			}
			outputPosition = nextOutputPosition
			found = true
		}
		if _, ok := shardOutputKeySet[shardErrorPath]; ok {
			nextErrorPosition, err := tr.ossClient.AppendToObject(ctx, shardErrorPath, errorPath, errorPosition)
			if err != nil {
				klog.Errorf("Failed to read shard output error file %s: %v", shardErrorPath, err)
				return fmt.Errorf("failed to read shard output error file %s: %v", shardErrorPath, err)
			}
			errorPosition = nextErrorPosition
			found = true
		}

		if !found {
			klog.Warningf("No output or error file found for shard %s", shardID)
		}

		shard, err := tr.redisStore.GetFileShard(ctx, taskID, shardID)
		if err != nil {

		}

		requestCounts.Total += shard.RequestCounts.Total
		requestCounts.Completed += shard.RequestCounts.Completed
		requestCounts.Failed += shard.RequestCounts.Failed
	}

	// Check if task status has changed before finalizing
	select {
	case <-taskStatusChanged:
		klog.Infof("Task %s is no longer in finalize status, stopping finalization processing", taskID)
		return nil
	default:
		// Continue processing
	}

	// Update task with completion information
	now := time.Now().Unix()
	task.CompletedAt = &now
	task.OutputFileID = &outputFileID
	task.UpdatedAt = &now
	task.Status = StatusCompleted
	task.RequestCounts = &requestCounts

	if errorPosition > 0 {
		task.ErrorFileID = &errorFileID
	}

	var expireAt int64
	if task.OutputExpiresAfter != nil {
		expireAt = now + task.OutputExpiresAfter.Seconds
	}

	// Create file record for OutputFileID
	outputFile := &File{
		ID:        outputFileID,
		Object:    "file",
		Bytes:     outputPosition, // position is equal to object length
		CreatedAt: now,
		ExpiresAt: expireAt,
		Filename:  "output.jsonl",
		Purpose:   "batch_output",
	}
	if err := tr.redisStore.CreateFile(ctx, outputFile); err != nil {
		return fmt.Errorf("failed to create output file record: %v", err)
	}

	// Create file record for ErrorFileID if it exists
	if errorPosition > 0 {
		errorFile := &File{
			ID:        errorFileID,
			Object:    "file",
			Bytes:     errorPosition,
			CreatedAt: now,
			ExpiresAt: expireAt,
			Filename:  "error.jsonl",
			Purpose:   "batch_output",
		}
		if err := tr.redisStore.CreateFile(ctx, errorFile); err != nil {
			return fmt.Errorf("failed to create error file record: %v", err)
		}
	}

	if err := tr.redisStore.UpdateTaskFields(ctx, taskID, task); err != nil {
		return fmt.Errorf("failed to update task fields and status: %v", err)
	}

	// Remove from finalize set
	if err := tr.redisStore.MoveTaskAtomically(ctx, taskID, StatusFinalize, StatusCompleted); err != nil {
		klog.Warningf("Failed to remove task from finalize set: %v", err)
	}

	klog.Infof("Finished finalizing task: %s with %d output bytes", taskID, outputPosition)
	return nil
}

// processCancellingTask processes a task in the cancelling phase
func (tr *TaskReactor) processCancellingTask(ctx context.Context, taskID string) error {
	klog.Infof("Processing cancelling task: %s", taskID)

	// Set up periodic keep-alive
	keepAliveInterval := 5 * time.Second
	keepAliveTicker := time.NewTicker(keepAliveInterval)
	defer keepAliveTicker.Stop()

	// Channel to signal when processing is done
	done := make(chan struct{}, 1)
	defer func() { done <- struct{}{} }()

	// Start keep-alive goroutine
	go func() {
		for {
			select {
			case <-keepAliveTicker.C:
				// Update the UpdatedAt timestamp to keep the task alive
				if err := tr.redisStore.ExtendTaskLock(ctx, taskID, tr.instanceID, DefaultLockExpiration); err != nil {
					klog.Errorf("Failed to update task status for keep-alive: %v", err)
				} else {
					klog.V(4).Infof("Updated task %s status for keep-alive", taskID)
				}
			case <-done:
				return
			case <-ctx.Done():
				return
			}
		}
	}()

	// Move pending files to cancelled status with proper locking
	if err := tr.handleShardCancellation(ctx, taskID); err != nil {
		klog.Errorf("Failed to handle file cancellation for task %s: %v", taskID, err)
		return fmt.Errorf("failed to handle file cancellation: %v", err)
	}

	// Clean up files and Redis data
	if err := tr.cleanupTaskShardMeta(ctx, taskID); err != nil {
		klog.Errorf("Failed to clean up cancelling task files %s: %v", taskID, err)
		return fmt.Errorf("failed to clean up cancelling task files: %v", err)
	}

	// Move task from cancelling to cancelled set and update status atomically
	if err := tr.redisStore.MoveTaskAndUpdateStatusAtomically(ctx, taskID, StatusCancelling, StatusCancelled); err != nil {
		return fmt.Errorf("failed to move task to cancelled set: %v", err)
	}

	// // Remove task from cancelled set as final step
	// if err := tr.redisStore.RemoveFromSet(ctx, StatusCancelled, taskID); err != nil {
	// 	klog.Warningf("Failed to remove task from cancelled set: %v", err)
	// }

	klog.Infof("Finished cancelling task: %s", taskID)
	return nil
}

// Move/Wait pending shards to final status
func (tr *TaskReactor) handleShardCancellation(ctx context.Context, taskID string) error {
	// Wait for all shards to reach final state
	// Check if there are any in-progress shards and wait for them to complete

	// Get all pending shards for this task
	shardIDs, err := tr.redisStore.GetShardsInStatus(ctx, taskID, ShardStatusPending)
	if err != nil {
		return fmt.Errorf("failed to get pending shards: %v", err)
	}

	// Process all shards directly
	for _, shardID := range shardIDs {
		// No need to acuqire file lock
		// Move shard from pending to failed status to indicate it's been processed (cancelled)
		if err := tr.redisStore.MoveShardAtomically(ctx, taskID, shardID, ShardStatusPending, ShardStatusFailed); err != nil {
			klog.Errorf("Failed to move shard %s from pending to completed: %v", shardID, err)
			continue
		}
	}

	// Handle in-progress shards
	// Get all in-progress shards for this task
	inProgressShardIDs, err := tr.redisStore.GetShardsInStatus(ctx, taskID, ShardStatusInProgress)
	if err != nil {
		return fmt.Errorf("failed to get in-progress shards: %v", err)
	}

	// For in-progress shards, we need to handle them appropriately
	if len(inProgressShardIDs) > 0 {
		klog.Infof("Task %s has %d in-progress shards that need to complete before cancellation", taskID, len(inProgressShardIDs))
		time.Sleep(time.Second)
		return fmt.Errorf("task %s has %d in-progress shards that must complete before cancellation can proceed", taskID, len(inProgressShardIDs))
	}

	return nil
}

// cleanupShardOSSFiles cleans up OSS files for a task
func (tr *TaskReactor) cleanupShardOSSFiles(ctx context.Context, taskID string) error {
	// Use ListObjectsV2 to get all objects related to this task instead of getting shards from Redis sets
	inputPrefix := tr.getShardFileInputOSSPrefix(taskID)
	outputPrefix := tr.getShardFileOutputOSSPrefix(taskID)

	// List all objects with the task prefix
	inputObjects, err := tr.ossClient.ListObjectsWithPrefix(ctx, inputPrefix)
	if err != nil {
		klog.Errorf("Failed to list objects for task %s: %v", taskID, err)
		return fmt.Errorf("failed to list objects for task %s: %v", taskID, err)
	}
	outputObjects, err := tr.ossClient.ListObjectsWithPrefix(ctx, outputPrefix)
	if err != nil {
		klog.Errorf("Failed to list objects for task %s: %v", taskID, err)
		return fmt.Errorf("failed to list objects for task %s: %v", taskID, err)
	}
	objects := append(inputObjects, outputObjects...)

	// Delete all files at once
	if len(objects) > 0 {
		if err := tr.ossClient.DeleteFiles(ctx, objects); err != nil {
			klog.Errorf("Failed to delete files for task %s: %v", taskID, err)
			return fmt.Errorf("Failed to delete files for task %s: %v", taskID, err)
		}
	}

	return nil
}

// cleanupShardMeta remove shard set keys
func (tr *TaskReactor) cleanupShardMeta(ctx context.Context, taskID string) error {
	shardStatusList := []ShardStatus{ShardStatusPending, ShardStatusInProgress, ShardStatusCompleted, ShardStatusFailed}
	for _, status := range shardStatusList {
		err := tr.redisStore.RemoveShardInfo(ctx, taskID, status)
		if err != nil {
			klog.Errorf("Failed to clean shard meta: %v", err)
			return fmt.Errorf("Failed to clean shard meta: %v", err)
		}
	}
	return nil
}

func (tr *TaskReactor) cleanupTaskShardMeta(ctx context.Context, taskID string) error {
	if err := tr.cleanupShardOSSFiles(ctx, taskID); err != nil {
		return err
	}
	if err := tr.cleanupShardMeta(ctx, taskID); err != nil {
		return err
	}
	return nil
}

// handleValidatingFailover handles failover for validating tasks
// Returns the task ID if a lock was acquired for a stale task that should be processed
func (tr *TaskReactor) handleValidatingFailover(ctx context.Context) string {
	// Get all tasks in validating set
	taskIDs, err := tr.redisStore.GetTasksInStatus(ctx, StatusValidating)
	if err != nil {
		klog.Errorf("Failed to get validating tasks: %v", err)
		return ""
	}

	// Shuffle the task IDs to ensure better distribution across instances
	// This helps avoid multiple instances trying to recover the same tasks
	rand.Shuffle(len(taskIDs), func(i, j int) {
		taskIDs[i], taskIDs[j] = taskIDs[j], taskIDs[i]
	})

	for _, taskID := range taskIDs {
		// Get task update time
		task, err := tr.redisStore.GetTask(ctx, taskID)
		if err != nil {
			klog.Errorf("Failed to get task %s: %v", taskID, err)
			continue
		}

		// Check if task hasn't been updated for more than 5 minutes
		updatedAt := int64(0)
		if task.UpdatedAt != nil {
			updatedAt = *task.UpdatedAt
		}
		if time.Since(time.Unix(updatedAt, 0)) > DefaultLockExpiration {
			// Try to acquire lock
			acquired, err := tr.redisStore.AcquireTaskLock(ctx, taskID, tr.instanceID, DefaultLockExpiration)
			if err != nil || !acquired {
				continue
			}

			// Instead of cleaning up, we return the task ID to be processed
			// since we now have the lock for this stale validating task
			klog.Infof("Acquired lock for stale validating task: %s", taskID)
			return taskID
		}
	}

	return ""
}

// ValidateBatchFile validates that each line in the batch input file has the correct format
func (tr *TaskReactor) ValidateBatchFile(ctx context.Context, fileLines [][]byte) error {
	lineNumber := 0

	for _, line := range fileLines {
		line = bytes.TrimSpace(line)

		// Skip empty lines
		if len(line) == 0 {
			continue
		}

		// Check line count limit (max 50,000 lines)
		if lineNumber >= 50000 {
			return fmt.Errorf("file line count exceeds maximum allowed lines of 50000. Current line count: %d", lineNumber)
		}
		lineNumber++

		// Try to unmarshal the line as JSON
		var request SingleLineRequest
		if err := json.Unmarshal(line, &request); err != nil {
			return fmt.Errorf("invalid JSON format at line %d: %v", lineNumber-1, err)
		}

		// Validate required fields
		if request.CustomID == "" {
			return fmt.Errorf("missing custom_id at line %d", lineNumber-1)
		}

		if request.Method == "" {
			return fmt.Errorf("missing method at line %d", lineNumber-1)
		}

		if request.Method != "POST" {
			return fmt.Errorf("invalid method '%s' at line %d, expected 'POST'", request.Method, lineNumber-1)
		}

		if request.URL == "" {
			return fmt.Errorf("missing url at line %d", lineNumber-1)
		}

		// Validate URL - must be one of the supported endpoints
		supportedEndpoints := map[string]bool{
			"/v1/responses":        true,
			"/v1/chat/completions": true,
			"/v1/completions":      true,
		}
		if !supportedEndpoints[request.URL] {
			return fmt.Errorf("invalid url '%s' at line %d, must be one of '/v1/responses', '/v1/chat/completions', or '/v1/completions'", request.URL, lineNumber-1)
		}

		if request.Body == nil {
			return fmt.Errorf("missing body at line %d", lineNumber-1)
		}
	}

	return nil
}

func (tr *TaskReactor) getFileOSSPath(fileID string) string {
	return path.Join(tr.ossPrefix, fileID)
}

func (tr *TaskReactor) getShardFileInputOSSPrefix(taskID string) string {
	return path.Join(tr.ossPrefix, taskID, "input")
}

func (tr *TaskReactor) getShardFileOutputOSSPrefix(taskID string) string {
	return path.Join(tr.ossPrefix, taskID, "output")
}

func (tr *TaskReactor) getShardFileInputOSSPath(taskID, shardID string) string {
	return path.Join(tr.getShardFileInputOSSPrefix(taskID), shardID)
}

func (tr *TaskReactor) getShardFileOutputOSSPath(taskID, shardID string) string {
	return path.Join(tr.getShardFileOutputOSSPrefix(taskID), shardID)
}

func (tr *TaskReactor) getShardFileOutputErrorOSSPath(taskID, shardID string) string {
	return path.Join(tr.getShardFileOutputOSSPrefix(taskID), "err_"+shardID)
}
