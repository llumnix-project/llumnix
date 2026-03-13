# Blade-KVT (KV Transfer)

## Overview

KVT (KV Transfer) is a high-performance, zero-overhead KV Cache transfer module designed for distributed LLM inference scenarios. It handles efficient KV Cache transmission between two nodes, supporting multiple model architectures and cache layouts.

## Design Goals

KVT's design originates from the following core requirements:

1. **Bypass Design**: No major changes to the main step flow, enabling sidecar-style integration
2. **Zero Overhead**: No additional load introduced to the step execution path due to KV Cache transfer
3. **Full CUDA Graph Compatibility**: Supports CUDA Graph optimization without introducing CPU synchronization points
4. **Generality**: Supports multiple model architectures (FlashAttention, GDN, DSA, etc.) and cache layouts

## Core Architecture

KVT consists of four core modules:

### 1. Access Layer (Python Binding)

Handles Python-side integration with the following functions:
- CUDA Event notification for layer computation completion, enabling Full CUDA Graph compatibility
- Supports P node full-cuda-graph, D node requires no logic execution

### 2. ParseBlock (Block Parser)

Calculates the list of `IpcBlock`s to send based on layer and request information:

```rust
struct IpcBlock {
    src_off: usize,   // Source offset
    dst_off: usize,   // Destination offset
    len: usize,       // Transfer length
}
```

**Design Evolution**:

Early KVT assumed Cache shape of `(num_blocks, block_size, 2, num_heads, head_dim)`, meaning K/V for each token resides in the same block.

However, vLLM uses FlashAttention with Cache shape of `(2, num_blocks, block_size, num_kv_heads, head_size)`, meaning K/V are separated into different blocks.

**Solution**: Extract ParseBlock as a pluggable strategy:
- During initialization, `block_size_bytes` and `token_size_bytes` are still calculated assuming "token kv together"
- ParseBlock reinterprets offsets based on actual layout during parsing

This design enables support for new architectures (e.g., Qwen3-Next GDN, DeepSeek DSA) by simply adding new ParseBlock implementations.

### 3. Control Layer

Responsible for:
- Remote connection maintenance
- Listening for layer computation completion signals (CUDA Event)
- Scheduling data transfer via the transport layer
- Error handling and fault tolerance for transfer failures

### 4. Transport Layer

Handles `Vec<IpcBlock>` transmission, supporting multiple backends:

- **GPU Direct RDMA (GDR)**: Direct GPU memory access, lowest latency
- **TCP**: Bypasses GPU/GDR path, isolated from EP all2all traffic
- **Shared Memory**: Single-node multi-GPU scenarios

## Physical Layout Abstraction

KVT provides the following transfer abstraction:

```
Each layer → One GPU memory region
    ↓
Memory → Multiple Blocks (same byte size)
    ↓
Block → Multiple Tokens (same byte size)
```

Physical layout parameters passed during initialization:
- `block_size_bytes`: Byte size per block
- `token_size_bytes`: Byte size per token
- `num_blocks`: Number of blocks

## Integration with HybridConnector

KVT works in coordination with HybridConnector:

```
┌─────────────────────────────────────────────────────┐
│              vLLM Engine (Python)                   │
│                                                     │
│  ┌─────────────┐    ┌──────────────────────────┐    │
│  │  Scheduler  │    │   HybridConnector        │    │
│  └─────────────┘    │  ┌────────────────────┐  │    │
│                     │  │   KVT ( C++ )      │  │    │
│                     │  │  ┌──────────────┐  │  │    │
│                     │  │  │ ParseBlock   │  │  │    │
│                     │  │  └──────────────┘  │  │    │
│                     │  └────────────────────┘  │    │
│                     └──────────────────────────┘    │
└─────────────────────────────────────────────────────┘
```

**Responsibility Division**:
- **KVT**: Handles low-level KV Cache transfer
- **HybridConnector**: Manages request lifecycle, fault tolerance, and Backend coordination

## Project Status

- ✅ FlashAttention cache layout support
- ✅ Full CUDA Graph compatibility
- ✅ GDR transfer support
- ✅ TCP transfer support
- ✅ Qwen3-Next GDN support
- ✅ DeepSeek DSA support
