# PDD Forwarding Protocol

## Introduction

PDD (Prefill-Decode disaggregation) decouples LLM inference into separate prefill and decode services, enabling independent scaling and optimization of each stage for improved resource utilization and performance. The emergence of diverse inference engines, connectors, and KV cache transfer backends creates protocol heterogeneity that demands flexible abstraction for low-effort PDD protocol integration and future extensibility.

Llumnix's current supported PDD protocol implementations:

- **VLLM-KVT Protocol**: VLLM engine with KVT transfer backend
- **VLLM-Mooncake Protocol**: VLLM engine with Mooncake transfer backend

---

## Design

### Forwarder Processing Flow

The core request forwarding architecture enables the Llumnix gateway to handle diverse inference engines and transfer backends combinations through a unified forwarding abstraction:

1. **Request Pre-processing**: OpenAIHandler receives incoming requests and performs initial preprocessing
2. **Forwarder Selection**: Based on `PDDisaggProtocol` configuration, selects the appropriate forwarder from registry
3. **Protocol-Specific Forwarding Logic**: Protocols differ in request payload, dispatch pattern and scheduling mode
4. **Request Dispatch**: Forwarder sends constructed requests to target inference engines
5. **Response Post-processing**: SSE Reader processes streaming responses and passes them to OpenAI handler for post-processing

### Forwarder Logic Abstraction

The forwarder abstraction enables different protocols to share the same execution framework while implementing protocol-specific logic:

**Shared Execution Framework**
- **Unified Interface Abstraction**: All protocols implement the common `Forwarder` interface with standardized `Forward()` method
- **Common Workflow Abstraction**: Shared staged scheduling abstraction and distinct protocol-specific implementations

**Protocol-Specific Implementation**
1. **Request Body Construction**: Each protocol defines its own request body structure and may handle parameter passing between dispatch stages
2. **Dispatch Pattern Variation**: Protocols implement different dispatch patterns, including single-stage dispatch, two-stage serial dispatch, and two-stage parallel dispatch

**Two Scheduling Modes**
1. **Batch Scheduling** (`SchedulingModePDBatch`): Prefill and decode instances scheduled simultaneously, enabling overlap of KV cache transfer overhead between prefill and decode instances
2. **Staged Scheduling** (`SchedulingModePDStaged`): Schedule prefill first, then schedule decode after prefill completion, allowing dispatch to the latest low-load decode instance

---

## Protocol Implementation Details

### VLLM-KVT Protocol

**Core Features**
- Support both batch scheduling mode and staged scheduling mode
- Single-stage dispatch in batch scheduling mode, only dispatch request to decode instance

**Request Forwarding Flow**

**Batch Scheduling Mode**
1. **Batch Scheduling**: Both prefill and decode instances scheduled and retrieved for KV transfer parameters construction
2. **Decode KV Transfer Parameters Config**: KV transfer parameters built using scheduled prefill instance information
3. **Decode Dispatch**: Requests forwarded only to decode instance with KV transfer parameters embedded

**Staged Scheduling Mode**
1. **Prefill Scheduling**: Schedule prefill instance
2. **Prefill KV Transfer Parameters Config**: `do_remote_decode = true`, KVT transfer backend configures prefill instance for remote decode mode, where prefill completes computation without proceeding to decode, holding generated KV cache for decode instance to pull
3. **Prefill Dispatch**: Dispatch request to prefill instance and wait for prefill completion
4. **Decode Scheduling**: After prefill completion, schedule decode instance
5. **Decode KV Transfer Parameters Config**: `do_remote_prefill = true`, setting scheduled prefill instance information in KV transfer parameters, KV transfer backend configures decode instance for remote prefill mode, pulling KV cache from prefill instance
6. **Decode Dispatch**: Dispatch request with KV transfer parameters containing scheduled prefill instance information to decode instance, enabling decode instance to pull KV cache from remote prefill

### VLLM-Mooncake Protocol

**Core Features**
- Support both batch scheduling mode and staged scheduling mode
- Two-stage serial dispatch, dispatch request to prefill instance first, then upon prefill completion, dispatch request incorporating KV transfer parameters returned by prefill response to decode instance

**Request Forwarding Flow**

**Batch Scheduling Mode**
1. **Batch Scheduling**: Both prefill and decode instances scheduled and retrieved for KV transfer parameter construction
2. **Prefill KV Transfer Parameters Config**: `do_remote_decode = true`,  transfer backend configures prefill instance for remote decode mode, where prefill completes computation without proceeding to decode, holding generated KV cache for decode instance to pull
3. **Prefill Dispatch**: Dispatch request to prefill instance and wait for prefill completion
4. **Decode KV Transfer Parameters Config**: Upon prefill completion, setting KV transfer parameters returned by prefill response to request body
5. **Decode Dispatch**: Dispatch request incorporating KV transfer parameters returned by prefill response to decode instance, enabling decode instance to pull KV cache from remote prefill

**Staged Scheduling Mode**
All steps are the same as VLLM-KVT's Staged Scheduling Mode, except the fourth step follows VLLM-Mooncake's approach.

> **Note**: Due to VLLM-Mooncake's protocol limitation that does not support single-stage dispatch that direct dispatch request to decode instance with scheduled prefill instance information, and only supports two-stage serial dispatch where requests are first sent to prefill instances and then to decode instances after prefill completion, the staged scheduling mode is optimal for VLLM-Mooncake protocol.

---

## Usage

### Configuration

Key configuration flags (`cmd/config/config.go`):

| Flag | Default | Description |
|---|---|---|
| `--pd-disagg-protocol` | `""` | PDD protocol type: vllm-kvt/vllm-mooncake |
| `--separate-pd-scheduling` | `false` | Enable staged scheduling mode, batched scheduling mode when false |

### Deployment example

For a complete Kubernetes deployment example with vllm-mooncake PDD protocol, see `deploy/pd/full-mode-scheduling/load-balance`. For vllm-kvt PDD protocol, see `deploy/pd-kvs/full-mode-scheduling/load-balance`