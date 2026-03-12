# Llumnix Documentation

Llumnix is a full-stack solution for distributed LLM inference serving, 
featuring fully dynamic request scheduling for modern LLM deployments.

---

## 🚀 Getting Started

New to Llumnix? Start here.

- [Quick Start](getting_started/quick_start.md) — Deploy your first Llumnix cluster in minutes
- [Deployment Guide](getting_started/e2e_deploy.md) - Full deployment guide covering all modes: neutral, PD, and PD-KVS
- [Benchmark](getting_started/benchmark.md) - Performance benchmarks for Llumnix on Kubernetes 
- [Llumlet Configuration](getting_started/engine_conf.md) — Configure Llumlet

---

## 🛠️ Development Guide

- [Development Setup](develop/developer_guide.md) — Environment setup, build commands, and e2e unit tests guide
- [Build Images](develop/image_build) — Manually build and push component images

---

## 🏗️ Design

Understand how Llumnix works internally.

- [Architecture Overview](design/architecture.md) — Full-stack component design
- Gateway
  - [Gateway Architecture](./design/gateway/gateway_architecture.md) - Gateway architecture and basic functionalities
  - [PDD Protocol](./design/gateway/pdd_protocol.md) — Prefill-Decode disaggregation protocol
  - [Batch Inference](./design/gateway/batch_inference.md) — Batch inference support
- Scheduler
  - [Scheduling Policy Framework](./design/scheduler/policy_framework.md) — Scheduling policy design
  - [Instant and Accurate Load for Scheduling](./design/scheduler/instant_accurate_load.md) — Instance load obervation and modeling
  - [Cache-aware Scheduling](./design/scheduler/cache_aware_scheduling.md) — Scheduling with KV cache state awareness
  - [SLO-aware Scheduling](./design/scheduler/slo_aware_scheduling.md) — Scheduling with SLO awareness
  - [Adaptive PD Scheduling](./design/scheduler/adaptive_pd_scheduling.md) — Adaptive P/D role assignment to maximize SLO attainment
  - [Rescheduler](./design/scheduler/rescheduler.md) — Continuous rescheduling via request migration
- Llumlet
  - [Llumlet and Llumlet Proxy](./design/llumlet/llumlet_and_llumlet_proxy.md) — Engine-side agent bridging local engine and global scheduler
  - [Real-time Instance Status Tracking](./design/llumlet/realtime_instance_status_tracking.md) — How Llumnix tracks engine state with minimal delay and overhead
  - [Migration](./design/llumlet/request_migration.md) — Request migration implementation

---

:::{toctree}
:hidden:
:caption: Getting Started

getting_started/quick_start
getting_started/e2e_deploy
getting_started/engine_conf
getting_started/benchmark
:::

:::{toctree}
:hidden:
:caption: Development

develop/developer_guide
develop/image_build
:::

:::{toctree}
:hidden:
:caption: Design Documents

design/architecture
design/gateway/index
design/scheduler/index
design/llumlet/index
:::
