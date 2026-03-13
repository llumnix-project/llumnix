
# Deployment Guide

## Deployment Modes Overview

### Mode Comparison

| Mode | Prefill/Decode | KV Transfer | Scheduler | Best For |
|------|---------------|-------------|-----------|------|
| **Neutral** | Combined | N/A | Optional | Getting started, simple deployments |
| **PD** | PD disaggregation | HybridConnector | Required | Production, PD disaggregation |
| **PD-KVS** | PD disaggregation  | HybridConnector  | Required | Production, prefix caching, cache-aware scheduling |
| **SLO-Aware** | PD disaggregation | HybridConnector | Required | Latency-sensitive workloads with TTFT/TPOT SLO enforcement |
| **SLO-Aware Adaptive-PD** | PD disaggregation | HybridConnector | Required | SLO enforcement with dynamic PD ratio adjustment and migration |

### Scheduling Variants

| Directory | Scheduling | Routing | Scheduler Pod | Best For |
|-----------|-----------|---------|--------------|---------|
| `full-mode-scheduling/load-balance` | Full Mode | Load Balance | Yes | **Recommended.** Load-aware routing with CMS state |
| `lite-mode-scheduling/load-balance` | Lite Mode | Load Balance | Yes | Lightweight, no CMS state tracking |
| `lite-mode-scheduling/round-robin` | Lite Mode | Round Robin | No | Simplest setup, stateless routing |

#### Full Mode vs Lite Mode

| Feature | Full Mode | Lite Mode |
|---------|-----------|-----------|
| `--enable-full-mode-scheduling` | `true` | `false` |
| CMS state tracking (Redis) | ✅  | ❌ |
| vLLM Llumnix integration | ✅ `VLLM_ENABLE_LLUMNIX=1` | ❌  |
| Scheduler CMS Redis args | ✅  | ❌  |
| Scheduling quality | Higher (load-aware) | Lower (best-effort) |

> Note: Full-mode scheduling relies on the Llumlet component
> embedded within the vLLM engine to collect and report instance metrics to
> the CMS. If you need to customize metric collection
> frequency, migration behavior, or CMS connection settings, refer to the
> [Llumlet Configuration Guide](./engine_conf.md).


## Prerequisites

### Cluster Requirements

| Component | Requirement |
|-----------|-------------|
| Kubernetes | ≥ 1.26 |
| kubectl | Compatible with cluster version |
| kustomize | ≥ 5.0 (or built-in via `kubectl kustomize`) |
| envsubst | Provided by `gettext` package |
| LeaderWorkerSet CRD | Must be installed before deployment |

Verify all tools are available:

```bash
kubectl version --client
kustomize version        # or: kubectl kustomize --help
envsubst --version

# Install envsubst if missing
# Ubuntu/Debian
apt-get install gettext-base
# CentOS/RHEL
yum install gettext
# macOS
brew install gettext && brew link --force gettext
```

### Node Resource Requirements

#### GPU Nodes

Resource requirements differ by mode and configuration:

| Mode | Component | GPU | CPU | Memory |
|------|-----------|-----|-----|--------|
| **Neutral** | neutral Pod | 4 | 32 | 256 G |
| **PD** | prefill Pod | 4 | 32 | 256 G |
| **PD** | decode Pod | 4 | 32 | 256 G | 
| **PD-KVS** | prefill Pod | 1 | 16 | 128 G |
| **PD-KVS** | decode Pod | 1 | 16 | 128 G |
| **SLO-Aware** | prefill Pod | 1 | 8 | 256 G |
| **SLO-Aware** | decode Pod | 1 | 8 | 256 G |

> Note：These are the default values from the example configurations.

#### CPU Nodes (for Gateway / Scheduler / Redis)

| Resource | Minimum |
|----------|---------|
| CPU | 1 core |
| Memory | 1 Gi |

## Before You Begin

### Install LeaderWorkerSet CRD

All deployment modes depend on the **LeaderWorkerSet CRD**. This must be installed regardless of which mode you choose.

```bash
# Install LWS
kubectl apply --server-side \
  -f https://github.com/kubernetes-sigs/lws/releases/latest/download/manifests.yaml

# Verify installation
kubectl get crd leaderworkersets.leaderworkerset.x-k8s.io
# Expected output:
# NAME                                          CREATED AT
# leaderworkersets.leaderworkerset.x-k8s.io    2026-xx-xx
```

### Verify GPU Node Availability

```bash
kubectl get nodes -o custom-columns=\
"NAME:.metadata.name,\
GPU:.status.allocatable.nvidia\.com/gpu,\
CPU:.status.allocatable.cpu,\
MEM:.status.allocatable.memory"

# Example output — at least one GPU node must be available:
# NAME              GPU   CPU   MEM
# node-gpu-01       8     96    512Gi  
```


## Neutral Mode

In neutral mode, each Pod runs both prefill and decode within a single vLLM instance. This is the simplest deployment mode.

### Deploy

```bash
cd deploy

# Full-mode scheduling with load balance (recommended)
./group_deploy.sh llumnix neutral/full-mode-scheduling/load-balance

# Lite-mode scheduling with load balance
./group_deploy.sh llumnix neutral/lite-mode-scheduling/load-balance

# Lite-mode scheduling with round-robin (no Scheduler)
./group_deploy.sh llumnix neutral/lite-mode-scheduling/round-robin
```

### Deployed Components

| Component | full-mode/load-balance | lite-mode/load-balance | lite-mode/round-robin |
|-----------|----------------------|----------------------|----------------------|
| Redis | ✅ | ✅ | ✅ |
| Neutral Pod | ✅ | ✅ | ✅ |
| Gateway | ✅ | ✅ | ✅ |
| Scheduler | ✅ | ✅ | ❌ |

### Expected Output

```
Using repository: llumnix-registry.cn-beijing.cr.aliyuncs.com/llumnix
Gateway tag:      20260313-094911
Scheduler tag:    20260313-094904
vLLM tag:         20260130-105854
Creating namespace: llumnix
...
NAME                READY   STATUS    NODE
gateway-xxx         1/1     Running   node-a
redis-xxx           1/1     Running   node-a
scheduler-xxx       1/1     Running   node-a
neutral-0           0/2     Running   gpu-node
```

> Note: `neutral-0` will show `0/2 Running` while vLLM loads the model. This typically takes a few minutes.


## PD Mode

In PD mode, Prefill and Decode run in separate Pods. In the provided example (`deploy/pd/full-mode-scheduling/load-balance/`), KV Cache is transferred using **HybridConnector** with the **kvt** backend.

### Default Resource Requirements

| Component | GPU | CPU | Memory |
|-----------|-----|-----|--------|
| Prefill Pod | 4 (`TP_SIZE=4`) | 32 | 256 G |
| Decode Pod | 4 (`TP_SIZE=4`) | 32 | 256 G |

### Deploy

```bash
cd deploy
./group_deploy.sh llumnix pd/full-mode-scheduling/load-balance
```

### Deployed Components

| Component | Description |
|-----------|-------------|
| Redis | Service discovery + CMS state |
| Prefill Pod | vLLM with `HybridConnector`, role `kv_producer` (kvt backend) |
| Decode Pod | vLLM with `HybridConnector`, role `kv_consumer` (kvt backend) |
| Gateway | PD disagg protocol: `vllm-kvt` |
| Scheduler | Full-mode scheduling with CMS Redis |

### Expected Output

```
NAME                READY   STATUS    NODE
decode-0            0/2     Running   gpu-node-a
gateway-xxx         1/1     Running   node-a
prefill-0           0/2     Running   gpu-node-b
redis-xxx           1/1     Running   node-a
scheduler-xxx       1/1     Running   node-a
```
> Note: `prefill-0` and `decode-0 ` will show `0/2 Running` while vLLM loads the model. This typically takes a few minutes.


## PD-KVS Mode

PD-KVS mode extends PD mode by introducing a **KV Cache Store** (backed by Mooncake) for centralized KV Cache management. This enables **prefix caching** and **cache-aware scheduling**.

### Additional Requirements

PD-KVS mode requires RDMA hardware for KV Cache transfer:
1. An RDMA-capable network adapter must be present.
   Verify with:
   ```bash
   ls /sys/class/infiniband/
   # Example output on Alibaba Cloud: erdma_0
   ```

2. The InfiniBand device directory must exist:
	```bash
    ls /dev/infiniband/
    # Expected: rdma_cm  uverbs0  ...
	```

3. Update device_name in prefill.yaml to match your hardware:
  ```yaml
   "device_name": "erdma_0"   # Replace with your actual device name
  ```


### Default Resource Requirements

| Component | GPU | CPU | Memory |
|-----------|-----|-----|--------|
| Prefill Pod | 1 (`TP_SIZE=1`) | 16 | 128 G |
| Decode Pod | 1 (`DP_SIZE_LOCAL=1`) | 16 | 128 G |
| Mooncake Master | 0 | 32 | 128 G |

> ⚠️ The Mooncake Master Pod does **not** require GPU, but has significant CPU and memory requirements.

### Deploy

```bash
cd deploy
./group_deploy.sh llumnix pd-kvs/full-mode-scheduling/load-balance
```

> Note: PD-KVS mode requires a vLLM image built with Mooncake support. Build it with: `bash scripts/build_vllm_release.sh --include_mooncake` (optionally add `--tag <tag>` for a fixed tag). The default image tag is `mooncake-<timestamp>`. When deploying with custom images, pass that tag via `--mooncake-vllm-tag`.

### Deployed Components

| Component | Description |
|-----------|-------------|
| Redis | Service discovery + CMS state |
| Mooncake Master | KV Cache Store coordinator (RPC :50051, Metadata :50052, Metrics :9003) |
| Prefill Pod | vLLM with `HybridConnector`, role `kv_both` (kvs+kvt backend) |
| Decode Pod | vLLM with `HybridConnector`, role `kv_consumer` (kvt backend) |
| Gateway | PD disagg protocol: `vllm-kvt` |
| Scheduler | Full-mode scheduling + cache-aware scheduling via Mooncake metadata |

### Expected Output

```
NAME                        READY   STATUS    NODE
decode-0                    0/2     Running   gpu-node-a
gateway-xxx                 1/1     Running   node-a
mooncake-xxx                1/1     Running   node-b
prefill-0                   0/2     Running   gpu-node-b
redis-xxx                   1/1     Running   node-a
scheduler-xxx               1/1     Running   node-a
```

## SLO-Aware Mode

SLO-Aware base mode targets **latency-sensitive production workloads** where Time-to-First-Token (TTFT) and Time-per-Output-Token (TPOT) Service Level Objectives (SLOs) must be satisfied. But in adaptive PD mode, the Scheduler always returns an instance even if it does not satisfy the SLOs. It builds on PD disaggregation (the same HybridConnector + kvt KV transfer as PD mode), but the Scheduler uses an SLO-driven policy backed by per-model profiling data to make dispatch decisions.

Two sub-variants are provided:

| Variant | Directory | Adaptive PD | Migration | Best For |
|---------|-----------|-------------|-----------|----------|
| **Base** | `slo-aware/base` | ❌ | ❌ | SLO enforcement with fixed PD ratio |
| **Adaptive-PD** | `slo-aware/adaptive-pd` | ✅ | ✅ | SLO constraints with dynamic PD ratio and request migration |

### Additional Requirements

SLO-Aware mode requires RDMA hardware for KV Cache transfer (same as PD mode):

1. An RDMA-capable network adapter must be present. Verify with:
   ```bash
   ls /sys/class/infiniband/
   # Example output on Alibaba Cloud: erdma_0
   ```

2. The InfiniBand device directory must exist:
   ```bash
   ls /dev/infiniband/
   # Expected: rdma_cm  uverbs0  ...
   ```

### Default Resource Requirements

| Component | GPU | CPU | Memory | Replicas |
|-----------|-----|-----|--------|----------|
| Prefill Pod | 1 (`TP_SIZE=1`) | 8 | 256 Gi | 2 |
| Decode Pod | 1 (`TP_SIZE=1`) | 8 | 256 Gi | 2 |

### Deploy

```bash
cd deploy

# SLO-Aware base (fixed PD ratio)
./group_deploy.sh llumnix slo-aware/base

# SLO-Aware with Adaptive PD (dynamic PD ratio + migration)
./group_deploy.sh llumnix slo-aware/adaptive-pd
```

### Deployed Components

| Component | Description |
|-----------|-------------|
| Redis | Service discovery + CMS state |
| Prefill Pod | vLLM with `HybridConnector`, role `kv_producer` (kvt backend), `LLUMNIX_INSTANCE_TYPE=prefill` |
| Decode Pod | vLLM with `HybridConnector`, role `kv_consumer` (kvt backend), `LLUMNIX_INSTANCE_TYPE=decode` |
| Gateway | SLO scheduling policy (`--scheduling-policy slo`), PD disagg protocol `vllm-kvt` |
| Scheduler | SLO policy + full-mode scheduling; profiling data downloaded at init |

### SLO Parameters

The Scheduler uses pre-collected profiling data (TTFT and TPOT latency curves) to predict whether dispatching a request to a given instance will satisfy the configured SLOs. The default values in the provided YAML are tuned for **Qwen3-32B on H20**:

| Parameter | Default | Description |
|-----------|---------|-------------|
| `--ttft-slo` | `6000` ms | TTFT SLO target |
| `--tpot-slo` | `50` ms | TPOT SLO target |
| `--ttft-slo-dispatch-threshold` | `0.9` | Dispatch only when predicted TTFT satisfaction probability ≥ 0.9 |
| `--tpot-slo-dispatch-threshold` | `0.9` | Dispatch only when predicted TPOT satisfaction probability ≥ 0.9 |
| `--ttft-profiling-data-path` | `/profiling/Qwen3-32B-h20/ttft.json` | Path to TTFT profiling data |
| `--tpot-profiling-data-path` | `/profiling/Qwen3-32B-h20/tpot.json` | Path to TPOT profiling data |

The profiling data is automatically downloaded at Scheduler startup from `https://llumnix.oss-cn-beijing.aliyuncs.com/profiling/Qwen3-32B-h20/`. To use a different model, replace the profiling data path and supply your own profiling files.

### Adaptive PD (`slo-aware/adaptive-pd`)

The Adaptive-PD variant adds dynamic PD ratio adjustment and request migration on top of the base SLO policy. When a decode instance is predicted to violate the TPOT SLO, the Scheduler can migrate in-flight requests to other decode instances. The additional Scheduler flags are:

| Parameter | Value | Description |
|-----------|-------|-------------|
| `--enable-adaptive-pd` | `true` | Enable adaptive PD ratio scheduling |
| `--colocated-rescheduling-mode` | `true` | Allow rescheduling across colocated instances |
| `--rescheduling-interval-ms` | `100` | Rescheduling check interval |
| `--rescheduling-policies` | `binpacking_mitigation,binpacking_consolidation` | Policies applied during rescheduling |
| `--tpot-migrate-out-floor-threshold` | `0.6` | Start migrating out when TPOT satisfaction drops below this value |
| `--tpot-migrate-out-ceil-threshold` | `0.95` | Stop migrating out once TPOT satisfaction rises above this value |

The Gateway in this variant also enables `--separate-pd-scheduling=true`, which causes prefill and decode instance selection to be performed independently.

### Expected Output

```
NAME                READY   STATUS    NODE
decode-0            0/2     Running   gpu-node-a
decode-1            0/2     Running   gpu-node-b
gateway-xxx         1/1     Running   node-a
prefill-0           0/2     Running   gpu-node-c
prefill-1           0/2     Running   gpu-node-d
redis-xxx           1/1     Running   node-a
scheduler-xxx       1/1     Running   node-a
```

> Note: `prefill-*` and `decode-*` will show `0/2 Running` while vLLM loads the model. This typically takes a few minutes.


## Configuration Reference

### Changing the Model

Update the `vllm serve` command in the respective yaml file and update the tokenizer path in `gateway.yaml`.

**vLLM Pod yaml (neutral.yaml / prefill.yaml / decode.yaml):**

```yaml
args:
  - |-
    ...
    vllm serve \
      your-org/your-model-name \    # ← Replace here
      ...
```

**gateway.yaml — initContainer:**

```yaml
args:
  - |
    python3 << 'EOF'
    from modelscope import snapshot_download
    model_dir = snapshot_download(
        'your-org/your-model-name',    # ← Replace here
        cache_dir='/tokenizers',
        allow_patterns=['tokenizer.json', 'tokenizer_config.json']
    )
    EOF
```

**gateway.yaml — gateway container args:**

```yaml
- "--tokenizer-path"
- "/tokenizers/your-org/your-model-name"   # ← Replace here
```

### Using a Custom Registry

Pass `--repository` and each component's image tag to the deploy script. For PD-KVS mode, also pass `--mooncake-vllm-tag` (the tag of the image built with `build_vllm_release.sh --include_mooncake`).

```bash
./group_deploy.sh llumnix neutral/full-mode-scheduling/load-balance \
    --repository my-registry.example.com/my-namespace \
    --gateway-tag 20260101-120000 \
    --scheduler-tag 20260101-130000 \
    --vllm-tag 20260101-140000 \
    --discovery-tag 20260101-150000
```

Or export environment variables before calling `group_update.sh`:

```bash
export REPOSITORY="my-registry.example.com/my-namespace"
export GATEWAY_IMAGE_TAG="20260101-120000"
export SCHEDULER_IMAGE_TAG="20260101-130000"
export VLLM_IMAGE_TAG="20260101-140000"
export DISCOVERY_IMAGE_TAG="20260101-150000"

./group_update.sh llumnix neutral/full-mode-scheduling/load-balance
```
### Advanced: Llumlet Configuration

The vLLM Pods in full-mode scheduling run an embedded **Llumlet** process,
which acts as the bridge between the vLLM inference engine and the Llumix
management layer. It is responsible for:

- Reporting real-time instance status and metrics to the CMS
- Receiving and executing migration commands from the Scheduler
- Registering instance metadata for service discovery

The default environment variables in the provided YAML files are sufficient
for most deployments. If you need to customize the following, refer to the
[Llumlet Configuration Guide](./engine_conf.md):

## Update and Teardown

### Update a Running Deployment

After modifying any yaml files, apply changes using:

```bash
# group_deploy.sh will call group_update.sh internally
./group_deploy.sh llumnix neutral/full-mode-scheduling/load-balance

# Or call group_update.sh directly (requires env vars to be set)
export REPOSITORY="llumnix-registry.cn-beijing.cr.aliyuncs.com/llumnix"
export GATEWAY_IMAGE_TAG="20260313-094911"
export SCHEDULER_IMAGE_TAG="20260313-094904"
export VLLM_IMAGE_TAG="20260306-165123"
export DISCOVERY_IMAGE_TAG="20260302-203317"

./group_update.sh llumnix neutral/full-mode-scheduling/load-balance
```

### Delete a Deployment

```bash
./group_delete.sh llumnix
```

The script will display all resources to be deleted and prompt for confirmation:

```
==> Resources to be deleted:
--- Deployments ---
gateway     redis     scheduler
--- Services ---
gateway     redis     scheduler
...
Confirm deletion of group 'llumnix' and all its resources? (yes/no): yes
✓ Service group 'llumnix' deleted successfully
```