# Rescheduler

## Introduction

Llumnix's rescheduler is a request rescheduling component that continuously rebalances load, maximizes SLO attainment and resource efficiency under fluctuating workloads, and proactively migrates requests from unhealthy instances. Operating in a **scheduler + rescheduler** architecture, the rescheduler complements the initial dispatch by performing ongoing rescheduling of running/waiting requests through two integrated components:

- **Rescheduling Planner**: Evaluates instance load using configurable policies (metrics → filters → selectors) to generate migration decisions.
- **Migration Dispatcher**: Initiates gRPC commands to llumlet processes to trigger request transfer with KV cache state.

The rescheduler serves three primary functions:

1. **Load Balancing**: Continuously monitors instance load and migrates requests from overloaded instances to underutilized ones, mitigating fragmentation and eliminating hotspots for improved latency.

2. **Adaptive PD Rescheduling**: Enhances adaptive prefill-decode disaggregation by migrating decode requests based on predicted TPOT, consolidating underutilized instances and mitigating overloaded ones to further improve SLO attainment and resource efficiency. See [Adaptive PD Scheduling](./adaptive_pd_scheduling.md) for details.

3. **Failover**: Detects unhealthy or unschedulable instances and proactively migrates their requests to healthy instances within configurable failure domains (instance/node/unit domain).

---

## Architecture

### Architectural Overview

The rescheduler operates within Llumnix's scheduling layer:

- **Scheduler**: Makes one-time routing decisions for incoming requests.
- **Rescheduler**: Performs continuous migration decisions for running/waiting requests.

```
┌──────────────────────────────────────────────────────────┐
│                  ReschedulerService                      │
│  ┌────────────────────────────────────────────────────┐  │
│  │              ReschedulingPolicy                    │  │
│  │  ┌──────────────────────────────────────────────┐  │  │
│  │  │  policies[] (multiple policy instances)      │  │  │
│  │  │   - load balance                             │  │  │
│  │  │   - failover                                 │  │  │
│  │  │   - adaptive PD                              │  │  │
│  │  └──────────────────────────────────────────────┘  │  │
│  │  - cmsClient (cluster view)                        │  │
│  │  - llumletClientManager (gRPC to engines)          │  │
│  └────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────┘
                            │
                            │ periodic loop
                            ↓
        ┌───────────────────────────────────────────┐
        │             ReschedulingLoop              │
        │  For each policy in policies[]:           │
        │  1. Calculate metrics                     │
        │  2. Filter src/dst candidates             │
        │  3. Select migration pairs                │
        │  4. Validate & append to global list      │
        └───────────────────────────────────────────┘
                            │
                            │ aggregated pairs
                            ↓
        ┌───────────────────────────────────────────┐
        │            Execute Migrations             │
        │  Parallel gRPC calls to llumlets          │
        └───────────────────────────────────────────┘
                            │
                            │ gRPC Migrate()
                            ↓
        ┌───────────────────────────────────────────┐
        │          Llumlet (engine side)            │
        │  - Receives migration command             │
        │  - Selects migration requests             │
        │  - Coordinates KV cache transfer          │
        └───────────────────────────────────────────┘
```

### Execution Flow

The rescheduler executes in a continuous loop with the following steps:

1. **Periodic Trigger**: `ReschedulingLoop()` runs at intervals configured by `reschedulingIntervalMs`.

2. **Cluster View Refresh**: Pulls latest instance status from Cluster Metadata Store.
3. **Multi-Policy Sequential Execution**: Iterates through all policies in `policies[]` list. Each policy executes independently:
   - **Metric Calculation**: Policy-specific metrics are computed for all instances (e.g., `kv_cache_usage_ratio_projected`).
   - **Candidate Filtering**: Applies single-instance filters (infer type, schedulability, staleness) and global filters (failover domain) to identify valid source and destination instances.
   - **Pair Selection**: Uses policy-specific selector (e.g., `metricBalanceSelector`, `roundRobinSelector`) to generate migration pairs from high-load sources to low-load destinations.
   - **Validation & Aggregation**: Each selected pair is validated against existing pairs to avoid conflicts (no A→B and B→A). Valid pairs are appended to global `reschedulingPairs` list.

4. **Migration Execution**: `executeMigrations()` dispatches all aggregated pairs concurrently via gRPC and blocks until all results are collected.

5. **Cycle Completion**: Returns to sleep until next interval, then repeats from step 2.
---

## Rescheduling Policies

The rescheduler implements a **multi-policy** architecture where each policy operates independently and produces migration pairs. Policies are configured via comma-separated lists (`--rescheduling-policies`).

### Policy Registry

Built-in policies (defined in `pkg/consts/consts.go`):

| Policy Name | Infer Type | Purpose |
|-------------|------------|----------|
| `decode_load` | Decode | Balance decode instance load |
| `neutral_load` | Neutral | Balance neutral instance load |
| `prefill_failover` | Prefill | Migrate from failing prefill instances |
| `decode_failover` | Decode | Migrate from failing decode instances |
| `neutral_failover` | Neutral | Migrate from failing neutral instances |
| `binpacking_mitigation` | Decode | Mitigate TPOT SLO violations |
| `binpacking_consolidation` | Decode | Consolidate underutilized instances |

Each policy implements its own metric calculation, instance filtering, and pair selection logic. See sections 3.2-3.4 for detailed mechanisms.

### Load Balance Policy

**Purpose**: Reduces load imbalance across instances of the same infer type within a configurable scope (cluster-wide or per-unit).

**Mechanism**:
- **Source filter**: Instances with load metric >= threshold (configurable via `--rescheduling-decode-load-threshold` or `--rescheduling-neutral-load-threshold`).
- **Destination filter**: Instances with load metric < threshold.
- **Selector**: `metricBalanceSelector` pairs highest-load sources with lowest-load destinations.
- **Scope**: Supports `cluster` (global balancing) or `unit` (per-unit balancing) via `--rescheduling-load-balance-scope`.

**Configuration**:
```bash
--rescheduling-policies=decode_load,neutral_load
--rescheduling-decode-load-metric=kv_cache_usage_ratio_projected
--rescheduling-decode-load-threshold=0.7
--rescheduling-load-balance-scope=cluster
--rescheduling-req-select-rule=TOKEN
--rescheduling-req-select-order=SR
--rescheduling-req-select-value=1024
```

**Example**: In a decode cluster with 5 instances (load: 0.9, 0.3, 0.8, 0.2, 0.4), the policy would:
1. Filter sources: instances with load >= 0.7 → [0.9, 0.8]
2. Filter destinations: instances with load < 0.7 → [0.3, 0.2, 0.4]
3. Pair: (0.9 → 0.2), (0.8 → 0.3)
4. Migrate specified number of tokens from source to destination.

### Adaptive PD Rescheduling Policies

For the full design of adaptive PD scheduling (including scheduler dispatch logic and rescheduling), see [Adaptive PD Scheduling](./adaptive_pd_scheduling.md).

**Purpose**: Enhances adaptive prefill-decode disaggregation by migrating decode requests based on predicted TPOT, consolidating underutilized instances and mitigating overloaded ones to further improve SLO attainment and resource efficiency.

**Background**: In PD-disaggregated deployments with static partitioning, load fluctuations cause suboptimal resource utilization: decode instances may become overloaded (TPOT exceeds SLO) or underutilized (TPOT well below SLO). The rescheduler complements the scheduler's adaptive PD logic by proactively rebalancing decode requests across instances.

**Migration Constraints**:
- Only migrate decode requests (prefill requests remain stationary).
- At most one migration pair per policy per rescheduling cycle.
- Excludes prefill-reserved instances from migration sources/destinations.

**Built-in Policies**:

| Policy Name | Purpose | Source Filter | Destination Filter | Selector |
|-------------|---------|---------------|-------------------|----------|
| `binpacking_mitigation` | Mitigate TPOT SLO violations | Predicted TPOT >= ceiling threshold (default 0.95 × TPOT SLO) | Predicted TPOT < dispatch threshold (default 0.85 × TPOT SLO) | `binPackingMitigationSelector` |
| `binpacking_consolidation` | Consolidate underutilized instances | Predicted TPOT < floor threshold (default 0.60 × TPOT SLO) AND decode batch size > 0.1 | Predicted TPOT < dispatch threshold AND decode batch size > 0.1 | `binPackingConsolidationSelector` |

**Threshold Configuration**:
```bash
--enable-adaptive-pd=true
--scheduling-policy=slo
--tpot-slo=50
--tpot-slo-dispatch-threshold=0.85
--tpot-migrate-out-ceil-threshold=0.95
--tpot-migrate-out-floor-threshold=0.60
--rescheduling-policies=binpacking_mitigation,binpacking_consolidation
--rescheduling-interval-ms=100
--colocated-rescheduling-mode=true
```

**Example - Mitigation**: Instance D1 has predicted TPOT = 48ms (exceeds 0.95 × 50ms = 47.5ms ceiling). Policy migrates decode requests from D1 to D2 (predicted TPOT = 35ms, below 0.85 × 50ms = 42.5ms dispatch threshold).

**Example - Consolidation**: Instance D3 has predicted TPOT = 25ms (below 0.60 × 50ms = 30ms floor) with active decode requests. Policy migrates all requests from D3 to D4 (predicted TPOT = 40ms, heavily loaded but SLO-compliant), freeing D3 for prefill assignment.

> **Note**: For details, see [Adaptive PD Scheduling](./adaptive_pd_scheduling.md).

### Failover Policy

**Purpose**: Responds to unhealthy or unschedulable instances by migrating their requests to healthy instances outside the failure domain.

**Mechanism**:
- **Source filter**: Global `failoverMigrationSrcFilter` identifies instances requiring failover based on `needsFailover` marks set by schedulability and staleness filters.
- **Destination filter**: Excludes all instances within the failure domain (configured via `--failover-domain`).
- **Selector**: `roundRobinSelector` distributes requests evenly across available destinations.

**Failover Domains** (defined in `pkg/consts/consts.go`):
- `instance`: Only the failed instance itself (default).
- `node`: All instances on the same physical node.
- `instance-unit`: All instances sharing the same unit with the failed instance.
- `node-unit`: All instances sharing units with instances on the failed node.

**Configuration**:
```bash
--rescheduling-policies=prefill_failover,decode_failover,neutral_failover
--failover-domain=node
--instance-staleness-seconds=100
```

**Example**: If instance `decode-3` becomes unschedulable with `failover-domain=node`:
1. Source filter: Marks `decode-3` as needing failover.
2. Global filter: Blocks all instances on the same node as `decode-3`.
3. Destination filter: Keeps only instances on other nodes.
4. Selector: Round-robin assigns requests from `decode-3` to remaining instances.

---

## Migration Implementation

### Migration Request Types

The rescheduler supports three migration granularities (defined in `pkg/consts/consts.go`):

| Type | Constant | Description |
|------|----------|-------------|
| `NUM_REQ` | `MigrationReqSelectRuleNumReq` | Migrate N requests |
| `TOKEN` | `MigrationReqSelectRuleToken` | Migrate N tokens |
| `RATIO` | `MigrationReqSelectRuleRatio` | Migrate N% of KV cache |

### Migration Request Ordering

When selecting which requests to migrate, the following ordering policies are supported:

| Order | Constant | Description|
|-------|----------|-------------|
| `LCR` | `MigrationReqSelectOrderLCR` | Last Come Running: the last request to come (among running requests) |
| `FCR` | `MigrationReqSelectOrderFCR` | First Come Running: the first request to come (among running requests) |
| `LR` | `MigrationReqSelectOrderLR` | Longest Running: the request with the longest sequence length |
| `SR` | `MigrationReqSelectOrderSR` | Shortest Running: the request with the shortest sequence length |
| `FCW` | `MigrationReqSelectOrderFCW` | First Come Waiting: the first request to come (among waiting requests) |
| `FCWSR` | `MigrationReqSelectOrderFCWSR` | First Come Waiting, if none exist, then Shortest Running |

### Execution Pipeline

The migration execution pipeline (lines 177-259 in `rescheduling_policy.go`):

1. **Channel Setup**: Creates a buffered channel to collect results from concurrent migrations.

2. **Parallel Dispatch**: Launches goroutines for each migration pair:
   - Retrieves gRPC client from connection pool.
   - Constructs `MigrateRequest` with source/destination instance IDs, migration type, order, and value.
   - Issues gRPC call with timeout (configured via `--llumlet-grpc-timeout-seconds`).

3. **Error Handling**: 
   - Logs timeout errors separately from other gRPC failures.
   - Records `rescheduling_failed_count` metric on failure.
   - Continues executing remaining migrations despite individual failures.

4. **Result Aggregation**: Collects all results and returns them for monitoring/debugging.

**Concurrency Model**: All migrations execute in parallel, bounded only by gRPC connection pool size (`--llumlet-grpc-connection-pool-size`).

---

## Configuration

### Core Rescheduling Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--enable-rescheduling` | `false` | Enable rescheduling |
| `--rescheduling-policies` | `"decode_load,prefill_failover,decode_failover,neutral_failover"` | Comma-separated list of rescheduling policies |
| `--rescheduling-interval-ms` | `500` | Interval between rescheduling iterations |
| `--colocated-rescheduling-mode` | `false` | Run rescheduler inside scheduler process |
| `--standalone-rescheduling-mode` | `false` | Run rescheduler as separate process |

### Load Balance Configuration

| Flag | Default | Description|
|------|---------|-------------|
| `--rescheduling-decode-load-metric` | `"kv_cache_usage_ratio_projected"` | Load metric for decode instances |
| `--rescheduling-decode-load-threshold` | `1.0` | Threshold for source/destination filtering: instances >= this value are migration sources, instances < this value are migration destinations |
| `--rescheduling-neutral-load-metric` | `"kv_cache_usage_ratio_projected"` | Load metric for neutral instances |
| `--rescheduling-neutral-load-threshold` | `1.0` | Threshold for source/destination filtering: instances >= this value are migration sources, instances < this value are migration destinations |
| `--rescheduling-load-balance-threshold` | `0.0` | Minimum load difference required to trigger migration |
| `--rescheduling-load-balance-scope` | `"cluster"` | Balancing scope: `cluster` or `unit` |

### Adaptive PD Configuration

| Flag | Default | Description |
|------|---------|-------------|
| `--enable-adaptive-pd` | `false` | Enable adaptive PD scheduling |
| `--scheduling-policy` | `"load-balance"` | Must be set to `slo` for adaptive PD |
| `--tpot-slo` | `50` | TPOT SLO target (ms) |
| `--tpot-slo-dispatch-threshold` | `0.85` | Fraction of TPOT SLO used as dispatch/destination filter threshold |
| `--tpot-migrate-out-ceil-threshold` | `0.95` | Fraction of TPOT SLO above which mitigating rescheduling triggers (source filter) |
| `--tpot-migrate-out-floor-threshold` | `0.60` | Fraction of TPOT SLO below which consolidating rescheduling triggers (source filter) |
| `--rescheduling-policies` | `"binpacking_mitigation,binpacking_consolidation"` | Rescheduling policies for adaptive PD|
| `--rescheduling-interval-ms` | `500` | Interval between rescheduling iterations (use `100` for adaptive PD) |

> **Note**: For details, see [Adaptive PD Scheduling](./adaptive_pd_scheduling.md).

### Failover Configuration

| Flag | Default | Description |
|------|---------|-------------|
| `--failover-domain` | `"instance"` | Failure domain: `instance`, `node`, `instance-unit`, `node-unit` |
| `--instance-staleness-seconds` | `60` | Time after which an instance is considered stale |

### Migration Request Configuration

| Flag | Default | Description |
|------|---------|-------------|
| `--rescheduling-req-select-rule` | `"TOKEN"` | Migration request selection rule: `NUM_REQ`, `TOKEN`, `RATIO` |
| `--rescheduling-req-select-order` | `"SR"` | Migration request selection order: `LCR`, `FCR`, `LR`, `SR`, `FCW`, `FCWSR` |
| `--rescheduling-req-select-value` | `1024` | Number of requests/tokens or KV cache ratio to migrate |

### gRPC Configuration

| Flag | Default | Description |
|------|---------|-------------|
| `--llumlet-grpc-connection-pool-size` | `10` | Size of gRPC connection pool per instance |
| `--llumlet-grpc-timeout-seconds` | `5` | Timeout for gRPC migration calls |

---

## Deployment Modes

| Mode | Flag | Process |
|------|------|---------|
| Colocated | `--colocated-rescheduling-mode=true` | Inside scheduler process |
| Standalone | `--standalone-rescheduling-mode=true` | Separate process |
