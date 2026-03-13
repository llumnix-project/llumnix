# Predictor-Enhanced Scheduling

## Introduction

Instance load updates are constrained by the per-step nature of inference execution and periodic CMS polling. Between two consecutive updates, the scheduler sees a stale snapshot while instances continue processing prefill tokens. This staleness causes the scheduler to overestimate prefill load on busy instances, leading to suboptimal dispatch decisions.

Predictor-enhanced scheduling closes this gap by fitting an online latency model from runtime profiling data and using it to predict how many prefill tokens each instance has computed since its last status update. The scheduler subtracts the predicted completions from the reported prefill load, producing a more accurate load estimate at dispatch time.

---

## Design and Implementation

The feature operates in two concurrent online phases:

### Online fitting phase

Each instance's llumlet collects `(num_scheduled_prefill_tokens, step_duration)` pairs during inference and pushes them to CMS via `InstanceStatus` (`status_updater.py`). The gateway extracts new profiling samples and feeds them to a `QuadraticPredictor` (`pkg/scheduler/predictor/quadratic_predictor.go`). Once enough warmup samples accumulate, it fits a quadratic model mapping batched token count to step duration, with linear and constant fallbacks. Sample ingestion and model fitting are managed in `pkg/cms/cms_read_client.go`.

### Online prediction phase

At dispatch time, the scheduler estimates how many prefill tokens each instance has computed since its last status update by simulating step-by-step prefill processing using the elapsed time, the instance's reported uncomputed prefill token counts, `max-num-batched-tokens`, and the fitted model (`pkg/scheduler/policy/predict_utils.go`). The result is subtracted from the `allPrefillsTokensNum` metric:

```
allPrefillsTokensNum = allWaitingPrefillsTokens
    + schedulerRunningPrefillsTokens
    + inflightDispatchPrefillTokens
    - numComputedPrefillTokensPredicted
```

This adjusted metric is used by all scheduling policies that rely on `allPrefillsTokensNum`, including load-balance, cache-aware, and SLO-aware scheduling.

---

## Current Limitations and Future Direction

Online profiling embeds historical latency characteristics into the predictor. Any inference-service change that alters step latency invalidates the fitted model, and the stale samples cannot be purged without restarting the scheduler. A future revision plans to adopt offline profiling to eliminate this coupling. The production-ready design is under active development.
