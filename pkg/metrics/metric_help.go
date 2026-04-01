package metrics

// This file registers human-readable Help descriptions and per-metric
// histogram bucket configurations for all known metrics.
//
// Help descriptions appear in the Prometheus /metrics endpoint and
// are used by Grafana to show tooltips. Per-metric buckets ensure
// each histogram has bucket boundaries matching its value range.

func init() {
	// ---- Request-level counters ----
	RegisterHelp("request_total", "Total number of inference requests processed, partitioned by status code")
	RegisterHelp("request_retry_total", "Total number of request retries due to transient backend errors")
	RegisterHelp("request_fallback_total", "Total number of requests routed to fallback endpoints")
	RegisterHelp("request_fallback_retry_success_total", "Total number of successful retries after fallback rate-limit (429)")
	RegisterHelp("request_input_tokens_total", "Cumulative count of input (prompt) tokens across all requests")
	RegisterHelp("request_output_tokens_total", "Cumulative count of output (completion) tokens across all requests")

	// ---- Request-level latencies ----
	RegisterHelp("request_e2e_latency_seconds", "End-to-end request latency in seconds")
	RegisterHelp("request_ttft_milliseconds", "Time to first token in milliseconds")
	RegisterHelp("request_itl_milliseconds", "Inter-token latency: gap between consecutive output tokens in milliseconds (token-weighted)")
	RegisterHelp("request_tpot_milliseconds", "Time per output token: (E2E - TTFT) / (output_tokens - 1) in milliseconds (request-weighted)")
	RegisterHelp("request_queue_duration_milliseconds", "Request queue waiting duration in milliseconds")
	RegisterHelp("request_schedule_duration_milliseconds", "Request scheduling phase duration in milliseconds")
	RegisterHelp("request_preprocess_duration_milliseconds", "Request preprocessing duration in milliseconds")
	RegisterHelp("request_postprocess_duration_milliseconds", "Response postprocessing duration in milliseconds")
	RegisterHelp("request_full_mode_schedule_duration_milliseconds", "Full-mode scheduling decision duration per request in milliseconds")
	RegisterHelp("request_query_prefix_cache_hit_duration_milliseconds", "Duration of querying KVS for prefix cache hit per request in milliseconds")
	RegisterHelp("request_calc_prefix_cache_hit_duration_milliseconds", "Duration of calculating prefix cache hit length per request in milliseconds")
	RegisterHelp("request_prefix_cache_hit_percent", "Prefix cache hit ratio on the selected instance per request, expressed as 0-100 percent")
	RegisterHelp("request_max_prefix_cache_hit_percent", "Maximum prefix cache hit ratio across all instances per request, expressed as 0-100 percent")

	// ---- Request-level token distributions ----
	RegisterHelp("request_input_tokens", "Distribution of input (prompt) token counts per request")
	RegisterHelp("request_output_tokens", "Distribution of output (completion) token counts per request")

	// ---- Service-level: gateway gauges ----
	RegisterHelp("gateway_pending_requests", "Number of requests currently waiting in the buffer queue")
	RegisterHelp("gateway_current_requests", "Current total number of requests in the gateway")

	// ---- Service-level: scheduler counters ----
	RegisterHelp("scheduler_rescheduling_total", "Total number of rescheduling operations triggered")
	RegisterHelp("scheduler_rescheduling_failed_total", "Total number of failed rescheduling operations")
	RegisterHelp("scheduler_scheduling_failed_total", "Total number of failed scheduling attempts, partitioned by error type")
	RegisterHelp("scheduler_scheduling_total", "Total number of scheduling attempts")

	// ---- Service-level: scheduler CMS latencies ----
	RegisterHelp("scheduler_cms_refresh_metadata_duration_milliseconds", "CMS instance metadata refresh duration in milliseconds")
	RegisterHelp("scheduler_cms_refresh_status_duration_milliseconds", "CMS instance status refresh duration in milliseconds")

	// ---- Instance-level: LRS gauges ----
	RegisterHelp("instance_lrs_running_tokens", "Running token count on a backend endpoint")
	RegisterHelp("instance_lrs_waiting_tokens", "Tokens waiting to be processed on a backend endpoint")
	RegisterHelp("instance_lrs_total_tokens", "Total token count on a backend endpoint")
	RegisterHelp("instance_lrs_running_requests", "Running requests on a backend endpoint")
	RegisterHelp("instance_lrs_waiting_requests", "Requests waiting on a backend endpoint")
	RegisterHelp("instance_lrs_total_requests", "Total requests on a backend endpoint")

	// ---- Instance-level: CMS gauges ----
	// GPU tokens
	RegisterHelp("instance_cms_used_gpu_tokens", "GPU tokens currently used per instance")

	// Request counts
	RegisterHelp("instance_cms_waiting_requests", "Waiting requests per instance")
	RegisterHelp("instance_cms_loading_requests", "Loading requests per instance")
	RegisterHelp("instance_cms_running_requests", "Running requests per instance")
	RegisterHelp("instance_cms_scheduler_waiting_to_decode_requests", "Scheduler waiting-to-decode requests per instance")
	RegisterHelp("instance_cms_scheduler_running_to_decode_requests", "Scheduler running-to-decode requests per instance")
	RegisterHelp("instance_cms_hybrid_scheduler_waiting_to_decode_requests", "Hybrid scheduler waiting-to-decode requests per instance")

	// Prefill tokens
	RegisterHelp("instance_cms_uncomputed_tokens_all_waiting_prefills", "Uncomputed tokens across all waiting prefill requests per instance")
	RegisterHelp("instance_cms_uncomputed_tokens_scheduler_running_prefills", "Uncomputed tokens for scheduler running prefills per instance")
	RegisterHelp("instance_cms_unallocated_tokens_scheduler_running_prefills", "Unallocated tokens for scheduler running prefills per instance")

	// Decode tokens
	RegisterHelp("instance_cms_unallocated_tokens_hybrid_scheduler_waiting_decodes", "Unallocated tokens for hybrid scheduler waiting decodes per instance")
	RegisterHelp("instance_cms_hybrid_scheduler_waiting_to_decode_tokens", "Hybrid scheduler waiting-to-decode tokens per instance")
	RegisterHelp("instance_cms_scheduler_waiting_to_decode_tokens", "Scheduler waiting-to-decode tokens per instance")
	RegisterHelp("instance_cms_scheduler_running_to_decode_tokens", "Scheduler running-to-decode tokens per instance")
	RegisterHelp("instance_cms_tokens_loading_requests", "Tokens for loading requests per instance")

	// Local account
	RegisterHelp("instance_cms_inflight_dispatch_requests", "Inflight dispatch requests per instance")
	RegisterHelp("instance_cms_inflight_dispatch_prefill_requests", "Inflight dispatch prefill requests per instance")
	RegisterHelp("instance_cms_inflight_dispatch_decode_requests", "Inflight dispatch decode requests per instance")
	RegisterHelp("instance_cms_uncomputed_tokens_inflight_dispatch_prefill_requests", "Uncomputed tokens for inflight dispatch prefill requests per instance")
	RegisterHelp("instance_cms_tokens_inflight_dispatch_decode_requests", "Tokens for inflight dispatch decode requests per instance")

	// ---- Instance-level: CMS scheduling policy computed metrics ----
	RegisterHelp("instance_cms_kv_cache_usage_ratio_projected", "Projected KV cache usage ratio per instance")
	RegisterHelp("instance_cms_decode_batch_size", "Decode batch size per instance")
	RegisterHelp("instance_cms_all_prefills_tokens_num", "Total tokens for all prefill requests per instance")
	RegisterHelp("instance_cms_all_decodes_tokens_num", "Total tokens for all decode requests per instance")

	// ---- Instance-level: selected instance prediction metrics ----
	RegisterHelp("selected_instance_predicted_ttft", "Predicted TTFT for the selected instance in milliseconds")
	RegisterHelp("selected_instance_predicted_tpot", "Predicted TPOT for the selected instance in milliseconds")
	RegisterHelp("selected_instance_kv_cache_usage_ratio_projected", "Projected KV cache usage ratio on the selected instance")
	RegisterHelp("selected_instance_decode_batch_size", "Decode batch size on the selected instance")
	RegisterHelp("selected_instance_all_prefills_tokens_num", "Total tokens for all prefill requests on the selected instance")
	RegisterHelp("selected_instance_all_decodes_tokens_num", "Total tokens for all decode requests on the selected instance")

	// ---- Per-metric bucket configurations ----
	// E2E request latency (seconds): 0.1s to 300s (5min).
	// LLM inference ranges from sub-second (short completions) to minutes (long generation / reasoning).
	secondsBuckets := []float64{0.1, 0.25, 0.5, 1, 2, 5, 10, 20, 30, 60, 90, 120, 180, 240, 300}
	RegisterBuckets("request_e2e_latency_seconds", secondsBuckets)

	// TTFT (milliseconds): 10ms to 60s.
	// Dominated by queue wait + prefill compute; can spike under load.
	ttftBuckets := []float64{10, 20, 50, 100, 200, 500, 1000, 2000, 5000, 10000, 20000, 30000, 60000}
	RegisterBuckets("request_ttft_milliseconds", ttftBuckets)

	// Per-token latencies: typically 1ms~500ms
	tokenLatencyBuckets := []float64{1, 2, 5, 10, 15, 20, 30, 50, 75, 100, 150, 200, 300, 500}
	RegisterBuckets("request_itl_milliseconds", tokenLatencyBuckets)
	RegisterBuckets("request_tpot_milliseconds", tokenLatencyBuckets)

	// Token count distributions: 1 to 100k tokens
	tokenCountBuckets := []float64{1, 4, 16, 64, 128, 256, 512, 1024, 2048, 4096, 8192, 16384, 32768, 65536, 131072}
	RegisterBuckets("request_input_tokens", tokenCountBuckets)
	RegisterBuckets("request_output_tokens", tokenCountBuckets)

	// Internal pipeline stage latencies (milliseconds): 0.1ms to 100ms.
	// Scheduling, preprocess, postprocess are in-memory operations, typically microsecond to low-millisecond.
	stageBuckets := []float64{0.1, 0.2, 0.5, 1, 2, 5, 10, 20, 50, 100}
	RegisterBuckets("request_schedule_duration_milliseconds", stageBuckets)
	RegisterBuckets("request_preprocess_duration_milliseconds", stageBuckets)
	RegisterBuckets("request_postprocess_duration_milliseconds", stageBuckets)
	RegisterBuckets("request_full_mode_schedule_duration_milliseconds", stageBuckets)
	RegisterBuckets("request_query_prefix_cache_hit_duration_milliseconds", stageBuckets)
	RegisterBuckets("request_calc_prefix_cache_hit_duration_milliseconds", stageBuckets)

	// Queue waiting latency (milliseconds): 1ms to 5s.
	queueBuckets := []float64{1, 5, 10, 50, 100, 200, 500, 1000, 2000, 5000}
	RegisterBuckets("request_queue_duration_milliseconds", queueBuckets)

	// Prefix cache hit ratio: 0-100%
	percentBuckets := []float64{0, 10, 20, 30, 40, 50, 60, 70, 80, 90, 100}
	RegisterBuckets("request_prefix_cache_hit_percent", percentBuckets)
	RegisterBuckets("request_max_prefix_cache_hit_percent", percentBuckets)

	// CMS refresh latencies (milliseconds): 0.1ms to 100ms.
	usBuckets := []float64{0.1, 0.2, 0.5, 1, 2, 5, 10, 20, 50, 100}
	RegisterBuckets("scheduler_cms_refresh_metadata_duration_milliseconds", usBuckets)
	RegisterBuckets("scheduler_cms_refresh_status_duration_milliseconds", usBuckets)

	// ---- Selected instance scheduling metric buckets ----
	// Predicted TTFT (milliseconds): same as request TTFT range.
	RegisterBuckets("selected_instance_predicted_ttft", ttftBuckets)
	// Predicted TPOT (milliseconds): same as per-token latency range.
	RegisterBuckets("selected_instance_predicted_tpot", tokenLatencyBuckets)
	// KV cache usage ratio: 0 to 1.
	ratioBuckets := []float64{0, 0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8, 0.9, 1.0}
	RegisterBuckets("selected_instance_kv_cache_usage_ratio_projected", ratioBuckets)
	// Decode batch size: 1 to 1000.
	batchSizeBuckets := []float64{1, 2, 5, 10, 20, 50, 100, 200, 500, 1000}
	RegisterBuckets("selected_instance_decode_batch_size", batchSizeBuckets)
	// Token counts: same as request token count range.
	RegisterBuckets("selected_instance_all_prefills_tokens_num", tokenCountBuckets)
	RegisterBuckets("selected_instance_all_decodes_tokens_num", tokenCountBuckets)
}
