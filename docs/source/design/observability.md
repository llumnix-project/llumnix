# Observability

Llumnix provides built-in observability at four levels: request performance, component diagnostics, fine-grained instance state, and engine-native metrics. Each component exposes a `/metrics` HTTP endpoint. Prometheus Operator CRDs (ServiceMonitor / PodMonitor) scrape these endpoints, and pre-built Grafana dashboards in `deploy/observability/` visualize the collected data.

## Monitoring Setup

`deploy/base/monitoring.yaml` defines Prometheus Operator resources to scrape metrics from Llumnix components:

- **ServiceMonitor** (`llumnix-control-plane`): scrapes Gateway (port 8089) and Scheduler (port 8088) via `/metrics` every 10s.
- **PodMonitor** (`llumnix-engine-neutral` / `llumnix-engine-prefill` / `llumnix-engine-decode`): scrapes engine pods managed by LeaderWorkerSet (neutral, prefill, decode) via `/metrics` every 10s, with relabeling to extract `infer_type` and `model` labels.

`monitoring.yaml` is included in `deploy/base/kustomization.yaml`. All deployment configurations under `deploy/` reference this base and inherit monitoring automatically. Note that the engine PodMonitors match only LeaderWorkerSet-based pods; deployment examples using plain Deployments (e.g., `traffic-mirror/`, `traffic-splitting/`) are not covered by the engine PodMonitors — only Gateway and Scheduler metrics are collected for those examples.

## Grafana Dashboards

Pre-built Grafana dashboard JSON files are located in `deploy/observability/`.

### Llumnix Request Dashboard

`llumnix-request-dashboard.json` — end-user request-level metrics.

| Panel | Key Metrics | Description |
|---|---|---|
| Request Rate | `request_total` | Total inference requests processed, partitioned by status code |
| Request Rate by Status | `request_total` | Request rate broken down by HTTP status code |
| Request Retry & Fallback Rate | `request_retry_total`, `request_fallback_total`, `request_fallback_retry_success_total` | Retry count, fallback count, and successful retries after fallback rate-limit (429) |
| Input / Output Token Throughput | `request_input_tokens_total`, `request_output_tokens_total` | Cumulative input (prompt) and output (completion) token counts |
| Input / Output Token Distribution | `request_input_tokens`, `request_output_tokens` | Per-request token count distribution (histogram) |
| E2E Latency | `request_e2e_latency_seconds` | End-to-end request latency in seconds |
| TTFT | `request_ttft_milliseconds` | Time to first token in milliseconds |
| TPOT | `request_tpot_milliseconds` | Time per output token: (E2E − TTFT) / (output_tokens − 1) in milliseconds |
| ITL | `request_itl_milliseconds` | Inter-token latency between consecutive output tokens in milliseconds |
| Prefix Cache Hit Ratio | `request_prefix_cache_hit_percent` | Prefix cache hit ratio on the selected instance per request (0–100%) |
| Max Prefix Cache Hit Ratio | `request_max_prefix_cache_hit_percent` | Maximum prefix cache hit ratio across all instances per request (0–100%) |

### Llumnix Component Dashboard

`llumnix-component-dashboard.json` — internal component-level metrics for Gateway, Scheduler, and system runtime.

| Panel | Key Metrics | Description |
|---|---|---|
| Queue Duration | `request_queue_duration_milliseconds` | Request queue waiting duration in milliseconds |
| Preprocess Duration | `request_preprocess_duration_milliseconds` | Request preprocessing duration in milliseconds |
| Schedule Duration | `request_schedule_duration_milliseconds` | Request scheduling phase duration in milliseconds |
| Postprocess Duration | `request_postprocess_duration_milliseconds` | Response postprocessing duration in milliseconds |
| Gateway Requests | `gateway_pending_requests`, `gateway_current_requests` | Pending and total in-flight requests in the Gateway |
| Scheduling Events | `scheduler_scheduling_total`, `scheduler_scheduling_failed_total` | Total scheduling attempts and failures |
| Rescheduling Events | `scheduler_rescheduling_total`, `scheduler_rescheduling_failed_total` | Total rescheduling operations and failures |
| CMS Refresh Metadata Duration | `scheduler_cms_refresh_metadata_duration_milliseconds` | CMS instance metadata refresh duration |
| CMS Refresh Status Duration | `scheduler_cms_refresh_status_duration_milliseconds` | CMS instance status refresh duration |
| Full-Mode Schedule Duration | `request_full_mode_schedule_duration_milliseconds` | Full-mode scheduling decision duration per request |
| Query Prefix Cache Hit Duration | `request_query_prefix_cache_hit_duration_milliseconds` | Duration of querying KVS for prefix cache hit |
| Calc Prefix Cache Hit Duration | `request_calc_prefix_cache_hit_duration_milliseconds` | Duration of calculating prefix cache hit length |
| Uptime / Goroutines / Go Memory | `uptime_seconds`, `go_goroutines`, `go_memstats_alloc_bytes` | System runtime diagnostics |

### Llumnix CMS Dashboard

`llumnix-cms-dashboard.json` — per-instance CMS status, split into Prefill and Decode sections. Each section includes:

| Panel | Key Metrics | Description |
|---|---|---|
| CMS Requests | `instance_cms_running_requests`, `instance_cms_waiting_requests`, `instance_cms_loading_requests`, `instance_cms_scheduler_waiting_to_decode_requests`, `instance_cms_scheduler_running_to_decode_requests`, `instance_cms_hybrid_scheduler_waiting_to_decode_requests` | Per-instance request counts by state |
| CMS Used Tokens | `instance_cms_used_gpu_tokens` | GPU tokens currently used per instance |
| CMS Prefill Tokens | `instance_cms_uncomputed_tokens_all_waiting_prefills`, `instance_cms_uncomputed_tokens_scheduler_running_prefills`, `instance_cms_unallocated_tokens_scheduler_running_prefills` | Uncomputed/unallocated tokens for prefill requests |
| CMS Decode Tokens | `instance_cms_unallocated_tokens_hybrid_scheduler_waiting_decodes`, `instance_cms_hybrid_scheduler_waiting_to_decode_tokens`, `instance_cms_scheduler_waiting_to_decode_tokens`, `instance_cms_scheduler_running_to_decode_tokens`, `instance_cms_tokens_loading_requests` | Tokens for decode and loading requests |
| CMS Inflight Dispatch Requests | `instance_cms_inflight_dispatch_requests`, `instance_cms_inflight_dispatch_prefill_requests`, `instance_cms_inflight_dispatch_decode_requests` | Inflight dispatch request counts |
| CMS Inflight Dispatch Tokens | `instance_cms_uncomputed_tokens_inflight_dispatch_prefill_requests`, `instance_cms_tokens_inflight_dispatch_decode_requests` | Tokens for inflight dispatch requests |
| CMS KV Cache Usage Ratio | `instance_cms_kv_cache_usage_ratio_projected` | Projected KV cache usage ratio per instance |
| CMS Decode Batch Size | `instance_cms_decode_batch_size` | Decode batch size per instance |
| CMS All Prefill/Decode Tokens | `instance_cms_all_prefills_tokens_num`, `instance_cms_all_decodes_tokens_num` | Total tokens for all prefill/decode requests per instance |

The dashboard also includes a **Selected Instance Scheduling Metrics** section showing the scheduling decision context for the chosen instance:

| Panel | Key Metrics | Description |
|---|---|---|
| Selected Instance KV Cache Usage Ratio | `selected_instance_kv_cache_usage_ratio_projected` | Projected KV cache usage ratio on the selected instance |
| Selected Instance Decode Batch Size | `selected_instance_decode_batch_size` | Decode batch size on the selected instance |
| Selected Instance Prefill/Decode Tokens | `selected_instance_all_prefills_tokens_num`, `selected_instance_all_decodes_tokens_num` | Total prefill/decode tokens on the selected instance |
| Selected Instance Predicted TTFT | `selected_instance_predicted_ttft` | Predicted TTFT for the selected instance in milliseconds |
| Selected Instance Predicted TPOT | `selected_instance_predicted_tpot` | Predicted TPOT for the selected instance in milliseconds |

### Llumnix LRS Dashboard

`llumnix-lrs-dashboard.json` — per-instance Local Real-time State (LRS), split into Prefill and Decode sections.

| Panel | Key Metrics | Description |
|---|---|---|
| LRS Requests | `instance_lrs_running_requests`, `instance_lrs_waiting_requests`, `instance_lrs_total_requests` | Running, waiting, and total requests on a backend endpoint |
| LRS Tokens | `instance_lrs_running_tokens`, `instance_lrs_waiting_tokens`, `instance_lrs_total_tokens` | Running, waiting, and total token counts on a backend endpoint |

### vLLM Dashboard

`vllm-dashboard.json` — engine-native vLLM metrics.

| Panel | Description |
|---|---|
| E2E Request Latency | End-to-end request latency from vLLM |
| Time To First Token Latency | TTFT from vLLM |
| Inter-Token Latency | ITL from vLLM |
| Scheduler State | vLLM internal scheduler state (running/waiting/swapped) |
| Cache Utilization | KV cache utilization ratio |
| Token Throughput | Token generation throughput |
| Finish Reason | Request completion reason distribution |
| Queue Time | Time spent in vLLM queue |
| Prefill and Decode Time | Per-request prefill and decode time |
| Request Prompt/Generation Length | Prompt and generation length distributions (heatmap) |
