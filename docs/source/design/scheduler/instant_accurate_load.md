# Instant and Accurate Load

## Goal and definition

Modern LLM workloads, especially agentic workflows, generate highly heterogeneous request lengths and heavy KV cache reuse, so both step duration and per-step load fluctuate significantly. In this regime, temporal gaps between observed (by the central scheduler) and actual load (on the inference engine), and accuracy gaps between approximate scheduling metrics and true engine compute, can significantly degrade scheduling quality.

**Goal**: provide an **i**nstant and **a**ccurate **l**oad basis (**Ial**) for LLM scheduling, covering both (i) how instance load status is observed and (ii) how prefill and decode compute load are modeled for decisions.

We say the scheduler has **Ial** when its view of load is both:
- **Instant**: reflects load status updates with minimal delay along the observation path, so each scheduling decision uses the freshest available information instead of stale snapshots.
- **Accurate**: instance load status matches the engine’s actual state, and the scheduling metrics used to represent compute load are consistent with the true prefill and decode compute required.

---

## Existing paradigms

Modern LLM schedulers typically adopt one of two paradigms for maintaining load:

1. **Engine-side per-step status exporting**  
   The inference engine exports status at each computation step (often via Prometheus or an equivalent monitoring system), and the scheduler periodically pulls this status to approximate instance load.

2. **Scheduler-side bookkeeping**  
   The scheduler maintains per-instance counters directly, increasing request/token counts when dispatching a request to an instance and decreasing them when the request completes.

Llumnix supports both paradigms via its **CMS (Cluster Metadata Store)** path and its **LRS (local real-time state)** path, as described in the *Scheduling modes: full-mode and lite-mode* sections of [Scheduling Policy Framework](policy_framework.md).

---

## Limitations of existing paradigms

### Engine-side exporting path

For the engine-side exporting path, existing designs typically suffer from three structural temporal gaps between observed and actual load:

1. **Step-loop-bound status exporting**: status is exported once per step loop, but load changes at several independent events within a step loop. Intermediate load mutations remain invisible until the next step loop boundary.
2. **Dispatch not treated as the origin of load change**: instance load is only updated when the engine exports it. Dispatch, which is the earliest point when the scheduler knows the load will change, is not considered a load-change event, so instance load does not change immediately at dispatch time and consecutive dispatches keep seeing the same load snapshot, causing multiple requests to be dispatched to the same instance.
3. **Discrete load status vs continuous queries**: engine status is updated at discrete events (at step granularity), while scheduling queries can arrive at arbitrary times. Between updates, the scheduler holds a frozen status that cannot reflect ongoing computation progress.

These three gaps together create load staleness and biased scheduling decisions, even when the engine exports status regularly.

### Scheduler-side bookkeeping path

For the scheduler-side bookkeeping path, existing designs are constrained by one inherent limitation and one implementation defect:

1. **Inherent limitation**: scheduler-side bookkeeping cannot directly observe engine-internal state, so it can only accumulate request/token counts without accounting for prefix cache sharing, making the resulting load view inherently inaccurate even when the request/token counts themselves are maintained correctly.
2. **Implementation defect**: some community implementations update request/token counts only when a request finishes rather than streaming updates from responses as tokens are generated, so the temporal freshness of instance load is bounded by request completion time instead of the token generation interval, which is sub-optimal.

---

## Llumnix Ial solutions

Ial has two dimensions: how load status is observed and how compute load is modeled for scheduling decisions.

### Metrics for prefill and decode

Beyond how load status is observed, Ial also depends on how instance load is *modeled* by scheduling metrics.

1. **Prefill compute load**  
   For prefill, the true compute load is
   ```{math}
   C_{\text{prefill}} \propto N_{\text{uncomputed prefill tokens}},
   ```
   where $N_{\text{uncomputed prefill tokens}}$ is the number of prompt tokens that have not yet been computed on the instance. Request count and raw prompt token count are only coarse proxies: they ignore three critical facts: (a) cached prefix tokens contribute **zero** prefill compute load on the instance that holds them, (b) in chunked prefill, only the not-yet-computed suffix of each prompt generates new prefill work at the current step, and (c) requests in the decode phase and their tokens must not be counted toward prefill compute load. As a result, these coarse metrics systematically mis-estimate prefill compute load, especially when prefix caching or chunked-prefill are enabled.

2. **Decode compute load**  
   For decode in MoE-style architectures, the true compute load depends on two independent factors:
   ```{math}
   C_{\text{decode}} \propto N_{\text{batch}} + \sum_{r \in \text{running batch}} L_r,
   ```
   where $N_{\text{batch}}$ is the number of requests in the active decode batch (which determines expert-routing cost), and $\sum L_r$ is the sum of sequence lengths across those requests (which determines attention cost). This also differs fundamentally from KV cache usage: different decode requests in the running batch may share a common prefix in the KV cache, so storage usage does not reflect running batch compute load.

A Ial-oriented scheduler must therefore design metrics that accurately model their actual compute load  (which is what Llumnix implements), rather than relying solely on coarse-grained metrics such as request count, raw prompt tokens, or KV cache usage.

---

### CMS (engine-side) path

```{mermaid}
graph TB
    Request([Request]) --> Gateway

    subgraph GW_Sched[" "]
        direction LR
        subgraph Scheduler
            LocalCMS[CMS View]:::dashed
        end
        Gateway <-->|/schedule| Scheduler
    end

    CMS[(CMS)]

    subgraph InferenceService["Inference Service"]
        direction LR
        subgraph Instance0[Instance 0]
            direction TB
            Engine0[Engine] --> Llumlet0[Llumlet]
        end
        subgraph Instance1[Instance 1]
            direction TB
            Engine1[Engine] --> Llumlet1[Llumlet]
        end
        subgraph InstanceN[Instance N]
            direction TB
            EngineN[Engine] --> LlumletN[Llumlet]
        end
    end

    Gateway -->|dispatch| InferenceService
    Llumlet0 -->|status| CMS
    Llumlet1 -->|status| CMS
    LlumletN -->|status| CMS
    LocalCMS -->|pull| CMS
    classDef dashed stroke-dasharray: 5 5
```

Llumnix’s full-mode uses a CMS-backed status path (engine → llumlet → CMS → scheduler). On this path, Ial is achieved by explicitly closing the three temporal gaps above:

1. **Load-change-event-triggered status exporting (fixes step-loop-bound status exporting)**  
    Status updates are triggered by the events that actually change load, rather than by the step loop boundary. Each load-change event generates a status update, eliminating the time window between a load mutation and its visibility to the scheduler.
   - **Usage**: This feature is enabled by default in full-mode

2. **Dispatch-time account with reconciliation (fixes dispatch blindness)**  
   The scheduler maintains a per-instance **instance status local account** that records a load increment at each dispatch. Requests that have been dispatched but not yet reflected in engine-exported status are called **in-flight requests**; the local account accumulates load increments from all in-flight requests on that instance. When the engine exports a status that includes an in-flight request, the scheduler reconciles the local account via a combine operation, subtracting the confirmed request's load increment to avoid double-counting. The local account is a temporary compensating ledger: it holds only net load increments from in-flight requests and shrinks back to zero as engine-side confirmations arrive.
   - **Usage**: This feature is enabled by default in full-mode. To enable accurate prompt token counting for load calculation, the gateway must be configured with either `--tokenizer-path` (for local tokenizer files) or `--tokenizer-name` (for built-in tokenizers) to properly tokenize incoming requests.

3. **Status forward-prediction (fixes discrete vs continuous mismatch)**  
   The scheduler uses a predictor that takes the last exported status and the elapsed time since its timestamp as input. It forward-predicts this snapshot to the current query time, estimating how much work has been completed and how much load remains. This converts discrete, step-bound status into a load estimate that is valid at arbitrary scheduling times.
   - **Usage**: See [Predictor-Enhanced Scheduling](predictor_enhanced_scheduling.md) for design details and configuration.

Together, these mechanisms make the CMS path provide the most instant load view achievable under the engine-side exporting paradigm, eliminating the structural temporal gaps that exist in existing designs.

---

### LRS (scheduler-side) path

```{mermaid}
graph TB
    Request([Request]) --> GW

    subgraph GW_Sched2[" "]
        direction LR
        subgraph GW[Gateway]
            RST[RequestStateTracker]
        end
        subgraph Scheduler2[Scheduler]
            LRS[LRS View]:::dashed
        end
        GW <-->|/schedule| Scheduler2
    end

    subgraph InferenceService["Inference Service"]
        Engine0[Engine 0] ~~~ Engine1[Engine 1] ~~~ EngineN[Engine N]
    end

    GW <-->|dispatch / SSE stream| InferenceService
    RST -->|/report| Scheduler2
    GW -->|/release| Scheduler2
    classDef dashed stroke-dasharray: 5 5
```

On the LRS path, Llumnix implements scheduler-side bookkeeping with a focus on temporal freshness:

1. **Dispatch-driven increments**: per-instance load counters increase when the scheduler dispatches a request to an instance.
2. **Streaming-driven updates**: as the engine streams responses back, `RequestStateTracker` in the gateway continuously extracts and updates token counts based on each SSE response chunk rather than waiting for request completion, and reports the states to the scheduler periodically via `/report`. This realizes the *theoretical best case* for scheduler-side bookkeeping in terms of temporal freshness.
3. **Completion-driven release**: when a request finishes, the gateway calls the scheduler's `/release` endpoint to remove the request's state from the scheduler's LRS, ensuring that completed requests no longer contribute to instance load.

The remaining limitation on the LRS path is informational rather than temporal: scheduler-side bookkeeping cannot directly observe accurate engine-internal state and therefore can only accumulate request/token counts without accounting for prefix cache sharing, so even with dispatch- and streaming-driven updates, the resulting load view remains a rough approximation rather than a fully accurate reflection of actual engine load.

---

### Summary of Llumnix Ial solution

- **CMS solution**: event-triggered exporting, dispatch-time accounting, and status forward-prediction close the structural gaps of existing engine-side status export, yielding a most instant and accurate load basis for scheduling within this paradigm.
- **LRS solution**: dispatch- and streaming-driven request/token count updates provide temporally optimal bookkeeping given the information available to the scheduler, with the only remaining inaccuracies arising from engine-internal states that are fundamentally opaque to this path.

In both solutions, Llumnix explicitly aligns scheduling decisions with a instant and accurate load basis (Ial), reducing both temporal gaps between observed and actual load and accuracy gaps between scheduling metrics and true engine compute, and thereby improving scheduling quality in modern LLM workloads.
