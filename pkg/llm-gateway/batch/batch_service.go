package batch

import (
	"context"
	"llumnix/cmd/gateway/app/options"
	"os"
	"strconv"
	"strings"

	"llumnix/pkg/llm-gateway/redis"

	"github.com/gorilla/mux"
	"k8s.io/klog/v2"
)

// BatchService handles batch processing functionality
type BatchService struct {
	config       *options.GatewayConfig
	batchHandler *BatchHandler
	redisStore   *RedisStore
	ossClient    *OSSClient
	reactor      *TaskReactor
	hostname     string
}

// NewBatchService creates a new batch service
func NewBatchService(config *options.GatewayConfig) (*BatchService, error) {
	// Parse Redis cluster hosts from string to slice
	hosts := strings.Split(config.BatchServiceRedisAddrs, ",")
	// Remove empty hosts
	nonEmptyHosts := []string{}
	for _, host := range hosts {
		host = strings.TrimSpace(host)
		if host != "" {
			nonEmptyHosts = append(nonEmptyHosts, host)
		}
	}

	// Initialize Redis client
	redisClient := redis.NewRedisStandaloneClient(
		nonEmptyHosts[0],
		config.BatchServiceRedisUsername,
		config.BatchServiceRedisPassword,
		config.BatchServiceRedisRetryTimes,
	)

	redisStore := NewRedisStore(redisClient)

	var ossBucket, ossPrefix string
	parts := strings.SplitN(strings.TrimPrefix(config.BatchOSSPath, "oss://"), "/", 2)
	if len(parts) == 0 {
		klog.Fatalf("Invalid batch oss path: %s", config.BatchOSSPath)
	}
	ossBucket = parts[0]
	if len(parts) > 1 {
		ossPrefix = parts[1]
	}

	ossClient, err := NewOSSClient(config.BatchOSSEndpoint, ossBucket)
	if err != nil {
		klog.Fatalf("Failed to create OSS client: %v", err)
	}

	// Get hostname from OS
	hostname, err := os.Hostname()
	if err != nil {
		klog.Warningf("Failed to get hostname, using default: %v", err)
		hostname = "llmgw-unknown"
	}

	// Create reactor
	reactor := NewTaskReactor(redisStore, ossClient, hostname, ossPrefix, config)
	// Create handler
	batchHandler := NewBatchHandler(redisStore, ossClient, reactor, hostname)

	return &BatchService{
		config:       config,
		batchHandler: batchHandler,
		redisStore:   redisStore,
		ossClient:    ossClient,
		reactor:      reactor,
		hostname:     hostname,
	}, nil
}

// Start starts the batch service
func (bs *BatchService) Start(ctx context.Context) {
	// Start reactors in separate goroutines
	go bs.startValidatingReactor(ctx)
	go bs.startInProcessReactor(ctx)
	go bs.startFinalizeReactor(ctx)
	go bs.startCancellingReactor(ctx)
	go bs.startExpireReactor(ctx)

	klog.Infof("Batch service started with hostname: %s", bs.hostname)
}

// startValidatingReactor starts the validating task reactor
func (bs *BatchService) startValidatingReactor(ctx context.Context) {
	klog.Info("Starting validating task reactor")
	bs.reactor.ValidatingTaskReactor(ctx)
}

// startInProcessReactor starts the in-process task reactor
func (bs *BatchService) startInProcessReactor(ctx context.Context) {
	klog.Info("Starting in-process task reactor")
	for i := range bs.config.BatchParallel {
		go bs.reactor.InProcessTaskReactor(ctx, strconv.Itoa(i))
	}
}

// startFinalizeReactor starts the finalize task reactor
func (bs *BatchService) startFinalizeReactor(ctx context.Context) {
	klog.Info("Starting finalize task reactor")
	bs.reactor.FinalizeTaskReactor(ctx)
}

// startCancellingReactor starts the cancelling task reactor
func (bs *BatchService) startCancellingReactor(ctx context.Context) {
	klog.Info("Starting cancelling task reactor")
	bs.reactor.CancelTaskReactor(ctx)
}

// startExpireReactor starts the expire task reactor
func (bs *BatchService) startExpireReactor(ctx context.Context) {
	klog.Info("Starting expire task reactor")
	bs.reactor.ExpireTaskReactor(ctx)
}

// RegisterRoutes registers batch API routes with the HTTP router
func (bs *BatchService) RegisterRoutes(router *mux.Router) {
	// File operations endpoints
	router.HandleFunc("/v1/files", bs.batchHandler.FilesHandler).Methods("POST", "GET")
	router.HandleFunc("/v1/files/{file_id}", bs.batchHandler.FileHandler).Methods("GET", "DELETE")
	router.HandleFunc("/v1/files/{file_id}/content", bs.batchHandler.GetFileContentHandler).Methods("GET")

	// Batch task endpoints
	router.HandleFunc("/v1/batches", bs.batchHandler.BatchesHandler).Methods("POST", "GET")
	router.HandleFunc("/v1/batches/{task_id}", bs.batchHandler.GetBatchTaskHandler).Methods("GET")
	router.HandleFunc("/v1/batches/{task_id}/cancel", bs.batchHandler.CancelBatchTaskHandler).Methods("POST")

	klog.Info("Batch API routes registered")
}
