# Service Discovery

## Overview

Service discovery registers instance metadata and tracks instance liveness across the cluster, enabling the [Gateway](./gateway/gateway_architecture.md) and the [Scheduler](./scheduler/policy_framework.md) to discover backend instances.

Llumnix provides two discovery paths. In [lite-mode scheduling](./scheduler/policy_framework.md#scheduling-modes-full-mode-and-lite-mode), both the Gateway and the Scheduler share the same sidecar path. In [full-mode scheduling](./scheduler/policy_framework.md#scheduling-modes-full-mode-and-lite-mode), the Scheduler switches to the [Llumlet](./llumlet/llumlet_and_llumlet_proxy.md) subprocess path because full-mode scheduling demands richer instance metadata and status that only the Llumlet subprocess path provides. The Gateway retains its own sidecar-based discovery in full-mode scheduling so that it can independently discover backend instances and fall back to round-robin routing when the Scheduler is unavailable.

| Component | Lite-mode scheduling | Full-mode scheduling |
|-----------|---------------------|---------------------|
| **Gateway** | Redis / etcd discovery | Redis / etcd discovery |
| **Scheduler** | Redis / etcd discovery | CMS discovery (backed by redis) |

---

## Discovery Paths by Scheduling Mode

**Lite-mode scheduling**: Both the Gateway and the Scheduler discover instances through the discovery sidecar path.

```{mermaid}
%%{init: {'flowchart': {'curve': 'basis', 'nodeSpacing': 30}}}%%
graph BT
    subgraph Inference_Pod["Inference Pod"]
        Sidecar[Discovery Sidecar]
        Engine[Inference Engine]
    end

    Store[(Redis / etcd)]

    Sidecar -.->|periodic health check| Engine
    Sidecar -->|put info| Store
    Store --> Gateway[Gateway]
    Store --> Scheduler[Scheduler]
```

**Full-mode scheduling**: The Gateway still uses the discovery sidecar path. The Scheduler switches to the Llumlet subprocess path, consuming richer instance metadata and status from the [Cluster Metadata Store (CMS)](./architecture.md#3-cluster-meta-store).

```{mermaid}
%%{init: {'flowchart': {'curve': 'basis', 'nodeSpacing': 30}}}%%
graph BT
    subgraph Inference_Pod["Inference Pod"]
        Sidecar[Discovery Sidecar]
        Engine[Inference Engine]
        Llumlet[Llumlet]
    end

    Store[(Redis / etcd)]
    CMS[("CMS (Redis)")]

    Sidecar -.->|periodic health check| Engine
    Llumlet -.->|periodic pull metadata| Engine
    Sidecar -->|put info| Store
    Store --> Gateway[Gateway]

    Llumlet -->|put metadata| CMS
    CMS --> Scheduler[Scheduler]
```

- **Discovery sidecar path**: A sidecar container in each inference pod health-checks the local inference engines, registers healthy instances to Redis or etcd, and maintains liveness through periodic heartbeats. The Gateway and the Scheduler (in lite-mode scheduling) consume this data through resolver components.

- **Llumlet subprocess path**: The Llumlet subprocess registers instance metadata and periodically pushes instance metadata and status to CMS (backed by Redis). The Scheduler (in full-mode scheduling) polls both metadata and status to build a rich instance view for advanced scheduling (see [full-mode scheduling](./scheduler/policy_framework.md#scheduling-modes-full-mode-and-lite-mode))policies.

---

## Discovery Sidecar Path

The discovery sidecar runs as a separate container in each inference pod. Each heartbeat cycle, it health-checks every local engine endpoint via HTTP, registers all healthy instances to the configured backend (Redis or etcd), and removes unhealthy ones. If the sidecar process dies, the backend expires the stale entry and downstream consumers remove the instance from their view.

### Supported Backends

- **Redis**: Low operational overhead — compatible with managed cloud Redis services, requiring no additional infrastructure for teams that already use Redis. The sidecar writes instance data with an embedded timestamp. Consumer-side resolvers poll Redis at a fixed interval and expire entries whose timestamp exceeds a configurable TTL.
- **etcd**: Built-in Raft consensus provides strong consistency and tolerates minority node failures, but requires deploying and maintaining a dedicated etcd cluster. The sidecar writes instance data under an etcd lease and runs a background keepalive loop. Consumer-side resolvers subscribe to etcd's Watch API for real-time change notifications, with periodic full refreshes as a reconciliation fallback.

---

## Llumlet Subprocess Path

The Llumlet subprocess runs alongside each inference engine instance, bridging the engine and the global scheduling layer. It registers two categories of data to CMS (backed by Redis):

1. **Metadata**: On startup, Llumlet registers instance metadata (instance ID, IP, ports, model type, data-parallel configuration). Metadata is periodically re-sent to refresh its TTL — if Llumlet dies, the key expires and the Scheduler removes the instance from its cluster view.

2. **Status**: Llumlet periodically pulls engine-internal status (request counts, GPU token usage, migration state) and pushes it to CMS. The Scheduler runs independent refresh loops for metadata and status, combining them into instance views for full-mode scheduling decisions.

CMS is only used in full-mode scheduling. In lite-mode scheduling, the Scheduler maintains a Local Real-time State (LRS) instead — see [Policy Framework](./scheduler/policy_framework.md#scheduling-modes-full-mode-and-lite-mode) for the comparison between full-mode scheduling and lite-mode scheduling.
