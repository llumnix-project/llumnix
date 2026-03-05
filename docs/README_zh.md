# LLM Gateway

[English](../README.md)

LLM Gateway 是面向大语言模型（LLM）推理服务的高性能网关，提供统一的请求入口、智能调度、负载均衡与流量管理能力。

**工作原理**：Gateway 负责处理客户端请求并转发至推理 Worker；Scheduler 维护所有 Worker 的实时负载状态，为 Gateway 提供最优调度决策。Gateway 可以独立工作（本地轮询），也可以与 Scheduler 协作实现智能负载均衡。

---

## 目录

- [LLM Gateway](#llm-gateway)
  - [目录](#目录)
  - [整体架构](#整体架构)
  - [API 接口](#api-接口)
  - [Gateway](#gateway)
    - [服务路由](#服务路由)
    - [处理器链](#处理器链)
    - [容错机制](#容错机制)
    - [流量镜像](#流量镜像)
    - [批处理](#批处理)
    - [Tokenizer](#tokenizer)
  - [Scheduler](#scheduler)
    - [调度策略](#调度策略)
    - [本地实时状态 (LRS)](#本地实时状态-lrs)
    - [限流器](#限流器)
    - [PD 分离模式](#pd-分离模式)
  - [服务发现](#服务发现)
  - [可观测性](#可观测性)
  - [配置说明](#配置说明)
    - [服务基础](#服务基础)
    - [调度与负载均衡](#调度与负载均衡)
    - [服务发现](#服务发现-1)
  - [构建与运行](#构建与运行)
    - [构建](#构建)
    - [本地测试](#本地测试)
      - [无 Scheduler](#无-scheduler)
      - [含 Scheduler](#含-scheduler)
      - [请求调用](#请求调用)
    - [发布](#发布)

---

## 整体架构

![LLM Gateway 架构](architecture.svg)

**架构说明**：
- **Gateway 层（多实例）**：无状态部署，处理完整请求数据链路（解析→队列→调度→转发→响应）
- **Scheduler 层（单例）**：集中式调度服务，通过 **LRS (Local Realtime State)** 维护实时负载状态、管理请求资源
- **Worker 层（多实例）**：推理引擎实例，支持三种角色：
  - **Normal**：普通服务，可独立执行完整请求
  - **Prefill / Decode**：PD 分离模式下协作执行
- **Keepalive**：Gateway 与 Scheduler 之间的心跳保活机制

**链路分离**：
- **请求数据链路（①④⑤⑥）**：Client → Gateway → Worker → Gateway → Client，完整数据闭环
- **调度链路（②③）**：Gateway ↔ Scheduler，仅传递调度元数据（endpoint 选择、资源释放）

---

## API 接口

**Gateway 接口**：

| 方法            | 路径                        | 说明                        |
| --------------- | --------------------------- | --------------------------- |
| POST            | `/v1/chat/completions`      | OpenAI Chat Completions API |
| POST            | `/v1/completions`           | OpenAI Completions API      |
| GET             | `/v1/models`                | 获取可用模型列表            |
| GET             | `/get_server_info`          | 获取服务器信息              |
| POST            | `/v1/messages`              | Anthropic Messages API      |
| POST            | `/v1/messages/count_tokens` | Token 计数                  |
| POST/GET/DELETE | `/batch/*`                  | 批处理服务操作              |
| GET             | `/metrics`                  | Prometheus 指标             |
| GET             | `/healthz`                  | 健康检查                    |

**Scheduler 接口**：

| 方法 | 路径            | 说明                            |
| ---- | --------------- | ------------------------------- |
| POST | `/schedule`     | 接收调度请求，返回选定的 Worker |
| POST | `/release`      | 请求完成后释放 Worker 资源      |
| POST | `/report`       | 接收 Gateway 上报的实时请求状态 |
| GET  | `/keepalive`    | WebSocket 长连接，Gateway 心跳  |
| GET  | `/ws/keepalive` | Worker 注册 WebSocket           |
| GET  | `/healthz`      | 健康检查                        |
| GET  | `/metrics`      | Prometheus 指标                 |

---

## Gateway

### 服务路由

**路径**：[`pkg/gateway/service/router/`](../pkg/gateway/service/router/)

`ServiceRouter` 在 Load Balancer 之前运行，决定请求最终去向：

| 路由类型        | 描述                                       |
| --------------- | ------------------------------------------ |
| `RouteInternal` | 路由至内部 Worker（走 Load Balancer）      |
| `RouteExternal` | 转发至外部 URL（如第三方 OpenAI 兼容接口） |
| `RouteUnknown`  | 无匹配，尝试 Fallback                      |

支持两种路由策略：
- **weight**：按权重随机分流，支持 A/B 测试
- **prefix**：按请求的 `model` 名称前缀匹配，将不同模型路由至不同后端

---

### 处理器链

**路径**：[`pkg/gateway/processor/`](../pkg/gateway/processor/)

请求在进入 Handler 前/后经过可配置的处理器链：

- **PreProcessorChain**：请求预处理（如 Tokenizer 计数、参数校验、内容改写）
- **PostProcessorChain**：响应后处理（如响应格式化、Tool Call 解析、Reasoning 解析）

---

### 容错机制

提供重试与降级机制：

| 功能     | 说明                                                         |
| -------- | ------------------------------------------------------------ |
| 重试     | `--retry-count` 设置转发失败重试次数                         |
| 重试排除 | `--retry-exclude-scope` 控制排除粒度（`instance` 或 `host`） |
| 降级     | `ServiceRouter` 配置中标记 `"fallback": true` 的路由作为兜底 |

---

### 流量镜像

**路径**：[`pkg/gateway/service/mirror/`](../pkg/gateway/service/mirror/)

将请求流量镜像到指定端点，用于测试或分析，不影响主请求流程。

---

### 批处理

**路径**：[`pkg/gateway/batch/`](../pkg/gateway/batch/)

通过 `--batch-oss-path` 配置启用，提供离线批量推理能力：

- 任务文件存储于阿里云 OSS
- 任务状态通过 Redis 持久化
- `TaskReactor` 循环拉取待处理任务，按 Shard 并行处理
- 每条记录独立发起推理请求，支持重试

---

### Tokenizer

**路径**：[`pkg/gateway/tokenizer/`](../pkg/gateway/tokenizer/)

Token 计数与管理：

| 参数                   | 说明                  |
| ---------------------- | --------------------- |
| `--tokenizer-name`     | 内置 tokenizer 名称   |
| `--tokenizer-path`     | 自定义 tokenizer 路径 |
| `--chat-template-path` | Chat 模板路径         |

---

## Scheduler

### 调度策略

通过 `--schedule-policy` 配置：

| 策略            | 描述                                                             |
| --------------- | ---------------------------------------------------------------- |
| `round-robin`   | 纯本地轮询，无需 Scheduler 进程                                  |
| `least-token`   | 选择当前占用 token 数最少的实例（需 Scheduler）                  |
| `least-request` | 选择当前处理请求数最少的实例（需 Scheduler）                     |
| `prefix-cache`  | 基于前缀缓存命中率选择实例，最大化 KV Cache 复用（需 Scheduler） |

---

### 本地实时状态 (LRS)

**路径**：[`pkg/scheduler/lrs/`](../pkg/scheduler/lrs/)

维护每个 Worker 的实时状态：
- Gateway 调 `/report` 上报增量 token 状态
- Scheduler 通过 LRS 在调度时读取各实例负载，选取最优 Worker

---

### 限流器

**路径**：[`pkg/scheduler/rate-limiter/`](../pkg/scheduler/rate-limiter/)

请求限流功能，保护后端服务免受过载。

---

### PD 分离模式

LLM Gateway 支持两种推理模式：

**普通模式（默认）**：请求由单个 Worker 完成完整的 Prefill + Decode 过程，适用于大多数场景。

**PD 分离模式**：将 Prefill 和 Decode 阶段分离到不同 Worker 执行，通过 `--pdsplit-mode` 开启，支持以下实现方式：

| 模式              | 描述                                 |
| ----------------- | ------------------------------------ |
| `vllm-kvt`        | vLLM + KV Transfer 方式传输 KV Cache |
| `vllm-vineyard`   | vLLM + Vineyard（分布式内存）        |
| `vllm-mooncake`   | vLLM + Mooncake 传输层               |
| `sglang-mooncake` | SGLang + Mooncake 传输层             |

**工作流程**：

```
Gateway
  │
  ├─ 1. 调度 Prefill Worker（InferStagePrefill）
  │       Balancer.Get() → prefillLocalBalancer
  │
  ├─ 2. Handler 将请求发至 Prefill Worker
  │       完成后触发 OnPostPrefillStream
  │       ├─ 释放 Prefill Worker 资源
  │       └─ 记录 TTFT 指标
  │
  ├─ 3. 调度 Decode Worker（InferStageDecode）
  │       PDSeparateSchedulerImpl.ScheduleDecode()
  │       → decodeLocalBalancer
  │
  └─ 4. Handler 将 Decode 请求发至 Decode Worker
          逐 chunk 触发 OnPostDecodeStreamChunk
          → LRS 上报实时状态
```

**分离调度模式（`--separate-pd-schedule`）**：Prefill 和 Decode 分两次独立调度（`ScheduleModePDStaged`）；否则两者合并一次调度（`ScheduleModePDBatch`）。

---

## 服务发现

**路径**：[`pkg/resolver/`](../pkg/resolver/)

支持多种服务发现机制，实现 `LLMResolver` 接口：

| 实现                | 描述                             |
| ------------------- | -------------------------------- |
| `EASResolver`       | 阿里云 EAS 平台内置服务发现      |
| `RedisResolver`     | 基于 Redis 的实例注册与发现      |
| `MsgBusResolver`    | 基于消息总线的实例发现           |
| `EndpointsResolver` | 静态 endpoint 列表（本地测试用） |

所有解析器实现 `Watch(ctx)` 方法，通过 channel 推送 `WorkerEventAdd` / `WorkerEventRemove` / `WorkerEventFullSync` 事件，驱动 LRS 状态更新。

---

## 可观测性

**Prometheus 指标**（`/metrics` 端点）：

| 指标名                     | 类型      | 说明                                         |
| -------------------------- | --------- | -------------------------------------------- |
| `llm_requests`             | Counter   | 请求总数（按 status_code、model 等标签聚合） |
| `llm_response_time`        | Histogram | 请求总耗时（ms）                             |
| `llm_ttft`                 | Histogram | 首 token 延迟（Time To First Token，ms）     |
| `llm_tpot`                 | Histogram | 每 token 延迟（Time Per Output Token，ms）   |
| `gateway_pending_requests` | Gauge     | 队列中 + 调度中的请求数                      |
| `gateway_requests`         | Gauge     | 当前总活跃请求数                             |

**访问日志**格式（`--enable-access-log`）：
```
Request completed [<id>] status_code:<code>,method:<method>,url:<url>;
<timing>;input_tokens:<n>,total_tokens:<n>;sch_results:<worker>;
queue:<n>,sch:<n>,work:<n>,model:<model>
```

---

## 配置说明

启动参数（`--help` 查看完整列表），常用配置分组如下：

### 服务基础

| 参数              | 默认值    | 说明                  |
| ----------------- | --------- | --------------------- |
| `--port`          | `8001`    | 监听端口              |
| `--host`          | `0.0.0.0` | 监听地址              |
| `--schedule-mode` | `false`   | 以 Scheduler 模式启动 |
| `--llm-scheduler` | —         | Scheduler 服务地址    |

### 调度与负载均衡

| 参数                     | 默认值        | 说明                   |
| ------------------------ | ------------- | ---------------------- |
| `--schedule-policy`      | `least-token` | 调度策略               |
| `--pdsplit-mode`         | —             | PD 分离模式            |
| `--separate-pd-schedule` | `false`       | 是否分阶段独立调度 P/D |
| `--max-queue-size`       | `512`         | 请求缓冲队列最大长度   |

### 服务发现

| 参数                   | 默认值 | 说明                                               |
| ---------------------- | ------ | -------------------------------------------------- |
| `--use-discovery`      | —      | 发现模式（`cache-server`、`message-bus`、`redis`） |
| `--discovery-endpoint` | —      | 发现服务地址                                       |

---

## 构建与运行

### 构建

```bash
make llm-gateway-build
```

### 本地测试

#### 无 Scheduler

启动 mock 推理服务，Gateway 直接轮询转发：

```bash
# 启动 mock 推理服务
./bin/mock-llm-server --port 8080 --model mock-model -v=2
./bin/mock-llm-server --port 8081 --model mock-model -v=2

# 启动 Gateway
./bin/llm-gateway --port 8001 --local-test-ip 127.0.0.1:8080,127.0.0.1:8081
```

#### 含 Scheduler

Gateway 通过 Scheduler 进行智能调度：

```bash
# 启动 mock 推理服务
./bin/mock-llm-server --port 8080 --model mock-model -v=2
./bin/mock-llm-server --port 8081 --model mock-model -v=2

# 启动 Gateway
./bin/llm-gateway -v=2 --port 8001 \
  --local-test-scheduler-ip 127.0.0.1:8002 \
  --local-test-ip 127.0.0.1:8080,127.0.0.1:8081

# 启动 Scheduler
./bin/llm-gateway -v=2 --schedule-mode --port 8002 \
  --local-test-backend-ip 127.0.0.1:8080,127.0.0.1:8081
```

#### 请求调用

Chat Completions（流式）：

```bash
curl http://127.0.0.1:8001/v1/chat/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "mock-model",
    "stream": true,
    "messages": [{"role": "user", "content": "What is the weather like in New York?"}]
  }'
```

Completions（非流式）：

```bash
curl http://127.0.0.1:8001/v1/completions \
  -H "Content-Type: application/json" \
  -d '{
    "model": "mock-model",
    "stream": false,
    "prompt": "Hello!",
    "max_tokens": 128
  }'
```

### 发布

```bash
make llm-gateway-docker-build    # 构建 arm/amd 双架构镜像
make llm-gateway-docker-release  # 发布镜像
```

