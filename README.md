# About Llumnix

Llumnix is a full-stack solution for distributed LLM inference serving. It has been a key part of the LLM serving infrastructure of PAI-EAS, a cloud-native inference serving platform on Alibaba Cloud, supporting production-grade inference deployments.
Llumnix features
distributed serving system designed for modern LLM inference deployments (e.g., PD disaggregation, large-scale EP), with a current focus on advanced request scheduling. With a scheduler + rescheduler architecture and white-box scheduling design, Llumnix achieves fully dynamic request scheduling and pushes the performance of inference engines to the limit.
With this new repository, we are re-architecting Llummix to a more modular and cloud-native design (Llumnix v1). The old Ray-based architecture (Llumnix v0) is a better choice for local deployments and quick prototyping and experimentation of scheduling ideas.

# Key features
1. Scheduler + rescheduler architecture for fully dynamic request scheduling
    a. Scheduler for initial routing
    b. Rescheduler for continuous migration
2. Smart scheduling policies for modern distributed serving: 
    a. Extreme load balancing for PD+EP: migration-enhanced DPLB, predictor-based prefill scheduling, etc.
    b. Precise KV-aware scheduling
    c. Adaptive PD disaggregation: taming instantaneous P-D load fluctuation
    d. ...
3. Realtime tracking of instance status for optimal scheduling quality
    a. Lightweight scheduler-engine sync to eliminate information lag
4. Modular, extensible scheduling policy framework for easily implementing and composing new policies
5. Dual-mode scheduling
    a. Full mode for max performance with engine participation (white-box)
    b. Basic mode for engine-transparent deployments (black-box)
6. LLM-specialized request gateway: tokenizers, diverse request routing / disaggregation protocols, etc.
7. Fault tolerance
    a. Fault tolerance for Llumnix components
    b. Engine health monitoring and reacitve (re-)scheduling upon engine failures

# Architecture
Llumnix is more than a "router". It has a full-stack design to support advanced scheduling features.
![image](docs/source/image/architecture.png)
## Components:
1. LlumSched: scheduler for initial scheduling and rescheduler for continuous rescheduling
2. Llumlet: an engine-side process that bridges global components and the inference engine
3. Cluster meta store: tracking realtime instance status
4. Engine: the inference engine (vLLM/SGLang) with Llumnix utility codes for scheduling enhancements
5. Gateway: LLM-specialized capabilities, such as tokenizers, routing protocols

# Getting Started
Llumnix supports multiple deployment modes and scheduling strategies to meet different inference requirements. 

### Prerequisites
- Kubernetes >= 1.20 with GPU nodes (NVIDIA GPU Operator installed)
- [LeaderWorkerSet Operator](https://github.com/kubernetes-sigs/lws) installed
- `kubectl` configured with cluster access
### Deployment

Run deploy script:

```bash
bash deploy/group_deploy.sh <group-name> <kustomize-dir>
#e.g. bash deploy/group_deploy.sh llumnix normal/lite-mode-scheduling/load-balance
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
# Deploy Normal mode with lite-mode scheduling
bash deploy/group_deploy.sh llumnix normal/lite-mode-scheduling/load-balance

# Deploy Normal mode with full-mode scheduling
bash deploy/group_deploy.sh llumnix normal/full-mode-scheduling/load-balance

# Deploy PD mode
bash deploy/group_deploy.sh llumnix pd/full-mode-scheduling/load-balance

# Deploy PD-KVS mode
bash deploy/group_deploy.sh llumnix pd-kvs/full-mode-scheduling/load-balance
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
