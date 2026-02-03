package metrics

const (
	LlumnixMetricRescheduleCount                               = "llumnix_reschedule_count"
	LlumnixMetricRescheduleFailedCount                         = "llumnix_reschedule_failed_count"
	LlumnixMetricScheduleFailedCount                           = "llumnix_schedule_failed_count"
	LlumnixMetricScheduleLatencyMicroseconds                   = "llumnix_schedule_latency_us"
	LlumnixMetricInstanceNumUncomputedTokensAllWaitingPrefills = "llumnix_instance_uncomputed_tokens_all_waiting_prefills"
	LlumnixMetricInstanceNumUsedGpuTokens                      = "llumnix_instance_tokens_used"
	LlumnixMetricCmsRefreshInstanceMetadataLatencyMicroseconds = "llumnix_cms_refresh_metadata_latency_us"
	LlumnixMetricCmsRefreshInstanceStatusLatencyMicroseconds   = "llumnix_cms_refresh_status_latency_us"
)

var enableLlumnixMetrics = false

func EnableLlumnixMetrics() {
	enableLlumnixMetrics = true
}

// counter
func IncrLlumnixCounterBy(k string, l Labels, value int) {
	if !enableLlumnixMetrics {
		return
	}
	Counter(k, l).IncrBy(value)
}

func IncrLlumnixCounterByOne(k string, l Labels) {
	if !enableLlumnixMetrics {
		return
	}
	Counter(k, l).IncrByOne()
}

// latency
func AddLlumnixLatency(k string, l Labels, value int64) {
	if !enableLlumnixMetrics {
		return
	}
	Latency(k, l).Add(value)
}

func AddManyLlumnixLatency(k string, l Labels, value []int64) {
	if !enableLlumnixMetrics {
		return
	}
	Latency(k, l).AddMany(value)
}

// status_value
func SetLlumnixStatusValue(k string, l Labels, value float32) {
	if !enableLlumnixMetrics {
		return
	}
	StatusValue(k, l).Set(value)
}
