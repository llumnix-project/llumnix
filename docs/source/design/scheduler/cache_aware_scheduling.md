# Cache-aware Scheduling

## Introduction

Prefill computation scales quadratically with prompt length. Prefix caching mitigates this by reusing KV cache from previously computed prefixes, confining computation to the unmatched suffix. Distributed KV cache stores (e.g. Mooncake) further extend cache capacity by pooling host DRAM across nodes. As cached prefixes spread across instances, the scheduler must be aware of their distribution to exploit reuse. Cache-aware scheduling addresses this by incorporating prefix cache affinity into dispatch decisions alongside load balancing.

## Design and implementation

```{mermaid}
graph TB
    Request([Request])

    subgraph Llumnix[" "]
        direction LR
        subgraph Gateway
            Tokenizer[Tokenizer]
        end
        subgraph Scheduler
            Hash("1. Token Hashing")
            Lookup("2. Cache Hit Lookup")
            Dispatch("3. Cache-aware Dispatch")
        end
    end

    KVS[(KVS Metadata Service)]

    subgraph InferenceService["Inference Service"]
        direction TB
        subgraph InstanceN[Instance N]
            direction LR
            EngineN[Inference Engine]
            StoreN[KV Cache Store]
        end
        subgraph Instance1[Instance 1]
            direction LR
            Engine1[Inference Engine]
            Store1[KV Cache Store]
        end
        subgraph Instance0[Instance 0]
            direction LR
            Engine0[Inference Engine]
            Store0[KV Cache Store]
        end
    end

    Request --> Tokenizer
    Tokenizer -->|token IDs| Hash
    Hash --> Lookup
    Lookup <-->|query / cache locations| KVS
    Lookup --> Dispatch
    Dispatch -->|instance ID| Gateway
    Gateway -->|dispatch| InferenceService
    KVS -.-|metadata management| InferenceService
```

Existing distributed inference scheduling projects typically maintain global cache state (e.g. LRU cache, global radix tree) inside the scheduler and synchronize KV cache events with inference engines via a KV indexer or equivalent. Llumnix instead leverages the KVS metadata service, which already tracks KV cache object locations as part of the distributed KV cache store, eliminating the need for separate synchronization and yielding a simpler and more extensible architecture.

For each incoming request, the gateway tokenizes the prompt and passes the token IDs to the scheduler. The scheduler hashes the token IDs into prefix chunk keys, queries the KVS metadata service for cache hits, and selects the instance that best balances cache affinity and load. The gateway then dispatches the request to the selected instance.

### Prompt token hashing

The gateway first tokenizes the request's prompt into token IDs via the tokenizer. The scheduler then splits the token IDs into fixed-size chunks (`KvsChunkSize`) and computes a deterministic hash for each chunk. The scheduler uses the same hashing algorithm and chunk size as the KVS metadata service, ensuring that the prefix hashes produced by the scheduler are identical to the keys stored in KVS. This alignment allows the scheduler to directly query the KVS metadata service for prefix cache hits.

> **Note**: For algorithms that require a hash seed (e.g. `sha256_cbor`), the scheduler and KVS must use the same seed. The scheduler reads its seed from the `GO_HASH_SEED` environment variable (default `"0"`).

Hashing is implemented in `TokenHasher` (`pkg/scheduler/hasher/token_hasher.go`), which supports multiple algorithms (`sha256_hex`, `sha256_cbor`, `xxhash`) configured via `KvsHashAlgo`. Hashes are computed as a chain: each chunk's hash takes the previous chunk's hash as input. Requests sharing the same prompt prefix thus yield identical hash sequences up to the point of divergence, enabling prefix cache hit lookup.

### KVS metadata service lookup

After hashing, the scheduler sends all prefix hashes to the KVS metadata service in a single batch query via `KVSClient` (`pkg/scheduler/kvs/kvs_client.go`). The metadata service returns, for each prefix hash, the set of KVS instances (identified by IP) that have cached the corresponding chunk.

> **Note**: The KVS metadata service typically takes one of two forms: a Redis database or the KVS master service. For the Mooncake backend, it corresponds to Mooncake's master service, which exposes an HTTP endpoint `/batch_query_keys` for batch key lookup.

The scheduler converts KVS instance IPs to instance IDs, then computes each instance's longest contiguous prefix hit length.

`KVSClient` includes retry and health gating. After `kvsRetryTimes` consecutive failures, it marks the metadata service as down and skips queries for `kvsMetadataServiceDownDuration`.

### Cache-aware dispatch

The scheduler writes three fields into each instance's per-request scheduling context (`schedulingCtx` in `pkg/scheduler/scheduling-policy/scheduling_policy.go`):

- `prefixHitTokens`: number of prompt tokens already cached on the instance.
- `prefixHitRatio`: `prefixHitTokens / numPromptTokens`.
- `prefixMissTokens`: `numPromptTokens - prefixHitTokens`, i.e. the tokens that still require prefill computation.

Two cache-aware metrics use these fields:

- `kvCacheHitLen` (`metrics.go`): reads `prefixHitTokens` directly; higher is better, so the selector prefers the instance with the longest cached prefix.
- `CacheAwareAllPrefillsTokensNum` (`metrics.go`): computes `prefixMissTokens + allPrefillsTokensNum` (the existing all-waiting-prefill load); lower is better. This metric balances cache affinity and load in a single interpretable value without normalized weighting: `prefixMissTokens` captures cache locality, while `allPrefillsTokensNum` captures existing prefill load, and both terms share the same unit (token count) so they are directly additive.

When cache-aware scheduling is enabled, the cache locality metric (default `cache_aware_all_prefills_tokens_num`) is prepended to the selector's metric list for both prefill and neutral infer types. The selector compares instances metric-by-metric in order, selecting on the first differentiating metric and falling through to the next on ties. This makes cache-aware metric the primary dispatch criterion and load balance metric the tiebreaker.

### Cache-aware prefill load modelling

Prefill compute load is proportional to uncomputed prefill tokens. Cached prefix tokens require zero computation, so raw prompt token count and request count that do not account for cache hits are coarse metrics that systematically overestimate prefill load.

The **instance status local account** (defined in [Instant and Accurate Load — Dispatch-time account with reconciliation](instant_accurate_load.md#cms-engine-side-path)) leverages cache hit query results from the KVS metadata service to close this gap, recording `numUncomputedTokens = numTokens - prefixHitNumTokens` per request instead of the full token count, so that load reflects the true uncomputed prefill cost.

This correction applies at two levels:

- **In-flight requests** (defined in [Instant and Accurate Load — Dispatch-time account with reconciliation](instant_accurate_load.md#cms-engine-side-path)): accumulates `numUncomputedTokens` into `NumUncomputedTokensInflightDispatchPrefillRequests`, so that pending dispatch load reflects actual computation cost.
- **Engine waiting requests**: when CMS reports waiting request IDs, recomputes `NumUncomputedTokensAllWaitingPrefills` by summing each request's `NumUncomputedTokens`, replacing the engine-reported raw token count.

## Usage

### Prerequisites

Llumnix's cache-aware scheduling requires the inference engine to use a global KV cache store (e.g. Mooncake). Llumnix queries the global KV cache store's metadata service to obtain prefix cache hit information for each request, so this feature is only applicable when such a store is deployed.

Cache-aware scheduling is **disabled by default**. It is only supported in **full-mode scheduling**; the scheduler forcibly disables it when full-mode is not enabled.

### Configuration

Key configuration flags (`cmd/config/config.go`):

| Flag | Default | Description |
|---|---|---|
| `--enable-cache-aware-scheduling` | `false` | Enable or disable cache-aware scheduling |
| `--cache-aware-scheduling-min-tokens` | `1024` | Minimum prompt length to trigger cache-aware logic |
| `--kvs-backend` | `mooncake` | KVS metadata service backend |
| `--kvs-hash-algo` | `sha256_cbor` | Hash algorithm for prompt chunking |
| `--kvs-chunk-size` | `256` | Token chunk size |
| `--kvs-enable-save-unfull-chunk` | `false` | Whether to hash the last incomplete chunk |
| `--kvs-retry-times` | `5` | Retry count for metadata service queries |
| `--kvs-retry-interval-ms` | `100` | Interval between retries |
| `--kvs-metadata-service-down-duration-s` | `30` | Duration to treat metadata service as down after failures |

Environment variables:

| Variable | Default | Description |
|---|---|---|
| `GO_HASH_SEED` | `"0"` | Hash seed for algorithms that require one (e.g. `sha256_cbor`). Must match the seed used by the KVS |

### Deployment example

For a complete Kubernetes deployment example with PD disaggregation, Mooncake as KVS, and cache-aware scheduling enabled, see `deploy/pd-kvs/full-mode-scheduling/load-balance/`.

