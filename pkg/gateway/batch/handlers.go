package batch

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"k8s.io/klog/v2"
)

const (
	// MaxFileSize is the maximum allowed file size (200MB)
	MaxFileSize = int64(200 << 20) // 200MB in bytes
)

// BatchHandler handles batch processing API requests
type BatchHandler struct {
	redisStore *RedisStore
	ossClient  *OSSClient
	reactor    *TaskReactor
	instanceID string
}

// NewBatchHandler creates a new batch handler
func NewBatchHandler(redisStore *RedisStore, ossClient *OSSClient, reactor *TaskReactor, instanceID string) *BatchHandler {
	return &BatchHandler{
		redisStore: redisStore,
		ossClient:  ossClient,
		reactor:    reactor,
		instanceID: instanceID,
	}
}

// handleFileUpload handles the file upload logic
func (h *BatchHandler) handleFileUpload(w http.ResponseWriter, r *http.Request) {
	// Parse multipart form
	if err := r.ParseMultipartForm(32 << 20); err != nil { // 32MB max memory
		http.Error(w, fmt.Sprintf("Failed to parse form: %v", err), http.StatusBadRequest)
		return
	}

	// Get file from form
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get file: %v", err), http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Validate file size (max 200MB)
	if header.Size > MaxFileSize {
		http.Error(w, fmt.Sprintf("File size exceeds maximum allowed size of %d bytes. File size: %d bytes", MaxFileSize, header.Size), http.StatusBadRequest)
		return
	}

	// Get purpose from form
	purpose := r.FormValue("purpose")
	if purpose == "" {
		http.Error(w, "Purpose is required", http.StatusBadRequest)
		return
	}

	// Get expires_after parameters
	expiresAnchor := r.FormValue("expires_after[anchor]")
	expiresSeconds := r.FormValue("expires_after[seconds]")

	// Validate expires_after parameters - if one is provided, both are required
	if (expiresAnchor != "" && expiresSeconds == "") || (expiresAnchor == "" && expiresSeconds != "") {
		http.Error(w, "Both expires_after[anchor] and expires_after[seconds] are required when expires_after is specified", http.StatusBadRequest)
		return
	}

	// Generate file ID
	fileID := fmt.Sprintf("batch_input_%s", uuid.New().String())

	ossPath := h.reactor.getFileOSSPath(fileID)

	// Upload file to OSS
	if err := h.ossClient.UploadFile(r.Context(), ossPath, file); err != nil {
		http.Error(w, fmt.Sprintf("Failed to upload file: %v", err), http.StatusInternalServerError)
		return
	}

	// Calculate expiration time if expires_after is provided
	var expiresAt int64
	createdAt := time.Now().Unix()

	if expiresAnchor != "" && expiresSeconds != "" {
		if expiresAnchor != "created_at" {
			http.Error(w, "Only 'created_at' is supported for expires_after[anchor]", http.StatusBadRequest)
			return
		}

		if seconds, err := strconv.Atoi(expiresSeconds); err != nil {
			http.Error(w, "expires_after[seconds] must be a valid integer", http.StatusBadRequest)
			return
		} else {
			expiresAt = createdAt + int64(seconds)
		}
	}

	// Create file record
	batchFile := &File{
		ID:        fileID,
		Object:    "file",
		Bytes:     header.Size,
		CreatedAt: createdAt,
		ExpiresAt: expiresAt,
		Filename:  header.Filename,
		Purpose:   purpose,
	}

	// Save file record to Redis
	if err := h.redisStore.CreateFile(r.Context(), batchFile); err != nil {
		http.Error(w, fmt.Sprintf("Failed to save file record: %v", err), http.StatusInternalServerError)
		return
	}

	// Return file information
	response := map[string]any{
		"id":         fileID,
		"object":     "file",
		"bytes":      header.Size,
		"created_at": createdAt,
		"filename":   header.Filename,
		"purpose":    purpose,
	}

	// Add expires_at to response if it was calculated
	if expiresAt > 0 {
		response["expires_at"] = expiresAt
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// handleListFiles handles the file listing logic
func (h *BatchHandler) handleListFiles(w http.ResponseWriter, r *http.Request) {
	// Get all file IDs
	fileIDs, err := h.redisStore.GetAllFiles(r.Context())
	if err != nil {
		klog.Errorf("Failed to get file list: %v", err)
		http.Error(w, "Failed to get file list", http.StatusInternalServerError)
		return
	}

	// Get file details for each file ID
	var files []*File
	for _, fileID := range fileIDs {
		file, err := h.redisStore.GetFile(r.Context(), fileID)
		if err != nil {
			klog.Errorf("Failed to get file %s: %v", fileID, err)
			continue
		}
		files = append(files, file)
	}

	// Sort files by creation time (newest first)
	sort.Slice(files, func(i, j int) bool {
		return files[i].CreatedAt > files[j].CreatedAt
	})

	// Prepare response
	response := map[string]any{
		"object": "list",
		"data":   files,
	}

	// Set first_id and last_id
	if len(files) > 0 {
		response["first_id"] = files[0].ID
		response["last_id"] = files[len(files)-1].ID
	} else {
		response["first_id"] = nil
		response["last_id"] = nil
	}

	// For simplicity, we'll set has_more to false
	response["has_more"] = false

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// FilesHandler handles file operations (upload, list)
func (h *BatchHandler) FilesHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.handleFileUpload(w, r)
	case http.MethodGet:
		h.handleListFiles(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// FileHandler handles file retrieval and deletion requests
func (h *BatchHandler) FileHandler(w http.ResponseWriter, r *http.Request) {
	// Extract file ID from path variables
	vars := mux.Vars(r)
	fileID := vars["file_id"]

	switch r.Method {
	case http.MethodGet:
		// Get file from Redis
		file, err := h.redisStore.GetFile(r.Context(), fileID)
		if err != nil {
			http.Error(w, fmt.Sprintf("File not found: %v", err), http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(file)

	case http.MethodDelete:
		// Get file info before deletion to return in response
		file, err := h.redisStore.GetFile(r.Context(), fileID)
		if err != nil {
			http.Error(w, fmt.Sprintf("File not found: %v", err), http.StatusNotFound)
			return
		}

		// Delete file from Redis
		if err := h.redisStore.DeleteFile(r.Context(), fileID); err != nil {
			klog.Errorf("Failed to delete file %s: %v", fileID, err)
			http.Error(w, "Failed to delete file", http.StatusInternalServerError)
			return
		}

		// Prepare response
		response := map[string]any{
			"id":      file.ID,
			"object":  file.Object,
			"deleted": true,
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
}

// GetFileContentHandler handles file content retrieval requests
func (h *BatchHandler) GetFileContentHandler(w http.ResponseWriter, r *http.Request) {
	// Extract file ID from path variables
	vars := mux.Vars(r)
	fileID := vars["file_id"]

	// Get file info from Redis to verify it exists
	file, err := h.redisStore.GetFile(r.Context(), fileID)
	if err != nil {
		http.Error(w, fmt.Sprintf("File not found: %v", err), http.StatusNotFound)
		return
	}

	ossPath := h.reactor.getFileOSSPath(fileID)

	// Get file content from OSS
	content, err := h.ossClient.GetObjectContent(r.Context(), ossPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to retrieve file content: %v", err), http.StatusInternalServerError)
		return
	}

	// Set appropriate content type for file download
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", file.Filename))

	// Write file content to response
	w.Write(content)
}

// handleCreateBatchTask handles the batch task creation logic
func (h *BatchHandler) handleCreateBatchTask(w http.ResponseWriter, r *http.Request) {
	// Extract bearer token from Authorization header
	var bearerToken *string
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" {
		if after, ok := strings.CutPrefix(authHeader, "Bearer "); ok {
			token := after
			bearerToken = &token
		}
	}

	// Parse request body
	var request struct {
		InputFileID        string              `json:"input_file_id"`
		Endpoint           string              `json:"endpoint"`
		Model              string              `json:"model"`
		CompletionWindow   string              `json:"completion_window"`
		Metadata           map[string]string   `json:"metadata,omitempty"`
		OutputExpiresAfter *OutputExpiresAfter `json:"output_expires_after,omitempty"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, fmt.Sprintf("Failed to decode request: %v", err), http.StatusBadRequest)
		return
	}

	// Validate metadata if provided
	if len(request.Metadata) > 0 {
		if err := ValidateMetadata(request.Metadata); err != nil {
			http.Error(w, fmt.Sprintf("Invalid metadata: %v", err), http.StatusBadRequest)
			return
		}
	}

	// Validate output_expires_after if provided
	if request.OutputExpiresAfter != nil {
		if request.OutputExpiresAfter.Anchor != "created_at" {
			http.Error(w, "output_expires_after.anchor must be 'created_at'", http.StatusBadRequest)
			return
		}
		if request.OutputExpiresAfter.Seconds < 3600 || request.OutputExpiresAfter.Seconds > 2592000 {
			http.Error(w, "output_expires_after.seconds must be between 3600 (1 hour) and 2592000 (30 days)", http.StatusBadRequest)
			return
		}
	}

	// Validate required fields
	if request.InputFileID == "" {
		http.Error(w, "input_file_id is required", http.StatusBadRequest)
		return
	}

	if request.Endpoint == "" {
		http.Error(w, "endpoint is required", http.StatusBadRequest)
		return
	}

	// Validate endpoint is one of the supported endpoints
	supportedEndpoints := map[string]bool{
		"/v1/responses":        true,
		"/v1/chat/completions": true,
		"/v1/embeddings":       true,
		"/v1/completions":      true,
	}
	if !supportedEndpoints[request.Endpoint] {
		http.Error(w, "endpoint must be one of '/v1/responses', '/v1/chat/completions', '/v1/embeddings', or '/v1/completions'", http.StatusBadRequest)
		return
	}

	// Check if file exists
	_, err := h.redisStore.GetFile(r.Context(), request.InputFileID)
	if err != nil {
		http.Error(w, fmt.Sprintf("File not found: %v", err), http.StatusBadRequest)
		return
	}

	// Generate task ID
	taskID := fmt.Sprintf("batch_%s", uuid.New().String())

	// Validate and set completion window
	completionWindow := request.CompletionWindow
	if completionWindow == "" {
		completionWindow = "24h"
	}

	// Validate that completion_window is only "24h"
	if completionWindow != "24h" {
		http.Error(w, "completion_window must be '24h'", http.StatusBadRequest)
		return
	}

	// Parse duration from completionWindow
	duration, err := time.ParseDuration(completionWindow)
	if err != nil {
		http.Error(w, fmt.Sprintf("Invalid completion_window format: %v", err), http.StatusBadRequest)
		return
	}

	now := time.Now().Unix()
	expiresAt := now + int64(duration.Seconds())

	// Create batch task
	task := &BatchTask{
		ID:                 taskID,
		Object:             "batch",
		Endpoint:           request.Endpoint,
		Model:              request.Model,
		Errors:             nil,
		InputFileID:        request.InputFileID,
		CompletionWindow:   completionWindow,
		Status:             StatusPending,
		CreatedAt:          &now,
		UpdatedAt:          &now,
		ExpiresAt:          expiresAt,
		RequestCounts:      &RequestCounts{Total: 0, Completed: 0, Failed: 0},
		Metadata:           request.Metadata,
		OutputExpiresAfter: request.OutputExpiresAfter,
		Token:              bearerToken,
	}

	// Save task to Redis
	if err := h.redisStore.CreateTask(r.Context(), task); err != nil {
		http.Error(w, fmt.Sprintf("Failed to create batch task: %v", err), http.StatusInternalServerError)
		return
	}

	stripInternalFields(task)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(task)
}

// handleListBatchTasks handles the batch task listing logic
func (h *BatchHandler) handleListBatchTasks(w http.ResponseWriter, r *http.Request) {
	// Parse query parameters
	limit := 20 // default limit
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if parsedLimit, err := strconv.Atoi(limitStr); err == nil && parsedLimit > 0 {
			limit = parsedLimit
		}
	}

	// Parse "after" parameter for pagination
	after := r.URL.Query().Get("after")

	// Get all task IDs from the global task set
	taskIDs, err := h.redisStore.GetAllTasks(r.Context())
	if err != nil {
		klog.Errorf("Failed to get task list: %v", err)
		http.Error(w, "Failed to get task list", http.StatusInternalServerError)
		return
	}

	// Get task details for each task ID
	var allTasks []*BatchTask
	for _, taskID := range taskIDs {
		task, err := h.redisStore.GetTask(r.Context(), taskID)
		if err != nil {
			klog.Errorf("Failed to get task %s: %v", taskID, err)
			continue
		}

		// Remove token for security
		task.Token = nil

		allTasks = append(allTasks, task)
	}

	// Sort tasks by creation time (newest first)
	sort.Slice(allTasks, func(i, j int) bool {
		return *allTasks[i].CreatedAt > *allTasks[j].CreatedAt
	})

	// Apply "after" parameter for pagination
	if after != "" {
		// Find the index of the task with the "after" ID
		afterIndex := -1
		for i, task := range allTasks {
			if task.ID == after {
				afterIndex = i
				break
			}
		}

		// If we found the "after" task, slice the array to start after it
		if afterIndex != -1 && afterIndex < len(allTasks)-1 {
			allTasks = allTasks[afterIndex+1:]
		} else {
			// If we didn't find the "after" task or it's the last task, return empty list
			allTasks = []*BatchTask{}
		}
	}

	// Check if there are more tasks available for pagination
	hasMore := false
	if len(allTasks) > limit {
		hasMore = true
		allTasks = allTasks[:limit]
	}

	// Prepare response
	response := map[string]any{
		"object": "list",
		"data":   allTasks,
	}

	// Set first_id and last_id
	if len(allTasks) > 0 {
		response["first_id"] = allTasks[0].ID
		response["last_id"] = allTasks[len(allTasks)-1].ID
	} else {
		response["first_id"] = nil
		response["last_id"] = nil
	}

	// Set has_more flag
	response["has_more"] = hasMore

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// BatchesHandler handles batch task operations (create, list)
func (h *BatchHandler) BatchesHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.handleCreateBatchTask(w, r)
	case http.MethodGet:
		h.handleListBatchTasks(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// GetBatchTaskHandler handles batch task retrieval requests
func (h *BatchHandler) GetBatchTaskHandler(w http.ResponseWriter, r *http.Request) {
	// Extract task ID from path variables
	vars := mux.Vars(r)
	taskID := vars["task_id"]

	// Get task from Redis
	task, err := h.redisStore.GetTask(r.Context(), taskID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Task not found: %v", err), http.StatusNotFound)
		return
	}

	stripInternalFields(task)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(task)
}

// CancelBatchTaskHandler handles batch task cancellation requests
func (h *BatchHandler) CancelBatchTaskHandler(w http.ResponseWriter, r *http.Request) {
	// Extract task ID from path variables
	vars := mux.Vars(r)
	taskID := vars["task_id"]

	// Get current task status
	taskStatus, err := h.redisStore.GetTaskStatus(r.Context(), taskID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Task not found: %v", err), http.StatusNotFound)
		return
	}

	// Only tasks in validating or in_progress status can be cancelled
	if taskStatus != StatusValidating && taskStatus != StatusInProgress {
		http.Error(w, "Task cannot be cancelled in current status", http.StatusBadRequest)
		return
	}

	// Try to acquire lock for the task to ensure consistency
	acquired, err := h.redisStore.AcquireTaskLock(r.Context(), taskID, h.instanceID, DefaultLockExpiration)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to acquire task lock: %v", err), http.StatusInternalServerError)
		return
	}
	if !acquired {
		http.Error(w, "Task is currently being processed by another instance", http.StatusConflict)
		return
	}
	// Release lock when done
	defer func() {
		if err := h.redisStore.ReleaseTaskLock(r.Context(), taskID, h.instanceID); err != nil {
			klog.Warningf("Failed to release task lock for %s: %v", taskID, err)
		}
	}()

	// Get task again after acquiring lock to ensure we have the most up-to-date status
	taskStatus, err = h.redisStore.GetTaskStatus(r.Context(), taskID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Task not found: %v", err), http.StatusNotFound)
		return
	}

	// Check if task is still in a cancellable status
	if taskStatus != StatusValidating && taskStatus != StatusInProgress {
		http.Error(w, "Task cannot be cancelled in current status", http.StatusBadRequest)
		return
	}

	// Atomically move task to cancelling set and update status
	if err := h.redisStore.MoveTaskAndUpdateStatusAtomically(r.Context(), taskID, taskStatus, StatusCancelling); err != nil {
		http.Error(w, fmt.Sprintf("Failed to move task to cancelling set: %v", err), http.StatusInternalServerError)
		return
	}

	task, err := h.redisStore.GetTask(r.Context(), taskID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Task details not found: %v", err), http.StatusNotFound)
		return
	}

	stripInternalFields(task)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(task)
}

func stripInternalFields(task *BatchTask) {
	// These fields are hidden from user
	task.OutputExpiresAfter = nil
	task.UpdatedAt = nil
	task.Token = nil
}
