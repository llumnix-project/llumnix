package res

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	// DefaultTimeout is the default timeout for HTTP requests.
	DefaultTimeout = 5 * time.Second
)

// Collector collects metrics from remote LLM engine instances.
type Collector struct {
	client *http.Client

	// serviceToken is used to authenticate HTTP requests via Authorization header
	serviceToken string

	// previousMetrics stores the last collected metrics for delta calculation
	previousMetrics map[string]*EngineState
}

// NewCollector creates a new metric collector with default settings.
func NewCollector() *Collector {
	return NewCollectorWithTimeout(DefaultTimeout)
}

// NewCollectorWithTimeout creates a new metric collector with custom timeout.
func NewCollectorWithTimeout(timeout time.Duration) *Collector {
	return &Collector{
		client: &http.Client{
			Timeout: timeout,
		},
		previousMetrics: make(map[string]*EngineState),
	}
}

// NewCollectorWithConfig creates a new metric collector with custom timeout and service token.
func NewCollectorWithConfig(timeout time.Duration, serviceToken string) *Collector {
	return &Collector{
		client: &http.Client{
			Timeout: timeout,
		},
		serviceToken:    serviceToken,
		previousMetrics: make(map[string]*EngineState),
	}
}

// FetchMetrics fetches metrics from the given URL via HTTP GET request.
func (c *Collector) FetchMetrics(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	if c.serviceToken != "" {
		req.Header.Set("Authorization", c.serviceToken)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch metrics: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	return string(data), nil
}

// fetchAndParse fetches metrics from URL and parses them into a map.
func (c *Collector) fetchAndParse(ctx context.Context, url string) (map[string]float64, error) {
	metricText, err := c.FetchMetrics(ctx, url)
	if err != nil {
		return nil, err
	}

	metrics := ParseAllMetrics(metricText)
	if len(metrics) == 0 {
		return nil, fmt.Errorf("no metrics parsed from %s", url)
	}
	return metrics, nil
}

// tryParser attempts to fetch and validate metrics using a specific parser.
// Returns the parsed metrics if the parser can handle them, nil otherwise.
func (c *Collector) tryParser(ctx context.Context, endpoint string, parser BackendParser) (map[string]float64, string, error) {
	metricURL := parser.MetricURL(endpoint)
	metrics, err := c.fetchAndParse(ctx, metricURL)
	if err != nil {
		return nil, metricURL, err
	}

	if !parser.CanHandle(metrics) {
		return nil, metricURL, fmt.Errorf("parser cannot handle metrics")
	}
	return metrics, metricURL, nil
}

// buildEngineState constructs EngineState from parsed metrics with delta calculation.
func (c *Collector) buildEngineState(
	instanceID, endpoint string,
	metrics map[string]float64,
	parser BackendParser,
	duration int64,
) *EngineState {
	state := parser.ExtractMetrics(metrics)
	state.InstanceID = instanceID
	state.Endpoint = endpoint
	state.Timestamp = time.Now()

	// Calculate derived metrics if we have previous data
	if duration > 0 {
		if prev, exists := c.previousMetrics[instanceID]; exists && prev != nil {
			calculateDerivedMetrics(&state, prev, duration)
		}
	}

	// Store for next delta calculation
	c.previousMetrics[instanceID] = &state
	return &state
}

// DetectBackendType detects the backend type by trying different metric endpoints.
// It returns the detected backend type and the metric URL if successful.
// This method should be called once when a new instance is added.
func (c *Collector) DetectBackendType(ctx context.Context, endpoint string) (BackendType, string, error) {
	for _, parser := range backendRegistry {
		_, metricURL, err := c.tryParser(ctx, endpoint, parser)
		if err != nil {
			continue
		}
		return parser.BackendType(), metricURL, nil
	}
	return "", "", fmt.Errorf("unable to detect backend type for endpoint: %s", endpoint)
}

// CollectEngineState collects and parses metrics from a remote LLM engine instance.
// If backend type is provided (non-empty), it uses the corresponding parser directly.
// If backend type is empty, it automatically detects the backend type.
// It also calculates derived metrics (like throughput) if duration > 0.
//
// Parameters:
// - instanceID: unique identifier for the instance
// - endpoint: the HTTP endpoint of the instance (e.g., "192.168.1.1:8000")
// - backend: the backend type (empty string to auto-detect)
// - duration: time duration in milliseconds since last collection (0 for first collection)
//
// Returns the parsed EngineState, detected backend type, and error.
func (c *Collector) CollectEngineState(
	ctx context.Context,
	instanceID string,
	endpoint string,
	backend BackendType,
	duration int64,
) (*EngineState, BackendType, error) {
	// Case 1: Backend type is known - use directly
	if backend != "" {
		parser := GetParserByType(backend)
		if parser == nil {
			return nil, backend, fmt.Errorf("unsupported backend type: %s", backend)
		}

		metricURL := parser.MetricURL(endpoint)
		metrics, err := c.fetchAndParse(ctx, metricURL)
		if err != nil {
			return nil, backend, fmt.Errorf("failed to fetch metrics from %s: %w", metricURL, err)
		}

		state := c.buildEngineState(instanceID, endpoint, metrics, parser, duration)
		return state, backend, nil
	}

	// Case 2: Backend type unknown - auto-detect
	for _, parser := range backendRegistry {
		metrics, _, err := c.tryParser(ctx, endpoint, parser)
		if err != nil {
			continue
		}

		state := c.buildEngineState(instanceID, endpoint, metrics, parser, duration)
		return state, parser.BackendType(), nil
	}

	return nil, "", fmt.Errorf("unable to detect backend type or fetch metrics from endpoint: %s", endpoint)
}

// calculateDerivedMetrics calculates throughput and cache usage metrics.
// It computes token throughput rates based on token deltas and duration.
func calculateDerivedMetrics(current, prev *EngineState, durationMs int64) {
	// Calculate input token throughput (tps_in)
	if current.PromptTokensTotal != 0 && current.TokenPerSecondIn == 0 {
		if diff := current.PromptTokensTotal - prev.PromptTokensTotal; diff > 0 {
			current.TokenPerSecondIn = float64(diff) * 1000.0 / float64(durationMs)
		}
	}

	// Calculate output token throughput (tps_out)
	if current.GenerationTokensTotal != 0 && current.TokenPerSecondOut == 0 {
		if diff := current.GenerationTokensTotal - prev.GenerationTokensTotal; diff > 0 {
			current.TokenPerSecondOut = float64(diff) * 1000.0 / float64(durationMs)
		}
	}
}

// RemoveInstance removes the stored previous metrics for an instance.
// This should be called when an instance is removed.
func (c *Collector) RemoveInstance(instanceID string) {
	delete(c.previousMetrics, instanceID)
}
