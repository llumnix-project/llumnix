# Policy framework overview

llumnix scheduling policies follow a **metrics → filters → selectors** pipeline.

- **Per-infer-mode composition**: each policy defines separate metrics, filters, and selector for `prefill`, `decode`, and `normal` infer modes.
- **Execution flow**: the dispatcher (`DispatchPolicy.Schedule`) builds a cluster view — a snapshot of all instance states and metadata grouped by infer mode, sourced from CMS (Cluster Metadata Store, enabled in full-mode) or LRS (Local Realtime State, enabled in lite-mode) — then for each infer mode:
  1. **Metric computation**: computes configured metrics for every candidate instance.
  2. **Filtering**: removes unsuitable candidate instances (with a fallback pass if necessary).
  3. **Selection**: picks the target instance based on metric comparison.
- **Policy families**: llumnix provides **load-balance** and **slo** policies, running in **full-mode** or **lite-mode**. Cache-aware scheduling, predictor-enhanced scheduling, and rescheduling are enabled via feature flags.

---

## Scheduling modes: full-mode and lite-mode

- **Full-mode** deploys a **llumlet** component alongside each inference engine and requires a **CMS** service. llumlet collects load data directly from the engine and writes it to CMS, giving the scheduler precise instance states (KV cache usage, schedulability). This requires patching vLLM, so a custom inference image (patched vLLM + llumnix) is needed.
  - Key advantage: engine-internal state is visible to the scheduler. For example, when prefix caching is enabled, shared prefix cache reduces actual load within an instance — only the engine can report this; the scheduler cannot infer it from request dispatch.
  - Other advantage: llumlet also enables **live request migration** between instances for load balancing or failover.

```{mermaid}
graph LR
    Client -->|request| Gateway
    Gateway <-->|schedule| Scheduler
    Scheduler -->|read| CMS["CMS (Cluster Metadata Store)"]
    Gateway -->|dispatch| Engine["vLLM (patched)"]
    Engine -->|instance status| llumlet
    llumlet -->|write| CMS
```

- **Lite-mode** requires no vLLM patching and no CMS; it works with official vLLM images. The gateway and scheduler track instance load (request count, token count) via request dispatch and streaming responses, maintained as local real-time state (LRS).
  - Trade-off: load data is less accurate. Features depending on engine-internal state (prefix cache sharing, exact KV cache usage) are unavailable. Inaccurate load data degrades the quality of scheduling, rescheduling, and advanced scheduling features, resulting in suboptimal resource utilization.

```{mermaid}
graph LR
    Client -->|request| Gateway
    Gateway <-->|schedule| Scheduler
    Gateway -->|dispatch| Engine["vLLM (official)"]
    Engine -->|streaming response| Gateway
    Gateway -->|update request/token counts| Scheduler
    Scheduler -->|update| LRS["LRS (local real-time state)"]
```

---

## Metrics: quantifying instance load and latency

Metrics compute numerical instance load and latency values from CMS/LRS instance data and request prompt tokens.
They implement the `instanceSchedulingMetric` interface and are constructed by `getSchedulingMetric`.

Built-in metrics (names correspond to constants in `pkg/consts/consts.go`):

- **`kv_cache_usage_ratio_projected` (`SchedulingMetricKVCacheUsageRatioProjected`)**
  - **Function**: approximate the projected KV cache usage ratio on an instance.
  - **Inputs**: used KV tokens, unallocated tokens for waiting/running prefills and decodes, and tokens of in-flight decode requests.
- **`decode_batch_size` (`SchedulingMetricDecodeBatchSize`)**
  - **Function**: measure the effective decode batch size on an instance.
  - **Inputs**: counts waiting, loading, running, and in-flight decode requests.
- **`num_waiting_requests` (`SchedulingMetricNumWaitingRequests`)**
  - **Function**: measure queue pressure on an instance.
  - **Inputs**: number of waiting requests plus in-flight dispatch requests.
- **`all_prefills_tokens_num` (`SchedulingMetricAllPrefillsTokensNum`)**
  - **Function**: estimate the total number of prefill tokens to compute.
  - **Inputs**: waiting, running, and in-flight prefill tokens, optionally adjusted by predicted completed tokens.
- **`kv_cache_hit_len` (`SchedulingMetricKVCacheHitLen`)**
  - **Function**: capture prompt cache hit length for the current request.
  - **Inputs**: number of prefix tokens that hit the KV cache; used to favor high-hit instances in cache-aware scheduling.
- **`cache_aware_all_prefills_tokens_num` (`SchedulingMetricCacheAwareAllPrefillsTokensNum`)**
  - **Function**: combine cache misses and prefill load into a single metric.
  - **Inputs**: prefix miss tokens and prefill token load.
- **`num_requests` (`SchedulingMetricNumRequests`)**
  - **Function**: measure the number of active and queued requests on an instance.
  - **Inputs**: in full-mode, derived from CMS (waiting, loading, running, in-flight prefill and decode); in lite-mode, derived from LRS `NumRequests()`.
- **`all_decodes_tokens_num` (`SchedulingMetricAllDecodesTokensNum`)**
  - **Function**: measure total decode token load on an instance.
  - **Inputs**: waiting, running, loading, and in-flight decode tokens.
- **`num_tokens` (`SchedulingMetricNumTokens`)**
  - **Function**: provide a simplified load indicator in lite-mode scheduling.
  - **Inputs**: total token count from LRS.
- **`predicted_ttft` (`SchedulingMetricPredictedTtft`)**
  - **Function**: predict time-to-first-token latency.
  - **Inputs**: profiling data, current prefill and decode loads, and instance capacity.
- **`predicted_tpot` (`SchedulingMetricPredictedTpot`)**
  - **Function**: predict per-token decode latency (TPOT).
  - **Inputs**: decode batch size, decode token load, and profiling data.

All metrics expose a uniform comparison interface (`Less`, `ValueLess`, `GetValue`) for consistent cross-instance comparison.

---

## Filters: filtering candidate instances

Filters remove unsuitable candidate instances from scheduling.
They are divided into **single-instance filters** and **global filters**.
Filters either block unhealthy instances, or narrow candidate instances according to scheduling requirements.

- **Single-instance filters** examine each instance independently and block those that fail checks.
- **Global filters** operate on all candidate instances to enforce failover across nodes or units.

Key filters:

- **`schedulabilityFilter` (single-instance)**
  - **Function**: block unschedulable instances.
  - **Implementation**: checks CMS `Status.Schedulable` and marks `needsFailover` for unschedulable instances.
- **`stalenessFilter` (single-instance)**
  - **Function**: block instances with stale status data.
  - **Implementation**: compares current time with `Status.TimestampMs`; marks `needsFailover` and blocks instances exceeding `InstanceStalenessSeconds`.
- **`metricBasedFilter` (single-instance)**
  - **Function**: block instances whose load metric exceeds a configured threshold.
  - **Implementation**: reads a metric and compares it with a threshold; optionally skipped in fallback passes via `notSkipWhenFallback`.
- **`inferModeFilter` (single-instance)**
  - **Function**: block instances whose infer mode does not match the current scheduling stage.
  - **Implementation**: compares instance infer mode against the target infer mode.
- **`failoverFilter` (global)**
  - **Function**: block all instances within the failure domain of unhealthy instances.
  - **Implementation**: reads `needsFailover` marks from single-instance filters and blocks instances according to `FailoverScope` (instance / node / instance-unit / node-unit).
- **`failoverMigrationSrcFilter` (global)**
  - **Function**: select valid migration source instances for rescheduling.
  - **Implementation**: runs schedulability and staleness checks to tag failover instances, then keeps the remaining instances as migration sources.

All single-instance filters run first, then all global filters.

---

## Selectors: choosing the final instance

Selectors pick the target instance from candidate instances that passed all filters.

- **`metricBasedSelector`**
  - **Function**: select an instance by comparing configured metrics.
  - **Implementation**: configured with a list of metric names and a `topK` parameter; when `topK == 1`, picks the top instance by metric rank; when `topK > 1`, sorts by metrics, keeps the top `topK`, and randomly picks one.

---

## Built-in policies and modes

### Load-balance policy

The load-balance policy routes requests to the instance with the lowest load.

- **Full-mode (`EnableFullModeScheduling = true`)**
  - Uses CMS-based cluster view.
  - **Filters**:
    - Global: `failoverFilter` (respecting `FailoverScope`).
    - Single-instance: `schedulabilityFilter`, `stalenessFilter`, `metricBasedFilter` on configured load metrics.
  - **Metrics** (typical defaults):
    - `prefill`: `DispatchPrefillLoadMetric`, by default `all_prefills_tokens_num`.
    - `decode`: `DispatchDecodeLoadMetric`, by default `kv_cache_usage_ratio_projected`.
    - `normal`: `DispatchNeutralLoadMetric`, by default `all_prefills_tokens_num`.
  - **Selector**: `metricBasedSelector(topK = DispatchTopK)`, ranking by the load metric.

- **Lite-mode (`EnableFullModeScheduling = false`)**
  - Uses LRS instead of CMS; does not support failover.
  - **Filters**: only `metricBasedFilter`; no schedulability, staleness, or failover checks.
  - **Metrics**:
    - `DispatchLoadMetric` values are restricted to `{num_requests, num_tokens}` by `verifyDispatchLoadMetric`.
  - **Selector**: `metricBasedSelector(topK = DispatchTopK)`, ranking by the load metric.

### SLO policy (`SchedulePolicySlo`)

The SLO policy routes requests to the instance with the lowest predicted latency.

- **Goal**:
  - Prefill: meet TTFT SLO.
  - Decode: meet TPOT SLO.
- **Metrics**:
  - Prefill: `predicted_ttft` only.
  - Decode: `predicted_tpot` only.
- **Filters**:
  - Global: `failoverFilter`.
  - Single-instance: `schedulabilityFilter`, `stalenessFilter`, `metricBasedFilter`.
  - `metricBasedFilter` threshold: `TtftSlo * TtftSloDispatchThreshold` or `TpotSlo * TpotSloDispatchThreshold`.
- **Selector**:
  - `metricBasedSelector` ranking by the predicted-latency metric.


## Scheduling features

These features require **full-mode scheduling** (`--enable-full-mode-scheduling`).

- **Cache-aware scheduling (`--enable-cache-aware-scheduling`)**
  - Adds cache locality metrics (`kv_cache_hit_len` / `cache_aware_all_prefills_tokens_num`) to the load-balance policy.
  - Inserts the cache-locality metric at the front of the selector's metric list so that cache hit length is considered before load.

- **Predictor-enhanced scheduling (`--enable-predictor-enhanced-scheduling`)**
  - Uses latency predictors to estimate completion prefill tokens since last instance status update and adjusts `all_prefills_tokens_num` accordingly.

- **Rescheduling (`--enable-rescheduling`)**
  - Periodically migrates requests from overloaded instances on a background reschedule.

---

## Usage and extension guidelines

- **Choosing a policy and mode**
  - SLO-restricted workloads with profiling data: **SLO + full-mode**.
  - Utilization-focused workloads with cache locality needs: **load-balance + full-mode + cache-aware scheduling**.
  - Simple deployments without CMS: **load-balance + lite-mode**, with `num_requests` or `num_tokens` as the load metric.

- **Switching between full-mode and lite-mode**
  - Flag: `--enable-full-mode-scheduling`.
  - **Full-mode (default)**: `--enable-full-mode-scheduling=true`.
  - **Lite-mode**: `--enable-full-mode-scheduling=false`.
  - Advanced scheduling features require full-mode.

- **Extending with new policies**
  - Reuse existing metrics, filters, and selectors; rewire combinations per infer mode.
  - For new signals: implement `instanceSchedulingMetric` in `metrics.go`, register in `getSchedulingMetric`, configure in `schedule_policy_registry.go`.

---

## Future work

- **More advanced scheduling features**: with the general-purpose **metrics + filters + selectors** framework in place, llumnix will open-source more advanced features built on top of it, including **elastic EP** and **adaptive PD**.

- **YAML-based policy configuration**: users will be able to define metrics, filters, and selectors via YAML, freely composing custom scheduling policies without writing code and recompiling.
