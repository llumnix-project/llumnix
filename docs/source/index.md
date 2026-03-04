# Llumnix Documentation

Llumnix is a full-stack solution for distributed LLM inference serving, 
featuring fully dynamic request scheduling for modern LLM deployments.

---

## 🚀 Getting Started

New to Llumnix? Start here.

- [Quick Start](getting_started/quick_start.md) — Deploy your first Llumnix cluster in minutes
- [Deployment Guide](getting_started/e2e_deploy.md) - Full deployment guide covering all modes: neutral, PD, and PD-KVS
- [Llumlet Configuration](getting_started/engine_conf.md) — Configure Llumlet

---

## 🏗️ Design

Understand how Llumnix works internally.

- [Architecture Overview](design/architecture.md) — Full-stack component design
- Scheduler
	- [Scheduling Policy Framework](design/policy_framework.md) — Scheduler Policy design
	- [Instant and Accurate Load for Scheduling](design/instant_accurate_load.md) — Load Obervation and Modeling
	- [KV-aware Scheduling](design/cache_aware_scheduling.md) — Scheduling with KV cache state awareness
	- [Rescheduling(Coming Soon)](design/rescheduling.md) — Continuous rebalancing via request migration
- Gateway
- Llumlet
	- [Llumlet & Llumlet Proxy](design/Llumlet&Llumlet_proxy.md) — Engine-side agent bridging local engine and global scheduler
	- [Realtime Instance Status Tracking](design/real_time_instance_status_tracking.md) — How Llumnix tracks engine state with minimal overhead
	- [Migration](design/request_migration.md) — Request migration implementation

---

## 🛠️ Development Guide

- [Development Setup](develop/developer_guide.md) — Environment setup, build commands, and e2e unit tests guide
- [Build Images](develop/image_build) — Manually build and push component images

---

:::{toctree}
:hidden:
:caption: Getting Started

getting_started/quick_start
getting_started/e2e_deploy
getting_started/engine_conf
:::

:::{toctree}
:hidden:
:caption: Design Documents

design/architecture
design/policy_framework
design/instant_accurate_load
design/cache_aware_scheduling
design/request_migration
design/Llumlet&Llumlet_proxy
design/real_time_instance_status_tracking
:::

:::{toctree}
:hidden:
:caption: Development

develop/developer_guide
develop/image_build
:::
