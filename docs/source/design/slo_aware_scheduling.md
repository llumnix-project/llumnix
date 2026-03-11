# SLO-aware Scheduling

The SLO (Service Level Objective) aware Scheduling is a latency-aware scheduling policy that routes requests to instances predicted to deliver the lowest latency, ensuring TTFT (Time-To-First-Token) and TPOT (Time-Per-Output-Token) SLO compliance.

---

## Overview

The SLO policy leverages latency prediction to make informed scheduling decisions. Unlike the load-balance policy that minimizes load, the SLO policy minimizes predicted latency, making it suitable for latency-sensitive workloads with strict SLO requirements.

**Key characteristics**:

- **Full-mode only**: Requires `--enable-full-mode-scheduling=true` and CMS for accurate instance state.
- **Profiling-based prediction**: Uses pre-collected profiling data to predict TTFT and TPOT.
- **SLO-aware filtering**: Filters out instances predicted to exceed SLO thresholds.
- **Adaptive PD integration** (optional): Supports adaptive prefill-decode disaggregation when enabled. please refer to [Adaptive PD](./adaptive_pd_scheduling.md) for more details.

---

## Generating Profiling Data

Profiling data should be collected from benchmark runs on your target hardware:

1. Run benchmarks with various batch sizes and token lengths.
2. Collect TTFT and TPOT distributions.
3. Compute p50 values for each configuration point.
4. Format as JSON according to the profiling data schema.

### Profiling Data Format

The profiling data should be formatted as JSON and the schema is defined as follows. Now, only p50 values are used.

**TTFT Profiling Data** (`--ttft-profiling-data-path`):

```json
{
  "metadata": {
    "model": "model-name",
    "timestamp": "2026-01-01T00:00:00Z",
    "description": "TTFT profiling results"
  },
  "results": [
    {
      "tokens_num": 128,
      "mean": 45.2,
      "p50": 42.0,
      "p95": 58.1,
      "p99": 62.3
    }
  ]
}
```

**TPOT Profiling Data** (`--tpot-profiling-data-path`):

```json
{
  "metadata": {
    "model": "model-name",
    "timestamp": "2026-01-01T00:00:00Z"
  },
  "results": [
    {
      "batch_size": 16,
      "tokens_per_request": 8,
      "mean": 12.5,
      "p50": 11.8,
      "p95": 15.2
    }
  ]
}
```

---

## Latency Prediction

### LatencyPredictor

The `LatencyPredictor` (defined in predict_utils.go) uses interpolation-based prediction from profiling data:

- **TTFT Prediction**: Based on prefill tokens, decode batch size and decode tokens. Uses chunked prefill modeling when applicable.
- **TPOT Prediction**: Based on decode batch size and decode tokens.

### Prediction Algorithm

The `InterpolationPredictor` (defined in interpolation_predictor.go) performs bilinear interpolation:

1. Finds the bounding box of profiling points around the target parameters.
2. Computes weighted interpolation between the four corner points.
3. Returns the predicted latency value.

---

## Scheduling Pipeline

### Prefill Stage

**Metrics**:
- `predicted_ttft`: Predicted time-to-first-token latency.

**Filters**:
1. `failoverFilter` (global): Blocks instances in failure domains with unhealthy instances.
2. `schedulabilityFilter` (single-instance): Blocks unschedulable instances.
3. `stalenessFilter` (single-instance): Blocks instances with stale status data.
4. `metricBasedFilter` (single-instance): Blocks instances where `predicted_ttft > TtftSlo * TtftSloDispatchThreshold`.

**Selector**:
- `metricBasedSelector`: Selects the instance with the lowest `predicted_ttft`.

### Decode Stage

**Metrics**:
- `predicted_tpot`: Predicted time-per-output-token latency.

**Filters**:
1. `failoverFilter` (global): Blocks instances in failure domains.
2. `schedulabilityFilter` (single-instance): Blocks unschedulable instances.
3. `stalenessFilter` (single-instance): Blocks instances with stale status data.
4. `metricBasedFilter` (single-instance): Blocks instances where `predicted_tpot > TpotSlo * TpotSloDispatchThreshold`.

**Selector**:
- `metricBasedSelector`: Selects the instance with the lowest `predicted_tpot`.

---

## Policy Configuration

| Flag | Setting | Description |
|------|-------------|---------|
| `--enable-full-mode-scheduling` | `true` |  Must be `true` for SLO policy  |
| `--scheduling-policy` | `slo` | Set to `slo` to enable SLO policy |
| `--ttft-profiling-data-path` | Path to TTFT profiling JSON file | (required) |
| `--tpot-profiling-data-path` | Path to TPOT profiling JSON file | (required) |
| `--ttft-slo` | Target TTFT SLO in milliseconds | `6000.0` |
| `--tpot-slo` | Target TPOT SLO in milliseconds | `50.0` |
| `--ttft-slo-dispatch-threshold` | Multiplier for TTFT dispatch threshold | `1.0` |
| `--tpot-slo-dispatch-threshold` | Multiplier for TPOT dispatch threshold | `1.0` |

The effective dispatch threshold is computed as `SLO * DispatchThreshold`. Instances with predicted latency exceeding this threshold are filtered out. If no instances meet the threshold, a scheduling error(ErrorNoAvailableEndpoint) is returned.

---

## Best Practices

1. **Accurate profiling data**: Collect profiling data on the same hardware configuration and engine launch parameters as production. Inaccurate profiling data leads to poor latency predictions.

2. **Conservative thresholds**: Start with `--ttft-slo-dispatch-threshold` and `--tpot-slo-dispatch-threshold` values slightly below 1.0 to allow some margin for prediction errors.

3. **Monitor actual latencies**: Compare predicted latencies with actual observed latencies and adjust profiling data if systematic bias is detected.
