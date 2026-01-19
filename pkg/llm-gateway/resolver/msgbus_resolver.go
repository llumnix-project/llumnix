package resolver

import (
	"context"
	"easgo/pkg/llm-gateway/types"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"k8s.io/klog/v2"
)

const (
	MsgBusURI       = "msgbus://127.0.0.1:9900"
	msgBusURL       = "http://127.0.0.1:9900/api/messages"
	msgSyncDuration = time.Second * 3
)

type LLMWorkerParser interface {
	ParseWorker(key, value string) (*types.LLMWorker, error)
	CheckWorkers(instance types.LLMWorkerSlice) (bool, error)
}

type msgBusParser struct {
	// - @Description: The information format of message bus:
	//
	// {
	// 	"__instance__": "ep-inference-5380-decode-ep32tp1dp32-91e7d-2",
	// 	"__timestamp__": "1751306504",
	// 	"__version__": "1",
	// 	"worker0_8": "decode,11.224.50.49,8000,9000",
	// 	"worker1_8": "decode,11.224.50.49,8001,9001",
	// 	"worker2_8": "decode,11.224.50.49,8002,9002",
	// 	"worker3_8": "decode,11.224.50.49,8003,9003",
	// 	"worker4_8": "decode,11.224.50.49,8004,9004",
	// 	"worker5_8": "decode,11.224.50.49,8005,9005",
	// 	"worker6_8": "decode,11.224.50.49,8006,9006",
	// 	"worker7_8": "decode,11.224.50.49,8007,9007"
	//   },
}

var (
	re = regexp.MustCompile(`^worker(\d+)_(\d+)$`)
)

// ParseWorker parses a key-value pair from message bus into an types.LLMWorker.
// The key should match pattern "worker\d+_\d+" where the first number is DpRank
// and the second number is DPSize. The value should be in format "role,ip,port,optionPort".
// Returns nil if the key doesn't match the expected pattern.
func (p *msgBusParser) ParseWorker(key, value string) (*types.LLMWorker, error) {
	matches := re.FindStringSubmatch(key)
	if len(matches) != 3 {
		return nil, nil
	}
	worker, err := p.valueParse(value)
	if err != nil {
		return nil, err
	}
	worker.DPRank, _ = strconv.Atoi(matches[1])
	worker.DPSize, _ = strconv.Atoi(matches[2])
	return worker, nil
}

// CheckWorkers validates that all workers in a slice have the same role.
// This is used to ensure consistency within a worker group.
// Returns true if all workers have the same role, false otherwise.
func (p *msgBusParser) CheckWorkers(workers types.LLMWorkerSlice) (bool, error) {
	if len(workers) <= 0 {
		return true, nil
	}
	role := workers[0].Role
	for _, worker := range workers {
		if worker.Role != role {
			return false, fmt.Errorf("LLM worker Role does not match: %s, %s", role, worker.Role)
		}
	}
	return true, nil
}

func (p *msgBusParser) valueParse(str string) (*types.LLMWorker, error) {
	fields := strings.Split(str, ",")
	if len(fields) < 3 {
		return nil, fmt.Errorf("Message Bus parse failed: got %v", str)
	}

	var worker types.LLMWorker
	worker.Role = types.InferRole(fields[0])
	worker.Endpoint.Host = fields[1]
	tmp, err := strconv.ParseInt(fields[2], 10, 32)
	if err != nil {
		return nil, fmt.Errorf("Message Bus parse failed: %v, str is %v", err, str)
	}
	worker.Endpoint.Port = int(tmp)

	if len(fields) > 3 {
		tmp, err = strconv.ParseInt(fields[3], 10, 32)
		if err != nil {
			return nil, fmt.Errorf("Message Bus parse failed: %v, str is %v", err, str)
		}
		worker.AuxPort = int(tmp)
	}
	return &worker, nil
}

type MsgKV map[string]string
type MsgKVs []MsgKV

type LLM []types.LLMWorker

// MessageReader defines an interface for reading messages from message bus
// This allows mocking in tests
type MessageReader interface {
	ReadMessages() (MsgKVs, error)
}

// HTTPMessageReader implements MessageReader using HTTP client
type HTTPMessageReader struct {
	client *http.Client
	url    string
}

// NewHTTPMessageReader creates a new HTTPMessageReader
func NewHTTPMessageReader(url string) *HTTPMessageReader {
	return &HTTPMessageReader{
		client: &http.Client{},
		url:    url,
	}
}

// ReadMessages implements MessageReader.ReadMessages
func (r *HTTPMessageReader) ReadMessages() (MsgKVs, error) {
	req, _ := http.NewRequest("GET", r.url, nil)

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("message-bus read failed: %v", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("message-bus read data failed: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("message-bus read failed, status code: %d", resp.StatusCode)
	}

	var kvs MsgKVs
	if err := json.Unmarshal(data, &kvs); err != nil {
		return nil, fmt.Errorf("message-bus data unmarshal failed")
	}
	return kvs, nil
}

type MsgBusResolverBackend struct {
	mu            sync.RWMutex
	workerSlices  map[string]types.LLMWorkerSlice
	workerParser  LLMWorkerParser
	messageReader MessageReader

	resolvers map[string][]*MsgBusResolver
}

var (
	msgBusResolverBackend *MsgBusResolverBackend
	msgBusOnce            sync.Once
)

// getOrCreateMsgBusResolverBackend creates or returns the singleton MsgBusResolverBackend
// This is the production version that uses HTTPMessageReader
func getOrCreateMsgBusResolverBackend() *MsgBusResolverBackend {
	msgBusOnce.Do(
		func() {
			parser := &msgBusParser{}
			msgBusResolverBackend = &MsgBusResolverBackend{
				workerParser:  parser,
				resolvers:     make(map[string][]*MsgBusResolver),
				messageReader: NewHTTPMessageReader(msgBusURL),
			}
			msgBusResolverBackend.SyncWorkerOnce()
			go msgBusResolverBackend.syncWorkersLoop()
		},
	)
	return msgBusResolverBackend
}

// NewTestMsgBusResolverBackend creates a MsgBusResolverBackend for testing with a custom MessageReader
// This should only be used in tests
func NewTestMsgBusResolverBackend(reader MessageReader) *MsgBusResolverBackend {
	parser := &msgBusParser{}
	return &MsgBusResolverBackend{
		workerParser:  parser,
		resolvers:     make(map[string][]*MsgBusResolver),
		messageReader: reader,
	}
}

// SyncWorkerOnce fetches the latest worker information from message bus and updates all resolvers.
// This is a single synchronization operation that can be called manually or periodically.
// Errors during fetching are logged but don't stop the update process for other resolvers.
func (r *MsgBusResolverBackend) SyncWorkerOnce() {
	newWorkerSlices, err := r.getLLMWorkers()
	if err != nil {
		return
	}

	r.updateResolver(newWorkerSlices)
}

// syncWorkersLoop periodically fetches worker information from message bus.
// It uses a ticker to run at regular intervals (msgSyncDuration).
// The function includes panic recovery to prevent the goroutine from crashing.
func (r *MsgBusResolverBackend) syncWorkersLoop() {
	defer func() {
		if err := recover(); err != nil {
			klog.Errorf("message bus backend resolver panic: %v\n%s", err, string(debug.Stack()))
		}
	}()

	// Create a ticker that fires every msgSyncDuration
	ticker := time.NewTicker(msgSyncDuration)
	defer ticker.Stop()

	// Run on every tick
	for {
		select {
		case <-ticker.C:
			r.SyncWorkerOnce()
		}
	}
}

// getLLMWorkers fetches and parses worker information from message bus.
// It returns a map from instance name to types.LLMWorkerSlice.
// The function handles parsing errors gracefully by logging and continuing.
func (r *MsgBusResolverBackend) getLLMWorkers() (map[string]types.LLMWorkerSlice, error) {
	kvs, err := r.messageReader.ReadMessages()
	if err != nil {
		klog.Errorf("get llm workers failed: %v", err)
		return nil, err
	}

	workers := make(map[string]types.LLMWorkerSlice)
	for _, kv := range kvs {
		podName := kv["__instance__"]
		instanceName := getInstanceName(podName)
		if _, ok := workers[instanceName]; !ok {
			workers[instanceName] = make(types.LLMWorkerSlice, 0)
		}
		for k, v := range kv {
			worker, err := r.workerParser.ParseWorker(k, v)
			if err != nil {
				klog.Errorf("parse worker failed: %v", err)
				continue
			}
			if worker == nil {
				continue
			}
			workers[instanceName] = append(workers[instanceName], *worker)
		}
	}

	// check the workerSlice worker consistency and fill common fields
	for name, workerSlice := range workers {
		if len(workerSlice) == 0 {
			continue
		}
		if ok, err := r.workerParser.CheckWorkers(workerSlice); !ok {
			klog.Errorf("worker %s is not consistent: %v", name, err)
		}
	}

	return workers, nil
}

// readMessages is kept for backward compatibility, but now delegates to messageReader
func (r *MsgBusResolverBackend) readMessages() (MsgKVs, error) {
	return r.messageReader.ReadMessages()
}

// trim the last index if the pod is fleet pod, only take the master node
func getInstanceName(podName string) string {
	strs := strings.Split(podName, "-")
	// if last index is number, it's a fleet pod, return the prefix
	if len(strs) > 0 && strs[len(strs)-1] != "" {
		if _, err := strconv.Atoi(strs[len(strs)-1]); err == nil {
			return strings.Join(strs[:len(strs)-1], "-")
		}
	}
	return podName
}

func (r *MsgBusResolverBackend) registerResolver(inferRole string, resolver *MsgBusResolver) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.resolvers[inferRole] = append(r.resolvers[inferRole], resolver)
}

func (r *MsgBusResolverBackend) getResolvers() map[string][]*MsgBusResolver {
	r.mu.Lock()
	defer r.mu.Unlock()

	resolvers := make(map[string][]*MsgBusResolver, len(r.resolvers))
	for k, v := range r.resolvers {
		resolvers[k] = v
	}
	return resolvers
}

// updateResolver updates all registered resolvers with new worker slices.
// It filters workers by infer role and notifies each resolver of the relevant workers.
// This function is thread-safe and should be called with the backend lock held.
func (r *MsgBusResolverBackend) updateResolver(newWorkerSlices map[string]types.LLMWorkerSlice) error {
	allResolvers := r.getResolvers()
	for key, resolvers := range allResolvers {
		var filteredWorkers types.LLMWorkerSlice
		// Filter workers by infer role
		for _, workers := range newWorkerSlices {
			if len(workers) == 0 {
				continue
			}
			role := workers[0].Role
			resolverRole := types.InferRole(key)
			if role == resolverRole || resolverRole == types.InferRoleAll {
				filteredWorkers = append(filteredWorkers, workers...)
			}
		}
		// Update each resolver with filtered workers
		for _, resolver := range resolvers {
			resolver.updateInstances(filteredWorkers)
		}
	}
	r.workerSlices = newWorkerSlices
	return nil
}

type MsgBusResolver struct {
	mu sync.RWMutex

	// Only focus on workers corresponding to the role
	inferRole string

	workerSlice  types.LLMWorkerSlice
	workerParser LLMWorkerParser

	// watcher provides common observer management functionality
	watcher *Watcher
}

func NewMsgBusResolver(inferRole string) LLMResolver {
	b := getOrCreateMsgBusResolverBackend()
	r := &MsgBusResolver{
		inferRole:    inferRole,
		workerParser: b.workerParser,
		watcher:      NewWatcher(),
		workerSlice:  make(types.LLMWorkerSlice, 0), // Initialize empty previous slice
	}
	b.registerResolver(inferRole, r)
	// ensure the scheduler fetches the latest worker list after restart.
	b.SyncWorkerOnce()
	return r
}

// updateInstances updates the resolver's worker state and notifies observers of changes.
// It calculates the difference between old and new worker slices and sends
// added/removed workers to all registered observers.
// This method is thread-safe and handles concurrent access.
func (r *MsgBusResolver) updateInstances(workerSlice types.LLMWorkerSlice) {
	r.mu.Lock()
	oldSlice := r.workerSlice
	r.workerSlice = workerSlice // Update previous state
	r.mu.Unlock()

	// Calculate differences
	added, removed := DiffSets(oldSlice, workerSlice, func(w types.LLMWorker) string {
		return w.Id()
	})
	// Notify all observers if there are changes
	if len(added) > 0 || len(removed) > 0 {
		r.notifyObservers(added, removed)
	}
}

// notifyObservers sends added and removed workers to all registered observers
func (r *MsgBusResolver) notifyObservers(added, removed types.LLMWorkerSlice) {
	r.watcher.notifyObservers(added, removed)
}

// GetLLMWorkers returns the current list of LLM workers for this resolver.
// This method implements the LLMResolver interface and provides a snapshot
// of the current worker state. The returned slice should not be modified.
func (r *MsgBusResolver) GetLLMWorkers() (types.LLMWorkerSlice, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.workerSlice, nil
}

// Watch implements LLMResolver.Watch.
// It returns two channels: one for added workers and one for removed workers.
// The first value sent on the added channel is the current state (all workers considered as "added").
// Both channels will be closed when the context is cancelled or the resolver stops.
// This method is thread-safe and supports multiple concurrent observers.
func (r *MsgBusResolver) Watch(ctx context.Context) (<-chan types.LLMWorkerSlice, <-chan types.LLMWorkerSlice, error) {
	return r.watcher.Watch(ctx, r.GetLLMWorkers)
}

// MsgBusResolverBuilder implements ResolverBuilder for creating MsgBusResolver instances.
type MsgBusResolverBuilder struct{}

// Schema returns the schema identifier for this builder.
func (b *MsgBusResolverBuilder) Schema() string {
	return "msgbus"
}

// parseMsgBusURI parses a message bus URI in format "msgbus://host:port"
// Returns the message bus URL (http://host:port/api/messages)
func checkParseMsgBusURI(uri string) (string, error) {
	// Remove the msgbus:// prefix
	if !strings.HasPrefix(uri, "msgbus://") {
		return "", fmt.Errorf("invalid msgbus URI format: must start with 'msgbus://'")
	}

	hostPort := strings.TrimPrefix(uri, "msgbus://")

	// Build the message bus URL
	msgBusURL := fmt.Sprintf("http://%s/api/messages", hostPort)

	return msgBusURL, nil
}

// Build creates a new MsgBusResolver instance using the provided arguments.
// Expected arguments:
//   - "uri" (string, required): The message bus URI in format "msgbus://host:port"
//
// Returns an error if required arguments are missing or invalid.
func (b *MsgBusResolverBuilder) Build(uri string, args BuildArgs) (LLMResolver, error) {
	if uri == "" {
		return nil, fmt.Errorf("missing or invalid 'uri' argument")
	}

	// Parse the URI to get message bus URL
	url, err := checkParseMsgBusURI(uri)
	if err != nil {
		return nil, fmt.Errorf("failed to parse msgbus URI: %v", err)
	}
	if url != msgBusURL {
		return nil, fmt.Errorf("invalid msgbus URL: %s", url)
	}

	// Read role build args
	role, ok := args["role"].(string)
	if !ok || role == "" {
		return nil, fmt.Errorf("missing role or invalid role build args: %v", role)
	}

	// Create and return the resolver
	return NewMsgBusResolver(role), nil
}

func init() {
	RegisterLLM(&MsgBusResolverBuilder{})
}
