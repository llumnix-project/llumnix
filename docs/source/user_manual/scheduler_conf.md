# Scheduler Configuration Guide

## Overview

For detailed design information about the Scheduler, please refer to
the [Scheduler Architecture Design Document](../design/scheduler/index.md).

---

## Policy Framework

### Usage and Extension Guidelines

- **Choosing a policy and mode**
    - SLO-restricted workloads with profiling data: **SLO + full-mode**.
    - Utilization-focused workloads with cache locality needs: **load-balance + full-mode + cache-aware scheduling**.
    - Simple deployments without CMS: **load-balance + lite-mode**, with `num_requests` or `num_tokens` as the load
      metric.

- **Switching between full-mode and lite-mode**
    - Flag: `--enable-full-mode-scheduling`.
    - **Full-mode (default)**: `--enable-full-mode-scheduling=true`.
    - **Lite-mode**: `--enable-full-mode-scheduling=false`.
    - Advanced scheduling features require full-mode.

- **Extending with new policies**
    - Reuse existing metrics, filters, and selectors; rewire combinations per infer type.
    - For new signals: implement `instanceSchedulingMetric` in `metrics.go`, register in `getSchedulingMetric`,
      configure in `schedule_policy_registry.go`.

---

## Cache-Aware Scheduling

### Prerequisites

Llumnix's cache-aware scheduling requires the inference engine to use a global KV cache store (e.g. Mooncake). Llumnix
queries the global KV cache store's metadata service to obtain prefix cache hit information for each request, so this
feature is only applicable when such a store is deployed.

Cache-aware scheduling is **disabled by default**. It is only supported in **full-mode scheduling**; the scheduler
forcibly disables it when full-mode is not enabled.

### Configuration

Key configuration flags (`cmd/config/config.go`):

| Flag                                     | Default       | Description                                               |
|------------------------------------------|---------------|-----------------------------------------------------------|
| `--enable-cache-aware-scheduling`        | `false`       | Enable or disable cache-aware scheduling                  |
| `--cache-aware-scheduling-min-tokens`    | `1024`        | Minimum prompt length to trigger cache-aware logic        |
| `--kvs-backend`                          | `mooncake`    | KVS metadata service backend                              |
| `--kvs-hash-algo`                        | `sha256_cbor` | Hash algorithm for prompt chunking                        |
| `--kvs-chunk-size`                       | `256`         | Token chunk size                                          |
| `--kvs-enable-save-unfull-chunk`         | `false`       | Whether to hash the last incomplete chunk                 |
| `--kvs-retry-times`                      | `5`           | Retry count for metadata service queries                  |
| `--kvs-retry-interval-ms`                | `100`         | Interval between retries                                  |
| `--kvs-metadata-service-down-duration-s` | `30`          | Duration to treat metadata service as down after failures |

Environment variables:

| Variable       | Default | Description                                                                                         |
|----------------|---------|-----------------------------------------------------------------------------------------------------|
| `GO_HASH_SEED` | `"0"`   | Hash seed for algorithms that require one (e.g. `sha256_cbor`). Must match the seed used by the KVS |

### Deployment Example

For a complete Kubernetes deployment example with PD disaggregation, Mooncake as KVS, and cache-aware scheduling
enabled, see `deploy/pd-kvs/full-mode-scheduling/load-balance/`.

---

## Predictor-Enhanced Scheduling

### Configuration

#### Scheduler Flags

| Flag                                     | Default | Description                                                                                  |
|------------------------------------------|---------|----------------------------------------------------------------------------------------------|
| `--enable-predictor-enhanced-scheduling` | `false` | Enable predictor-enhanced scheduling                                                         |
| `--max-num-batched-tokens`               | `65536` | Maximum tokens per prefill batch; must match the inference engine's `max_num_batched_tokens` |
| `--num-predictor-warmup-samples`         | `20`    | Minimum profiling samples before fitting the predictor model                                 |

#### Engine Environment Variables

| Variable                   | Default | Description                                                       |
|----------------------------|---------|-------------------------------------------------------------------|
| `LLUMNIX_ENABLE_PROFILING` | `0`     | Set to `1` to enable profiling data collection on the engine side |
| `LLUMNIX_PROFILING_STEPS`  | `50`    | Number of profiling samples to collect per instance               |

### Constraints

- **Full-mode only**: Requires `--enable-full-mode-scheduling=true` and CMS. The scheduler forcibly disables this
  feature when full-mode is not enabled.
- **`max-num-batched-tokens` alignment**: The scheduler-side `--max-num-batched-tokens` must match the inference
  engine's `max_num_batched_tokens` configuration. A mismatch causes inaccurate step simulation and degrades prediction
  quality.

---

## SLO-Aware Scheduling

### Configuration

| Flag                            | Setting                                | Description                       |
|---------------------------------|----------------------------------------|-----------------------------------|
| `--enable-full-mode-scheduling` | `true`                                 | Must be `true` for SLO policy     |
| `--scheduling-policy`           | `slo`                                  | Set to `slo` to enable SLO policy |
| `--ttft-profiling-data-path`    | Path to TTFT profiling JSON file       | (required)                        |
| `--tpot-profiling-data-path`    | Path to TPOT profiling JSON file       | (required)                        |
| `--ttft-slo`                    | Target TTFT SLO in milliseconds        | `6000.0`                          |
| `--tpot-slo`                    | Target TPOT SLO in milliseconds        | `50.0`                            |
| `--ttft-slo-dispatch-threshold` | Multiplier for TTFT dispatch threshold | `1.0`                             |
| `--tpot-slo-dispatch-threshold` | Multiplier for TPOT dispatch threshold | `1.0`                             |

The effective dispatch threshold is computed as `SLO * DispatchThreshold`. Instances with predicted latency exceeding
this threshold are filtered out. If no instances meet the threshold, a scheduling error (ErrorNoAvailableEndpoint) is
returned.

### Best Practices

1. **Accurate profiling data**: Collect profiling data on the same hardware configuration and engine launch parameters
   as production. Inaccurate profiling data leads to poor latency predictions.

2. **Conservative thresholds**: Start with `--ttft-slo-dispatch-threshold` and `--tpot-slo-dispatch-threshold` values
   slightly below 1.0 to allow some margin for prediction errors.

3. **Monitor actual latencies**: Compare predicted latencies with actual observed latencies and adjust profiling data if
   systematic bias is detected.

---

## Adaptive PD Scheduling

### Configuration

| Flag                                                | Setting                                          | Description                                                                    |
|-----------------------------------------------------|--------------------------------------------------|--------------------------------------------------------------------------------|
| `--enable-full-mode-scheduling`                     | `true`                                           | Requires full-mode scheduling.                                                 |
| `--scheduling-policy`                               | `slo`                                            | Must be set to `slo` for adaptive PD.                                          |
| `--enable-adaptive-pd`                              | `true`                                           | Enable adaptive PD scheduling.                                                 |
| `--tpot-slo`                                        | `50`                                             | TPOT SLO target (ms).                                                          |
| `--tpot-slo-dispatch-threshold`                     | `0.85`                                           | Fraction of TPOT SLO used as the dispatch filter threshold.                    |
| `--colocated-reschedule-mode`                       | `true`                                           | Enable colocated reschedule mode. (standalone rescheduling is also supported). |
| `--reschedule-interval-ms`                          | `100`                                            | Reschedule interval.                                                           |
| `--reschedule-policies`                             | `binpacking_mitigation,binpacking_consolidation` | Reschedule policies.                                                           |
| `--tpot-migrate-out-ceil-threshold`                 | `0.95`                                           | Fraction of TPOT SLO above which overload rescheduling triggers.               |
| `--tpot-migrate-out-floor-threshold`                | `0.60`                                           | Fraction of TPOT SLO below which underload rescheduling triggers.              |
| `--enable-instance-status-local-account` (Optional) | `true`                                           | Enable instance status local account.                                          |

---

## Rescheduler

### Configuration

#### Core Rescheduling Flags

| Flag                             | Default                                                           | Description                                   |
|----------------------------------|-------------------------------------------------------------------|-----------------------------------------------|
| `--enable-rescheduling`          | `false`                                                           | Enable rescheduling                           |
| `--rescheduling-policies`        | `"decode_load,prefill_failover,decode_failover,neutral_failover"` | Comma-separated list of rescheduling policies |
| `--rescheduling-interval-ms`     | `500`                                                             | Interval between rescheduling iterations      |
| `--colocated-rescheduling-mode`  | `false`                                                           | Run rescheduler inside scheduler process      |
| `--standalone-rescheduling-mode` | `false`                                                           | Run rescheduler as separate process           |

#### Load Balance Configuration

| Flag                                    | Default                            | Description                                                                                                                                  |
|-----------------------------------------|------------------------------------|----------------------------------------------------------------------------------------------------------------------------------------------|
| `--rescheduling-decode-load-metric`     | `"kv_cache_usage_ratio_projected"` | Load metric for decode instances                                                                                                             |
| `--rescheduling-decode-load-threshold`  | `1.0`                              | Threshold for source/destination filtering: instances >= this value are migration sources, instances < this value are migration destinations |
| `--rescheduling-neutral-load-metric`    | `"kv_cache_usage_ratio_projected"` | Load metric for neutral instances                                                                                                            |
| `--rescheduling-neutral-load-threshold` | `1.0`                              | Threshold for source/destination filtering: instances >= this value are migration sources, instances < this value are migration destinations |
| `--rescheduling-load-balance-threshold` | `0.0`                              | Minimum load difference required to trigger migration                                                                                        |
| `--rescheduling-load-balance-scope`     | `"cluster"`                        | Balancing scope: `cluster` or `unit`                                                                                                         |

#### Adaptive PD Configuration

| Flag                                 | Default                                            | Description                                                                          |
|--------------------------------------|----------------------------------------------------|--------------------------------------------------------------------------------------|
| `--enable-adaptive-pd`               | `false`                                            | Enable adaptive PD scheduling                                                        |
| `--scheduling-policy`                | `"load-balance"`                                   | Must be set to `slo` for adaptive PD                                                 |
| `--tpot-slo`                         | `50`                                               | TPOT SLO target (ms)                                                                 |
| `--tpot-slo-dispatch-threshold`      | `0.85`                                             | Fraction of TPOT SLO used as dispatch/destination filter threshold                   |
| `--tpot-migrate-out-ceil-threshold`  | `0.95`                                             | Fraction of TPOT SLO above which mitigating rescheduling triggers (source filter)    |
| `--tpot-migrate-out-floor-threshold` | `0.60`                                             | Fraction of TPOT SLO below which consolidating rescheduling triggers (source filter) |
| `--rescheduling-policies`            | `"binpacking_mitigation,binpacking_consolidation"` | Rescheduling policies for adaptive PD                                                |
| `--rescheduling-interval-ms`         | `500`                                              | Interval between rescheduling iterations (use `100` for adaptive PD)                 |

> **Note**: For details, see [Adaptive PD Scheduling](../design/scheduler/adaptive_pd_scheduling.md).

#### Failover Configuration

| Flag                           | Default      | Description                                                      |
|--------------------------------|--------------|------------------------------------------------------------------|
| `--failover-domain`            | `"instance"` | Failure domain: `instance`, `node`, `instance-unit`, `node-unit` |
| `--instance-staleness-seconds` | `60`         | Time after which an instance is considered stale                 |

#### Migration Request Configuration

| Flag                              | Default   | Description                                                                 |
|-----------------------------------|-----------|-----------------------------------------------------------------------------|
| `--rescheduling-req-select-rule`  | `"TOKEN"` | Migration request selection rule: `NUM_REQ`, `TOKEN`, `RATIO`               |
| `--rescheduling-req-select-order` | `"SR"`    | Migration request selection order: `LCR`, `FCR`, `LR`, `SR`, `FCW`, `FCWSR` |
| `--rescheduling-req-select-value` | `1024`    | Number of requests/tokens or KV cache ratio to migrate                      |

#### gRPC Configuration

| Flag                                  | Default | Description                               |
|---------------------------------------|---------|-------------------------------------------|
| `--llumlet-grpc-connection-pool-size` | `10`    | Size of gRPC connection pool per instance |
| `--llumlet-grpc-timeout-seconds`      | `5`     | Timeout for gRPC migration calls          |

#### Deployment Modes

| Mode       | Flag                                  | Process                  |
|------------|---------------------------------------|--------------------------|
| Colocated  | `--colocated-rescheduling-mode=true`  | Inside scheduler process |
| Standalone | `--standalone-rescheduling-mode=true` | Separate process         |
