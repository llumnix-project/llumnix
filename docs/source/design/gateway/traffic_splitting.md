# Traffic Splitting

## Introduction

In production LLM serving, backend instances may become unavailable due to crashes, overload, or rate limiting. Without a traffic management layer, such failures propagate directly to users. The service router addresses this by splitting request traffic across internal (Llumnix-managed) and external (e.g. third-party API) endpoints, and falling back to alternative endpoints when the primary route fails.

The service router supports two routing policies — **weight-based** and **prefix-based** — and a **fallback chain** that activates on scheduling failures, HTTP errors, or unmatched routes.

## Design and implementation

```{mermaid}
graph TB
    Request([Request])

    subgraph Gateway
        Router["Service Router"]
    end

    Router -->|RouteInternal| Scheduler["Scheduler"]
    Router -->|RouteExternal| ExternalEP["External Endpoints"]
    Router -->|RouteUnknown| Fallback["Fallback Chain"]

    Scheduler --> Internal["Internal Instances<br/>(Llumnix-managed)"]
    Scheduler -->|scheduling failure| Fallback

    Internal -->|HTTP error after retries| Fallback
    ExternalEP -->|HTTP error| Fallback

    Fallback --> FB1["Fallback Endpoints"]

    Request --> Router
```

### Routing policies

The `ServiceRouter` (`pkg/gateway/router/service_router.go`) evaluates each incoming request and produces one of three route types:

- **RouteInternal**: dispatch to internal Llumnix-managed instances via the scheduler.
- **RouteExternal**: proxy directly to an external endpoint (e.g. a third-party model API).
- **RouteUnknown**: no matching route found; proceed to the fallback chain.

Two mutually exclusive policies determine how the route is selected:

**Weight-based routing** (`--route-policy weight`): each configured endpoint carries an integer weight. The router draws a random number over the total weight sum and selects the corresponding endpoint. This distributes traffic proportionally — for example, weights 50/50 yield an approximately even split.

**Prefix-based routing** (`--route-policy prefix`): each endpoint declares a model name prefix pattern (e.g. `Qwen/Qwen3-*`). The router matches the request's model name against all patterns and selects the endpoint with the **longest matching prefix**. An exact match (no wildcard) takes priority over any prefix match. A catch-all pattern `*` matches any model with the shortest length, serving as a default route.

For both policies, an endpoint with `base_url` set to `"local"` is treated as the internal route.

### Fallback mechanism

When the primary route fails, the gateway attempts fallback endpoints in the order they appear in `--route-config`. Only external endpoints with `"fallback": true` participate in the fallback chain.

Fallback triggers in three scenarios:

1. **Scheduling failure**: the scheduler cannot find an available internal instance (e.g. all instances are overloaded or unhealthy).
2. **Internal HTTP error after retries**: the request was dispatched to an internal instance, encountered a retryable error (5xx, network error), exhausted all retry attempts (`--retry-max-count`), and the response headers have not yet been sent to the client.
3. **External HTTP error**: the primary external endpoint returned a 4xx/5xx error.

For each fallback attempt, the gateway proxies the request to the next fallback endpoint. If that endpoint also fails, the gateway moves to the next one in the chain until all fallback endpoints are exhausted.

When a fallback endpoint returns HTTP 429 (Too Many Requests), the gateway can optionally enqueue the request into a **rate-limit retry queue** (`--fallback-retry-queue-enabled`) that retries with exponential backoff, avoiding immediate rejection.

### Request dispatch flow

The gateway's `dispatchRequest` method (`pkg/gateway/service/gateway_service.go`) orchestrates the full lifecycle:

1. `ServiceRouter.Route()` determines the route type and target endpoint.
2. Based on the route type:
   - **RouteInternal**: acquire an instance from the scheduler, then execute the request with retry logic. On exhausted retries, trigger fallback if available.
   - **RouteExternal**: proxy the request to the external endpoint. On failure, trigger fallback if available.
   - **RouteUnknown**: proceed directly to the fallback chain.
3. The fallback chain iterates through configured fallback endpoints sequentially, with exponential backoff between attempts.
