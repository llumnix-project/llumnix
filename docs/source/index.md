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
- Scheduler
	- [Scheduling Policy Framework](./design/scheduling/policy_framework.md) — Scheduling policy design
	- [Instant and Accurate Load for Scheduling](./design/scheduling/instant_accurate_load.md) — Instance load obervation and modeling
	- [KV-aware Scheduling](./design/scheduling/cache_aware_scheduling.md) — Scheduling with KV cache state awareness
    - [SLO-aware Scheduling](./design/scheduling/slo_aware_scheduling.md) — Scheduling with SLO awareness
	- [Adaptive PD Scheduling](./design/scheduling/adaptive_pd_scheduling.md) — Adaptive P/D role assignment to maximize SLO attainment
- Rescheduler
	- [Rescheduler](./design/rescheduler.md) — Continuous rescheduling via request migration
- Gateway
  - [Gateway Architecture](./design/gateway/gateway_architecture.md) - Gateway architecture and basic functionalities
	- [PDD Protocol](./design/gateway/pdd_protocol.md) — Prefill-Decode disaggregation protocol
	- [Batch Inference](./design/gateway/batch_inference.md) — Batch inference support
- Llumlet
	- [Llumlet & Llumlet Proxy](./design/llumlet/Llumlet&Llumlet_proxy.md) — Engine-side agent bridging local engine and global scheduler
	- [Realtime Instance Status Tracking](./design/scheduling/real_time_instance_status_tracking.md) — How Llumnix tracks engine state with minimal delay and overhead
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
design/scheduling/index
design/llumlet/index
design/gateway/index
design/rescheduler
:::
