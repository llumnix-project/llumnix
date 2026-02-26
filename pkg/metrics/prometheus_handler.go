package metrics

import (
	"fmt"
	"net/http"
	"runtime"
	"sort"
	"strings"
	"time"
)

// PrometheusHandler exports metrics in Prometheus text format
// It provides an HTTP handler that exposes all registered metrics
//
// The handler exports three types of metrics:
//  1. Counter metrics: Cumulative values and QPS (queries per second)
//  2. Latency metrics: Min, max, mean, and percentiles (p50, p75, p90, p99)
//  3. Status metrics: Current gauge values
//
// Usage:
//
//	handler := metrics.NewPrometheusHandler()
//	http.Handle("/metrics", handler)
//
// The metrics endpoint can be scraped by Prometheus:
//
//	scrape_configs:
//	  - job_name: 'llm-gateway'
//	    static_configs:
//	      - targets: ['localhost:8080']
type PrometheusHandler struct {
	startTime time.Time
}

// NewPrometheusHandler creates a new Prometheus metrics handler
func NewPrometheusHandler() *PrometheusHandler {
	return &PrometheusHandler{
		startTime: time.Now(),
	}
}

// ServeHTTP implements the http.Handler interface
// It exports all registered metrics in Prometheus text format
func (h *PrometheusHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	var output strings.Builder

	// Export counter metrics
	h.exportCounterMetrics(&output)

	// Export latency metrics
	h.exportLatencyMetrics(&output)

	// Export status value metrics
	h.exportStatusMetrics(&output)

	// Export runtime metrics
	h.exportRuntimeMetrics(&output)

	// Export process uptime metric
	uptime := time.Since(h.startTime).Seconds()
	output.WriteString("# HELP llm_gateway_uptime_seconds Gateway uptime in seconds\n")
	output.WriteString("# TYPE llm_gateway_uptime_seconds gauge\n")
	output.WriteString(fmt.Sprintf("llm_gateway_uptime_seconds %.2f\n", uptime))

	w.Write([]byte(output.String()))
}

// exportCounterMetrics exports counter metrics to Prometheus format
func (h *PrometheusHandler) exportCounterMetrics(output *strings.Builder) {
	counterGroup.mu.Lock()
	counters := make(map[string]*CounterValue)
	for k, v := range counterGroup.data {
		counters[k] = v
	}
	counterGroup.mu.Unlock()

	// Group metrics by base name
	metricGroups := make(map[string][]*CounterValue)
	for _, counter := range counters {
		metricGroups[counter.key] = append(metricGroups[counter.key], counter)
	}

	// Export each metric group
	for metricName, group := range metricGroups {
		if len(group) == 0 {
			continue
		}

		// Write HELP and TYPE
		output.WriteString(fmt.Sprintf("# HELP %s Counter metric\n", h.sanitizeMetricName(metricName)))
		output.WriteString(fmt.Sprintf("# TYPE %s counter\n", h.sanitizeMetricName(metricName)))

		// Write metric values
		for _, counter := range group {
			value := counter.counter.Load()
			labels := h.formatLabels(counter.labels)
			output.WriteString(fmt.Sprintf("%s%s %d\n", h.sanitizeMetricName(metricName), labels, value))
		}
		output.WriteString("\n")
	}
}

// exportLatencyMetrics exports latency metrics to Prometheus format
func (h *PrometheusHandler) exportLatencyMetrics(output *strings.Builder) {
	latencyGroup.mu.Lock()
	latencies := make(map[string]*LatencyValue)
	for k, v := range latencyGroup.data {
		latencies[k] = v
	}
	latencyGroup.mu.Unlock()

	// Group metrics by base name
	metricGroups := make(map[string][]*LatencyValue)
	for _, latency := range latencies {
		metricGroups[latency.key] = append(metricGroups[latency.key], latency)
	}

	// Export each metric group
	for metricName, group := range metricGroups {
		if len(group) == 0 {
			continue
		}

		// Calculate current values (non-destructive peek)
		for _, latency := range group {
			latency.mu.Lock()
			if len(latency.data) == 0 {
				latency.mu.Unlock()
				continue
			}

			// Create a copy for calculation
			dataCopy := make([]int64, len(latency.data))
			copy(dataCopy, latency.data)
			latency.mu.Unlock()

			// Calculate statistics
			sort.Slice(dataCopy, func(i, j int) bool {
				return dataCopy[i] < dataCopy[j]
			})

			length := len(dataCopy)
			sum := int64(0)
			for _, v := range dataCopy {
				sum += v
			}

			min := dataCopy[0]
			max := dataCopy[length-1]
			mean := sum / int64(length)

			// Calculate percentiles
			p50 := h.calculatePercentile(dataCopy, 0.50)
			p75 := h.calculatePercentile(dataCopy, 0.75)
			p90 := h.calculatePercentile(dataCopy, 0.90)
			p99 := h.calculatePercentile(dataCopy, 0.99)

			labels := h.formatLabels(latency.labels)
			baseName := h.sanitizeMetricName(metricName)

			// Export min
			output.WriteString(fmt.Sprintf("# HELP %s_min Minimum latency in milliseconds\n", baseName))
			output.WriteString(fmt.Sprintf("# TYPE %s_min gauge\n", baseName))
			output.WriteString(fmt.Sprintf("%s_min%s %d\n\n", baseName, labels, min))

			// Export max
			output.WriteString(fmt.Sprintf("# HELP %s_max Maximum latency in milliseconds\n", baseName))
			output.WriteString(fmt.Sprintf("# TYPE %s_max gauge\n", baseName))
			output.WriteString(fmt.Sprintf("%s_max%s %d\n\n", baseName, labels, max))

			// Export mean
			output.WriteString(fmt.Sprintf("# HELP %s_mean Mean latency in milliseconds\n", baseName))
			output.WriteString(fmt.Sprintf("# TYPE %s_mean gauge\n", baseName))
			output.WriteString(fmt.Sprintf("%s_mean%s %d\n\n", baseName, labels, mean))

			// Export percentiles
			output.WriteString(fmt.Sprintf("# HELP %s_percentile Latency percentiles in milliseconds\n", baseName))
			output.WriteString(fmt.Sprintf("# TYPE %s_percentile gauge\n", baseName))

			p50Labels := h.formatLabelsWithQuantile(latency.labels, "0.5")
			output.WriteString(fmt.Sprintf("%s_percentile%s %d\n", baseName, p50Labels, p50))

			p75Labels := h.formatLabelsWithQuantile(latency.labels, "0.75")
			output.WriteString(fmt.Sprintf("%s_percentile%s %d\n", baseName, p75Labels, p75))

			p90Labels := h.formatLabelsWithQuantile(latency.labels, "0.9")
			output.WriteString(fmt.Sprintf("%s_percentile%s %d\n", baseName, p90Labels, p90))

			p99Labels := h.formatLabelsWithQuantile(latency.labels, "0.99")
			output.WriteString(fmt.Sprintf("%s_percentile%s %d\n", baseName, p99Labels, p99))

			output.WriteString("\n")
		}
	}
}

// exportStatusMetrics exports status value metrics to Prometheus format
func (h *PrometheusHandler) exportStatusMetrics(output *strings.Builder) {
	statusValueGroup.mu.Lock()
	statusValues := make(map[string]*StatusData)
	for k, v := range statusValueGroup.data {
		statusValues[k] = v
	}
	statusValueGroup.mu.Unlock()

	// Group metrics by base name
	metricGroups := make(map[string][]*StatusData)
	for _, status := range statusValues {
		metricGroups[status.key] = append(metricGroups[status.key], status)
	}

	// Export each metric group
	for metricName, group := range metricGroups {
		if len(group) == 0 {
			continue
		}

		// Write HELP and TYPE
		output.WriteString(fmt.Sprintf("# HELP %s Status value metric\n", h.sanitizeMetricName(metricName)))
		output.WriteString(fmt.Sprintf("# TYPE %s gauge\n", h.sanitizeMetricName(metricName)))

		// Write metric values
		for _, status := range group {
			value := status.mValue.Load()
			labels := h.formatLabels(status.labels)
			output.WriteString(fmt.Sprintf("%s%s %.3f\n", h.sanitizeMetricName(metricName), labels, float32(value)/1000))
		}
		output.WriteString("\n")
	}
}

// sanitizeMetricName converts metric name to Prometheus-compatible format
func (h *PrometheusHandler) sanitizeMetricName(name string) string {
	// Replace invalid characters with underscores
	name = strings.ReplaceAll(name, "-", "_")
	name = strings.ReplaceAll(name, ".", "_")
	name = strings.ReplaceAll(name, " ", "_")
	return name
}

// formatLabels formats labels in Prometheus format: {label1="value1",label2="value2"}
func (h *PrometheusHandler) formatLabels(labels Labels) string {
	if len(labels) == 0 {
		return ""
	}

	var parts []string
	for _, label := range labels {
		// Escape special characters in label values
		value := strings.ReplaceAll(label.Value, "\\", "\\\\")
		value = strings.ReplaceAll(value, "\"", "\\\"")
		value = strings.ReplaceAll(value, "\n", "\\n")
		parts = append(parts, fmt.Sprintf("%s=\"%s\"", label.Name, value))
	}

	return fmt.Sprintf("{%s}", strings.Join(parts, ","))
}

// formatLabelsWithQuantile formats labels with an additional quantile label
func (h *PrometheusHandler) formatLabelsWithQuantile(labels Labels, quantile string) string {
	newLabels := labels.Append(Label{Name: "quantile", Value: quantile})
	return h.formatLabels(newLabels)
}

// calculatePercentile calculates the percentile value from sorted data
func (h *PrometheusHandler) calculatePercentile(sortedData []int64, percentile float64) int64 {
	length := len(sortedData)
	if length == 0 {
		return 0
	}

	pos := percentile * float64(length+1)
	if pos < 1.0 {
		return sortedData[0]
	} else if pos >= float64(length) {
		return sortedData[length-1]
	} else {
		lower := float64(sortedData[int(pos)-1])
		upper := float64(sortedData[int(pos)])
		return int64(lower + (pos-float64(int(pos)))*(upper-lower))
	}
}

// exportRuntimeMetrics exports Go runtime metrics to Prometheus format
func (h *PrometheusHandler) exportRuntimeMetrics(output *strings.Builder) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	// Export goroutines count
	output.WriteString("# HELP go_goroutines Number of goroutines that currently exist\n")
	output.WriteString("# TYPE go_goroutines gauge\n")
	output.WriteString(fmt.Sprintf("go_goroutines %d\n\n", runtime.NumGoroutine()))

	// Export heap allocated memory in bytes
	output.WriteString("# HELP go_memstats_heap_alloc_bytes Number of heap bytes allocated and still in use\n")
	output.WriteString("# TYPE go_memstats_heap_alloc_bytes gauge\n")
	output.WriteString(fmt.Sprintf("go_memstats_heap_alloc_bytes %d\n\n", m.HeapAlloc))

	// Export heap in-use memory in bytes
	output.WriteString("# HELP go_memstats_heap_inuse_bytes Number of heap bytes that are in use\n")
	output.WriteString("# TYPE go_memstats_heap_inuse_bytes gauge\n")
	output.WriteString(fmt.Sprintf("go_memstats_heap_inuse_bytes %d\n\n", m.HeapInuse))

	// Export heap objects count
	output.WriteString("# HELP go_memstats_heap_objects Number of allocated heap objects\n")
	output.WriteString("# TYPE go_memstats_heap_objects gauge\n")
	output.WriteString(fmt.Sprintf("go_memstats_heap_objects %d\n\n", m.HeapObjects))
}
