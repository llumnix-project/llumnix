# Llumnix Documentation

Llumnix is a full-stack solution for distributed LLM inference serving, 
featuring fully dynamic request scheduling for modern LLM deployments.

---

## 🚀 Getting Started

New to Llumnix? Start here.

- [Quick Start (Coming Soon)](getting_started/quick_start.md) — Deploy your first Llumnix cluster in minutes
- [Deployment Modes（Coming Soon）](getting_started/deployment_modes.md) — Choose between Normal, PD, and PD-KVS modes
- [Llumlet Configuration](getting_started/engine_conf.md) — Configure Llumlet

---

## 🏗️ Design

Understand how Llumnix works internally.

- [Architecture Overview (Coming Soon)](design/architecture.md) — Full-stack component design and data flow
- [Scheduling Design (Coming Soon)](design/scheduling.md) — Scheduler and rescheduler design
- [Predictor-enhanced Scheduling(Coming Soon)](design/predictor_scheduling.md) — Prefill load prediction for smarter routing
- [KV-aware Scheduling(Coming Soon)](design/kv_scheduling.md) — Scheduling with KV cache state awareness
- [Rescheduling(Coming Soon)](design/rescheduling.md) — Continuous rebalancing via request migration
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

getting_started/prerequisites
getting_started/quick_start
getting_started/deployment_modes
getting_started/engine_conf
:::

:::{toctree}
:hidden:
:caption: Design Documents

design/architecture
design/scheduling
design/predictor_scheduling
design/kv_scheduling
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
