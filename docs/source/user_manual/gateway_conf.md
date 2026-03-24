# Gateway Configuration Guide

## Overview

For detailed design information about the Gateway, please refer to
the [Gateway Architecture Design Document](../design/gateway/gateway_architecture.md).

---

## Service Discovery

For detailed design information about service discovery, please refer to
the [Service Discovery Design Document](../design/service_discovery.md).

### Configuration

The Gateway discovers backend inference instances through Redis or etcd. The discovery backend is selected via `--llm-backend-discovery` and configured with backend-specific flags.

#### General flags

| Flag                       | Default   | Description                                                  |
|----------------------------|-----------|--------------------------------------------------------------|
| `--llm-backend-discovery`  | `"redis"` | Discovery backend for LLM instances: `redis`, `etcd`, or `endpoints` |

#### Redis discovery flags

| Flag                                  | Default   | Description                                          |
|---------------------------------------|-----------|------------------------------------------------------|
| `--discovery-redis-host`              | `"redis"` | Redis host                                           |
| `--discovery-redis-port`              | `6379`    | Redis port                                           |
| `--discovery-redis-username`          | `""`      | Redis username                                       |
| `--discovery-redis-password`          | `""`      | Redis password                                       |
| `--discovery-redis-socket-timeout`    | `1.0`     | Redis socket timeout in seconds                      |
| `--discovery-redis-retry-times`       | `1`       | Redis retry times on connection failure              |
| `--discovery-redis-status-ttl-ms`     | `60000`   | TTL in milliseconds for discovery entries; entries older than this are considered expired |
| `--discovery-redis-refresh-interval-ms` | `1000`  | Polling interval in milliseconds for refreshing the instance list from Redis |

#### etcd discovery flags

| Flag                                      | Default    | Description                                                                 |
|-------------------------------------------|------------|-----------------------------------------------------------------------------|
| `--discovery-etcd-endpoints`              | `"etcd:2379"` | etcd endpoints, comma-separated (e.g. `etcd-0:2379,etcd-1:2379`)       |
| `--discovery-etcd-username`               | `""`       | etcd username                                                               |
| `--discovery-etcd-password`               | `""`       | etcd password                                                               |
| `--discovery-etcd-dial-timeout`           | `5.0`      | etcd dial timeout in seconds                                                |
| `--discovery-etcd-lease-ttl`              | `60`       | etcd lease TTL in seconds; instances whose lease expires are automatically removed |
| `--discovery-etcd-refresh-interval-sec`   | `3600`     | Periodic full-refresh interval in seconds (safety net for missed Watch events) |

#### Static endpoint discovery flags

For development or testing, instances can be specified as a static comma-separated list instead of using Redis or etcd:

| Flag                      | Default | Description                                                    |
|---------------------------|---------|----------------------------------------------------------------|
| `--llm-backend-endpoints` | `""`    | Static backend endpoints (e.g. `0.0.0.0:8090,0.0.0.0:8091`)  |

### Deployment Example

- **Redis-based discovery** (default): See `deploy/base/redis.yaml` for the Redis deployment and any standard deployment example (e.g. `deploy/neutral/lite-mode-scheduling/load-balance/`).
- **etcd-based discovery**: See `deploy/etcd-discovery/` for a 3-node etcd StatefulSet deployment with an integration test (`test_etcd_failover.sh`) that validates Gateway discovery recovery across single-node failure, quorum loss, full cluster restart with data loss, and instance pod restart.

---

## PDD Forwarding Protocol

### Configuration

Key configuration flags (`cmd/config/config.go`):

| Flag                       | Default | Description                                                       |
|----------------------------|---------|-------------------------------------------------------------------|
| `--pd-disagg-protocol`     | `""`    | PDD protocol type: vllm-kvt/vllm-mooncake                         |
| `--separate-pd-scheduling` | `false` | Enable staged scheduling mode, batched scheduling mode when false |

### Deployment Example

For a complete Kubernetes deployment example with vllm-mooncake PDD protocol, see
`deploy/pd/full-mode-scheduling/load-balance`. For vllm-kvt PDD protocol, see
`deploy/pd-kvs/full-mode-scheduling/load-balance`.

---

## Traffic Splitting

### Configuration

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

### Route Config JSON Format

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

### Deployment Example

For complete Kubernetes deployment examples with service routing, see:

- **Prefix-based routing**: `deploy/traffic-splitting/prefix/`
- **Weight-based routing**: `deploy/traffic-splitting/weight/`

Each example includes an integration test that verifies routing and fallback behavior by sending requests through the gateway and checking that traffic is correctly distributed and fallback occurs on failure.

---

## Traffic Mirror

### Configuration

Traffic mirroring asynchronously copies a configurable percentage of requests to a secondary target without affecting client responses. Configure mirroring via a JSON file mounted at `/mnt/mirror.json` inside the gateway pod.

#### Mirror configuration fields

| Field           | Type    | Description                                               |
|-----------------|---------|-----------------------------------------------------------|
| `Enable`        | bool    | Master switch for mirroring                               |
| `Target`        | string  | Base URL of the mirror target                             |
| `Ratio`         | float64 | Percentage of requests to mirror (0-100)                  |
| `Timeout`       | float64 | Mirror request timeout in ms (0 = use request context)    |
| `Authorization` | string  | Override Authorization header for mirror requests         |
| `EnableLog`     | bool    | Enable mirror-related logging in gateway                  |

#### Example configuration

```json
{
  "Enable": true,
  "Target": "http://mirror-target:8000",
  "Ratio": 10,
  "Timeout": 5000,
  "Authorization": "",
  "EnableLog": true
}
```

With this configuration, approximately 10% of requests are mirrored to the target endpoint.

#### Hot-reload

The gateway watches `/mnt/mirror.json` every 10 seconds and applies changes without restart. Two approaches to update:

**Via ConfigMap** (standard Kubernetes update, may take up to 60s):
```bash
kubectl edit configmap mirror-config -n <namespace>
```

**Direct pod write** (immediate effect):
```bash
kubectl exec -it deployment/gateway -n <namespace> -c gateway -- \
  sh -c 'echo '\''{"Enable":false,"Target":"http://mirror-target:8000","Ratio":0}'\'' > /mnt/mirror.json'
```

### Deployment example

For a complete Kubernetes deployment example with traffic mirroring, see `deploy/traffic-mirror/`. This example includes an integration test (`test_traffic_mirror.sh`) that verifies:
- Requests are mirrored when mirroring is enabled
- Hot-reload disables mirroring without restarting the gateway
- Hot-reload re-enables mirroring and traffic resumes
