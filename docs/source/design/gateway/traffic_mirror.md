# Traffic Mirror

## Introduction

Traffic mirroring copies a configurable fraction of incoming inference requests to a secondary target asynchronously without affecting the primary request path or client response. This enables production-safe validation of new model versions, shadow testing of alternative serving stacks, and traffic capture for offline analysis.

The mirror target receives an identical HTTP request to the one sent to the primary backend, including headers and body. The gateway does not wait for the mirror response and discards any mirror result. Mirror failures do not impact client responses.

## Design and implementation

```{mermaid}
graph TB
    Client([Client])

    subgraph Gateway
        Service["LlmGatewayService"]
        Mirror["Mirror"]
    end

    Client -->|Request| Service
    Service -->|Dispatch| Primary["Primary Backend"]
    Primary -->|Response| Service
    Service -->|Response| Client
    Service -.->|Async Copy| Mirror
    Mirror -.->|Fire-and-Forget| MirrorTarget["Mirror Target"]
```

### Mirror module

`Mirror` (`pkg/gateway/mirror/mirror.go`) asynchronously replicates requests based on a sampled ratio. For each request:

1. Reads current mirror configuration from `ConfigProvider` (loaded from `/mnt/mirror.json` with hot-reload).
2. Skips if mirroring is disabled, target URL is empty, or ratio is non-positive.
3. Samples against the configured ratio (percentage 0-100). Requests that fail the sample are not mirrored.
4. Clones the HTTP request (method, URL path, headers, body) and constructs the mirror URL by concatenating the configured target base URL with the original request path.
5. Optionally overrides the `Authorization` header if `Authorization` is configured.
6. Sends the request with a configurable timeout (or inherits the request context if timeout is zero).
7. Discards the response body and logs the result if logging is enabled.

All mirror operations execute in a separate goroutine to avoid blocking the primary request path. Panic recovery protects the gateway from crashes due to mirror errors.

### Integration with request lifecycle

The mirror trigger is invoked after request parsing but before the request enters the buffer queue:

```go
// After request parsing and logging
if lgs.mirror.Enabled() {
    lgs.mirror.TryMirror(reqCtx)
}

// Request then enters buffer queue, followed by preprocessing, scheduling, and forwarding
```

This placement ensures:
- Mirror requests start immediately and execute concurrently with primary processing (including preprocessing, scheduling, and forwarding)
- Mirror failures do not delay or disrupt the client response
- The same raw request body sent to the primary is also sent to the mirror (no serialization differences)

### Use cases

**Shadow testing**: Deploy a new model version or serving configuration on the mirror target and compare responses against the production backend without exposing users to potential regressions.

**Traffic capture**: Mirror 100% of traffic to a logging or analysis service for offline dataset construction, prompt auditing, or usage analytics.

**Load validation**: Validate that a candidate backend can handle production load levels by mirroring real traffic patterns.
