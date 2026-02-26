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

	// ========== Category 1: GC (Garbage Collection) Metrics ==========

	// Export GC pause time in seconds
	output.WriteString("# HELP go_gc_duration_seconds GC pause duration in seconds\n")
	output.WriteString("# TYPE go_gc_duration_seconds summary\n")
	if m.NumGC > 0 {
		// Calculate GC pause time percentiles from PauseNs ring buffer
		// PauseNs is a circular buffer of recent GC pause times in nanoseconds
		pauseTimes := make([]uint64, 0, 256)
		numGC := m.NumGC
		for i := uint32(0); i < 256 && i < numGC; i++ {
			idx := (numGC - 1 - i) % 256
			pauseTimes = append(pauseTimes, m.PauseNs[idx])
		}

		// Calculate quantiles from pause times
		if len(pauseTimes) > 0 {
			// Sort for percentile calculation
			sortedPauses := make([]uint64, len(pauseTimes))
			copy(sortedPauses, pauseTimes)
			// Simple bubble sort for small arrays
			for i := 0; i < len(sortedPauses); i++ {
				for j := i + 1; j < len(sortedPauses); j++ {
					if sortedPauses[i] > sortedPauses[j] {
						sortedPauses[i], sortedPauses[j] = sortedPauses[j], sortedPauses[i]
					}
				}
			}

			// Export GC pause quantiles (convert nanoseconds to seconds)
			p50Idx := len(sortedPauses) * 50 / 100
			p90Idx := len(sortedPauses) * 90 / 100
			p99Idx := len(sortedPauses) * 99 / 100

			output.WriteString(fmt.Sprintf("go_gc_duration_seconds{quantile=\"0.5\"} %.9f\n", float64(sortedPauses[p50Idx])/1e9))
			output.WriteString(fmt.Sprintf("go_gc_duration_seconds{quantile=\"0.9\"} %.9f\n", float64(sortedPauses[p90Idx])/1e9))
			output.WriteString(fmt.Sprintf("go_gc_duration_seconds{quantile=\"0.99\"} %.9f\n", float64(sortedPauses[p99Idx])/1e9))
		}
	}
	output.WriteString("\n")

	// Export last GC time
	output.WriteString("# HELP go_memstats_last_gc_time_seconds Last GC time in Unix timestamp\n")
	output.WriteString("# TYPE go_memstats_last_gc_time_seconds gauge\n")
	output.WriteString(fmt.Sprintf("go_memstats_last_gc_time_seconds %.3f\n\n", float64(m.LastGC)/1e9))

	// Export GC system memory
	output.WriteString("# HELP go_memstats_gc_sys_bytes Memory used by GC metadata\n")
	output.WriteString("# TYPE go_memstats_gc_sys_bytes gauge\n")
	output.WriteString(fmt.Sprintf("go_memstats_gc_sys_bytes %d\n\n", m.GCSys))

	// Export GC count
	output.WriteString("# HELP go_memstats_num_gc Total number of GC cycles completed\n")
	output.WriteString("# TYPE go_memstats_num_gc counter\n")
	output.WriteString(fmt.Sprintf("go_memstats_num_gc %d\n\n", m.NumGC))

	// Export forced GC count
	output.WriteString("# HELP go_memstats_num_forced_gc Total number of forced GC cycles\n")
	output.WriteString("# TYPE go_memstats_num_forced_gc counter\n")
	output.WriteString(fmt.Sprintf("go_memstats_num_forced_gc %d\n\n", m.NumForcedGC))

	// ========== Category 2: System Thread Metrics ==========

	// Export goroutines count
	output.WriteString("# HELP go_goroutines Number of goroutines that currently exist\n")
	output.WriteString("# TYPE go_goroutines gauge\n")
	output.WriteString(fmt.Sprintf("go_goroutines %d\n\n", runtime.NumGoroutine()))

	// Export OS threads count
	// IMPORTANT: This metric is critical for CGO-heavy applications
	// High thread count may indicate thread leaks or excessive blocking operations
	// Note: Go runtime does not directly expose OS thread count via runtime package
	// This reports GOMAXPROCS which is the limit of OS threads that can execute Go code
	output.WriteString("# HELP go_threads Number of OS threads that can execute user-level Go code simultaneously\n")
	output.WriteString("# TYPE go_threads gauge\n")
	output.WriteString(fmt.Sprintf("go_threads %d\n\n", runtime.GOMAXPROCS(0)))

	// ========== Category 3: Memory Fine-Grained Metrics ==========

	// Export total system memory
	output.WriteString("# HELP go_memstats_sys_bytes Total bytes of memory obtained from the OS\n")
	output.WriteString("# TYPE go_memstats_sys_bytes gauge\n")
	output.WriteString(fmt.Sprintf("go_memstats_sys_bytes %d\n\n", m.Sys))

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

	// Export stack memory
	output.WriteString("# HELP go_memstats_stack_inuse_bytes Bytes in stack spans currently in use\n")
	output.WriteString("# TYPE go_memstats_stack_inuse_bytes gauge\n")
	output.WriteString(fmt.Sprintf("go_memstats_stack_inuse_bytes %d\n\n", m.StackInuse))

	// Export stack system memory
	output.WriteString("# HELP go_memstats_stack_sys_bytes Bytes of stack memory obtained from OS\n")
	output.WriteString("# TYPE go_memstats_stack_sys_bytes gauge\n")
	output.WriteString(fmt.Sprintf("go_memstats_stack_sys_bytes %d\n\n", m.StackSys))

	// Export mspan memory
	output.WriteString("# HELP go_memstats_mspan_inuse_bytes Bytes of allocated mspan structures\n")
	output.WriteString("# TYPE go_memstats_mspan_inuse_bytes gauge\n")
	output.WriteString(fmt.Sprintf("go_memstats_mspan_inuse_bytes %d\n\n", m.MSpanInuse))

	// Export mcache memory
	output.WriteString("# HELP go_memstats_mcache_inuse_bytes Bytes of allocated mcache structures\n")
	output.WriteString("# TYPE go_memstats_mcache_inuse_bytes gauge\n")
	output.WriteString(fmt.Sprintf("go_memstats_mcache_inuse_bytes %d\n\n", m.MCacheInuse))

	// Export total allocations
	output.WriteString("# HELP go_memstats_mallocs_total Total number of heap objects allocated\n")
	output.WriteString("# TYPE go_memstats_mallocs_total counter\n")
	output.WriteString(fmt.Sprintf("go_memstats_mallocs_total %d\n\n", m.Mallocs))

	// Export total frees
	output.WriteString("# HELP go_memstats_frees_total Total number of heap objects freed\n")
	output.WriteString("# TYPE go_memstats_frees_total counter\n")
	output.WriteString(fmt.Sprintf("go_memstats_frees_total %d\n\n", m.Frees))

	// Export total allocated bytes
	output.WriteString("# HELP go_memstats_alloc_bytes_total Total bytes allocated (even if freed)\n")
	output.WriteString("# TYPE go_memstats_alloc_bytes_total counter\n")
	output.WriteString(fmt.Sprintf("go_memstats_alloc_bytes_total %d\n\n", m.TotalAlloc))

	// ========== Category 4: Process Resource Metrics ==========

	// Export CPU count
	output.WriteString("# HELP go_cpu_count Number of logical CPUs available\n")
	output.WriteString("# TYPE go_cpu_count gauge\n")
	output.WriteString(fmt.Sprintf("go_cpu_count %d\n\n", runtime.NumCPU()))
}
