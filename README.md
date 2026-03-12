# About

Llumnix is a full-stack solution for distributed LLM inference serving. It has been a key part of the LLM serving infrastructure of [Alibaba Cloud PAI-EAS](https://help.aliyun.com/zh/pai/user-guide/overview-2?spm=a2c4g.11174283.help-menu-30347.d_3_3_0.3416192348t6Fw), a cloud-native inference serving platform, supporting production-grade inference deployments.

Llumnix provides key functionalities for modern distributed serving deployments (e.g., PD disaggregation, wide EP), such as LLM-specialized request gateway, intelligent and dynamic scheduling, high-performance KV cache transfer/storage support, etc. With a scheduler + rescheduler architecture and white-box scheduling design, Llumnix achieves fully dynamic request scheduling and pushes the performance of inference engines to the limit.

Note that with this new repository, we are re-architecting Llummix to a more modular and cloud-native design (Llumnix v1). The old Ray-based architecture ([Llumnix v0](https://github.com/llumnix-project/llumnix-ray)) is a better choice for local deployments and quick prototyping and experimentation of scheduling ideas.

[[Documentation](https://llumnix.ai)]

# Key Features
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

<div align="center">
  <img src="docs/source/image/architecture.png" width="70%" />
</div>

Components:
1. LlumSched: scheduler for initial scheduling and rescheduler for continuous rescheduling
2. Llumlet: an engine-side process that bridges global components and the inference engine
3. Cluster meta store: tracking realtime instance status
4. Engine: the inference engine (vLLM/SGLang) with Llumnix utility codes for scheduling enhancements (if using full mode)
5. Gateway: LLM-specialized capabilities, such as tokenizers, routing protocols
6. Hybrid Connector: unified KV cache control plane, using blade-kvt for KV transfer and external KV storage for offloading

# Getting Started
View our [documentation](https://github.com/llumnix-project/llumnix/tree/docs/docs/source) to learn more (the official documentation website is coming soon!).

- [Quick start](docs/source/getting_started/quick_start.md)
- [Deployment guide](docs/source/getting_started/e2e_deploy.md)
- [Development guide](docs/source/develop/developer_guide.md)


# License

Llumnix is licensed under the [Apache 2.0 License](LICENSE).
