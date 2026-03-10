# About

Llumnix is a full-stack solution for distributed LLM inference serving. It has been a key part of the LLM serving infrastructure of [PAI-EAS](https://help.aliyun.com/zh/pai/user-guide/overview-2?spm=a2c4g.11174283.help-menu-30347.d_3_3_0.3416192348t6Fw), a cloud-native inference serving platform on Alibaba Cloud, supporting production-grade inference deployments.

Llumnix provides key functionalities for modern distributed serving deployments (e.g., PD disaggregation, wide EP), such as LLM-specialized request gateway, intelligent and dynamic scheduling, high-performance KV cache transfer/storage support, etc. With a scheduler + rescheduler architecture and white-box scheduling design, Llumnix achieves fully dynamic request scheduling and pushes the performance of inference engines to the limit.

Note that with this new repository, we are re-architecting Llummix to a more modular and cloud-native design (Llumnix v1). The old Ray-based architecture (Llumnix v0) is a better choice for local deployments and quick prototyping and experimentation of scheduling ideas.

# Key features
- **Scheduler + rescheduler architecture** for fully dynamic request scheduling: initial routing + continuous migration
- **Advanced scheduling policies**: load balancing, KV-aware, SLO/predictor-based scheduling, adaptive PD disaggregation, etc.
- **Dual-mode scheduling**
  - Full mode (white-box) for max performance with engine participation
  - Lite mode (black-box) for engine-transparent deployments
- **Realtime instance status tracking** for optimal scheduling quality
- **Modular, extensible policy framework** for easily implementing and composing scheduling policies
- **LLM-specialized request gateway**: tokenizers, diverse request routing / disaggregation protocols, batch API, etc.
- **High-performance KV cache support** (see [llumnix-kv](https://github.com/llumnix-project/llumnix-kv))
  - Efficient, flexible data plane for KV cache transfer (blade-kvt)
  - Unified control plane for PD disaggregation, migration, KV storage (hybrid-connector)
- **High availability**
  - Fault tolerance for Llumnix components
  - Engine health monitoring and reactive (re-)scheduling upon engine failures

# Architecture
Llumnix is more than a "router". It has a full-stack design to support advanced scheduling features.
![image](docs/source/image/architecture.png)
## Components:
1. LlumSched: scheduler for initial scheduling and rescheduler for continuous rescheduling
2. Llumlet: an engine-side process that bridges global components and the inference engine
3. Cluster meta store: tracking realtime instance status
4. Engine: the inference engine (vLLM/SGLang) with Llumnix utility codes for scheduling enhancements (if using full mode)
5. Gateway: LLM-specialized capabilities, such as tokenizers, routing protocols
6. Hybrid Connector: unified KV cache control plane, using blade-kvt for KV transfer and external KV storage for offloading

# Getting Started
Llumnix supports multiple deployment modes and scheduling strategies to meet different inference requirements. 

### Prerequisites
- Kubernetes >= 1.20 with GPU nodes (NVIDIA GPU Operator installed)
- [LeaderWorkerSet Operator](https://github.com/kubernetes-sigs/lws) installed
- `kubectl` configured with cluster access
### Deployment

Run deploy script:

```bash
cd deploy/

bash group_deploy.sh <group-name> <kustomize-dir>
# e.g. bash group_deploy.sh llumnix neutral/lite-mode-scheduling/load-balance
```

Update an existing deployment (namespace must already exist):

```bash
cd deploy/
bash group_update.sh <group-name> <kustomize-dir> \
  [--repository <registry>/<namespace>] \
  [--gateway-tag <tag>] \
  [--scheduler-tag <tag>] \
  [--vllm-tag <tag>] \
  [--discovery-tag <tag>] \
  [--mooncake-vllm-tag <tag>]
```
### Verify & Test

```bash
# Check pod status
kubectl get pods -n <group-name> -o wide

# Get gateway service port
kubectl get svc -n <group-name> | grep gateway

# Forward gateway port to local (replace <gateway_port> with the port from above command)
kubectl port-forward -n <group-name> svc/gateway 8080:<gateway-port>
```
Test inference API ,open a new terminal and run:
```bash
curl http://localhost:8080/v1/completions \
  -H "Content-Type: application/json" \
  -d '{"prompt": "Hello!", "max_tokens": 50}'
```

Llumnix supports the following deployment modes:
```bash
cd deploy/

# Deploy Neutral mode with lite-mode scheduling
bash group_deploy.sh llumnix neutral/lite-mode-scheduling/load-balance

# Deploy Neutral mode with full-mode scheduling
bash group_deploy.sh llumnix neutral/full-mode-scheduling/load-balance

# Deploy PD mode
bash group_deploy.sh llumnix pd/full-mode-scheduling/load-balance

# Deploy PD-KVS mode
bash group_deploy.sh llumnix pd-kvs/full-mode-scheduling/load-balance
```

Run `bash group_delete.sh $<group-name>` to delete group.

# Development Guide

`llumnix-registry.cn-beijing.cr.aliyuncs.com/llumnix/vllm:dev-20260204-140225` is recommended for development. Then, you should run the following commands to set up the environment:

```bash
go mod tidy

# install patched vllm
make vllm-install

# install llumlet package
make llumlet-install

# install discovery package
make discovery-install

# build lib-tokenizers
make lib-tokenizers-build

# build blade-kvt
make blade-kvt-install

# build mooncake
make mooncake-install
```

Run `make gateway-build` to build the gateway binary and `make scheduler-build` to build the scheduler binary. And `make e2e-test` is used to run all end-to-end tests. Please refer to [tests/local/utils.py](tests/local/utils.py) for the details of launching commands. `make unit-test` is used to run all go unit tests.

## Build Images (Optional)

We provide pre-built images for all components. If you need to build images manually (e.g., for custom modifications), follow the steps below.

### Prerequisites
- Docker installed and running
- Access to the target image registry

### Build Steps

```bash
# Step 1: Build lib-tokenizers (only required once)
bash scripts/build_tokenizers.sh

# Step 2: Build Gateway
bash scripts/build_component_bin.sh gateway
bash scripts/build_component_release.sh gateway

# Step 3: Build Scheduler
bash scripts/build_component_bin.sh scheduler
bash scripts/build_component_release.sh scheduler

# Step 4: Build Discovery
bash scripts/build_discovery_whl.sh
bash scripts/build_component_release.sh discovery

# Step 5: Build LLM Backend (vLLM)
bash scripts/build_llumnix_whl.sh
bash scripts/build_vllm_release.sh
# For PD-KVS mode, also build Mooncake-enabled image: bash scripts/build_vllm_release.sh --include_mooncake
# Optional: --push, --repository <registry>/vllm, --tag <image-tag>
```

# License

Llumnix is licensed under the [Apache 2.0 License](LICENSE).
