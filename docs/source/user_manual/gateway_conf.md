# Gateway Configuration Guide

## Overview

For detailed design information about the Gateway, please refer to
the [Gateway Architecture Design Document](../design/gateway/gateway_architecture.md).

---

## PDD Forwarding Protocol

### Usage

#### Configuration

Key configuration flags (`cmd/config/config.go`):

| Flag                       | Default | Description                                                       |
|----------------------------|---------|-------------------------------------------------------------------|
| `--pd-disagg-protocol`     | `""`    | PDD protocol type: vllm-kvt/vllm-mooncake                         |
| `--separate-pd-scheduling` | `false` | Enable staged scheduling mode, batched scheduling mode when false |

#### Deployment example

For a complete Kubernetes deployment example with vllm-mooncake PDD protocol, see
`deploy/pd/full-mode-scheduling/load-balance`. For vllm-kvt PDD protocol, see
`deploy/pd-kvs/full-mode-scheduling/load-balance`
---

## Traffic Splitting

### Configuration

#### Route configuration flags

| Flag                             | Default         | Description                                                                     |
|----------------------------------|-----------------|---------------------------------------------------------------------------------|
| `--route-policy`                 | `""` (disabled) | Routing policy: `weight` or `prefix`                                            |
| `--route-config`                 | `""`            | JSON array of route endpoint configurations                                     |
| `--retry-max-count`              | `0`             | Max retries for internal routing on retryable errors before triggering fallback |
| `--fallback-retry-queue-enabled` | `false`         | Enable retry queue for 429 responses from fallback endpoints                    |
| `--fallback-retry-queue-size`    | `100`           | Max queued 429-retry tasks                                                      |
| `--fallback-retry-worker-size`   | `10`            | Concurrent goroutines processing 429-retry tasks                                |
| `--fallback-retry-max-count`     | `3`             | Max 429 retries per request                                                     |
| `--fallback-retry-init-delay-ms` | `500`           | Initial backoff delay (ms) for 429 retries                                      |
| `--fallback-retry-max-delay-ms`  | `5000`          | Max backoff delay (ms) for 429 retries                                          |

#### Route config JSON format

The `--route-config` flag accepts a JSON array. Each element describes one endpoint:

| Field      | Type   | Description                                                                                                                         |
|------------|--------|-------------------------------------------------------------------------------------------------------------------------------------|
| `base_url` | string | Endpoint URL. Set to `"local"` for internal Llumnix-managed instances                                                               |
| `api_key`  | string | API key for authentication. The gateway sets the `Authorization: Bearer <api_key>` header on proxied requests to external endpoints |
| `model`    | string | Reserved. Carried in the route config but not used to modify the proxied request; the original request model is forwarded as-is     |
| `fallback` | bool   | Whether this endpoint participates in the fallback chain                                                                            |
| `weight`   | int    | Weight for weight-based routing                                                                                                     |
| `prefix`   | string | Model name prefix pattern for prefix-based routing (e.g. `"Qwen/Qwen3-*"`, `"*"`)                                                   |

**Weight-based example**:

```json
[
  {
    "base_url": "local",
    "weight": 50
  },
  {
    "base_url": "http://vllm-external:8000",
    "weight": 50,
    "fallback": true
  }
]
```

**Prefix-based example**:

```json
[
  {
    "prefix": "Qwen/Qwen3-*",
    "base_url": "local"
  },
  {
    "prefix": "Qwen/Qwen2.5-*",
    "base_url": "http://vllm-external:8000",
    "fallback": true
  }
]
```

### Deployment example

For complete Kubernetes deployment examples with service routing, see:

- **Prefix-based routing**: `deploy/service-router/prefix/`
- **Weight-based routing**: `deploy/service-router/weight/`

Both examples deploy an internal vLLM instance (Llumnix-managed with service discovery) and an external standalone vLLM
instance, with the gateway configured to route between them and fall back to the external service on internal failure.
