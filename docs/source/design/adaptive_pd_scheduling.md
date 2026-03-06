# Adaptive PD Scheduling

## Introduction

In PD (Prefill-Decode) disaggregated serving, instances are statically partitioned into prefill (P) and decode (D) roles. This static assignment is inherently vulnerable to instantaneous load fluctuations: when prefill traffic surges, P instances become bottlenecked while D instances sit idle, and vice versa. Adaptive PD scheduling dynamically adjusts the number of P and D instances at runtime based on real-time workload characteristics to maximize SLO attainment.

---

## Prerequisites

- All instances must be capable of serving both prefill and decode requests.
- Prefill and decode operations can be distributed across any two instances, or co-located on a single instance.

---

## Overview

Adaptive PD maximizes SLO guarantees by dynamically adjusting the number of prefill and decode instances at the scheduling layer — **without any instance scaling or redeployment**. From the scheduler's perspective, one dedicated instance is always reserved for prefill and one for decode. All remaining instances are assigned roles at runtime to best accommodate the current workload.

![adaptive_pd_architecture](../image/adaptive_pd_architecture.png)

Since decode is memory-bound, over-provisioning decode instances yields diminishing throughput gains while simultaneously reducing the number of available prefill instances — causing TTFT latency to grow linearly as P-instance count shrinks. Adaptive PD employs a bin-packing strategy for decode scheduling: decode requests are consolidated onto the minimum number of instances required to satisfy the TPOT SLO, maximizing per-instance utilization and freeing as many instances as possible for prefill.

---

## Reserve State Management

All instances are initially role-less. Upon engine launch, the scheduler randomly designates one instance as the prefill reserve and another as the decode reserve.

Reserved instances are given scheduling priority:

- Prefill scheduling: The prefill reserve receives priority when two instances have equal predicted TTFT values.
- Decode scheduling: The decode reserve is preferentially selected to maximize utilization while remaining within the TPOT SLO.

Since reserved instances may crash or become unavailable, the scheduler attempts to re-elect reserve instances. Instances that only contain prefill or decode requests are considered as candidates first. If no such instance exists, the instances with the fewest prefill tokens and fewest active decode requests are selected as reserves.

---

## Scheduling Strategy

Decode Instance Selection
The scheduler selects the instance with **the highest predicted TPOT that still satisfies the TPOT SLO** (bin-packing). If no such instance exists, the scheduler attempts to convert a prefill-only instance (excluding the prefill reserve) with the lowest predicted TTFT to serve as a decode instance. If no instance can satisfy the TPOT SLO under any assignment, the scheduler falls back to load balancing across all decode instances.

Prefill Instance Selection
The scheduler selects the instance with the lowest predicted TTFT among instances with no active decode workload. Because one instance is always reserved for prefill, there is always at least one eligible candidate.

Integration with SLO Scheduling Policy
Adaptive PD integrates with the SLO scheduling policy. Unlike the standard SLO policy — which rejects requests that cannot meet SLO targets — Adaptive PD always returns an instance. It dynamically reassigns instance roles to satisfy SLOs whenever possible, and degrades gracefully when SLOs cannot be met.

---

## Rescheduling Strategy

As decode scheduling uses bin-packing, two scenarios need to be handled:
- Overload: As active decode tokens accumulate, predicted TPOT may rise and breach the TPOT SLO.
- Underload: As decode requests complete, some instances may become underutilized and eligible for consolidation.

For adaptive PD related rescheduling, only decode requests will be migrated and at most one migration pair for each rescheduling policy is generated during each rescheduling cycle. To address the problem described above, two rescheduling policies are designed: `binpacking_mitigation` and `binpacking_consolidation`.

`binpacking_mitigation` migrates requests from overloaded instances to underutilized ones based on predicted TPOT, aiming to restore TPOT compliance through bin-packing consolidation. It pairs the most overloaded source instance with the most loaded destination instance whose predicted TPOT is below the TPOT SLO. This strategy prioritizes relieving the highest-pressure instance while efficiently utilizing spare capacity on destination instances.

`binpacking_consolidation` migrates requests from underutilized instances to more heavily loaded ones, improving resource utilization by packing workloads more densely. It pairs the least loaded source instance with the most loaded destination instance. This strategy empties the lightest instances, progressively freeing them for reassignment to prefill.

---

## Configuration

| Flag | Setting | Description |
|------|---------|-------------|
| `--enable-full-mode-scheduling` | `true` | Requires full-mode scheduling. |
| `--scheduling-policy` | `slo` | Must be set to `slo` for adaptive PD. |
| `--enable-adaptive-pd` | `true` | Enable adaptive PD scheduling. |
| `--tpot-slo` | `50` | TPOT SLO target (ms). |
| `--tpot-slo-dispatch-threshold` | `0.85` | Fraction of TPOT SLO used as the dispatch filter threshold. |
| `--colocated-reschedule-mode` | `true` | Enable colocated reschedule mode. (standalone rescheduling is also supported). |
| `--reschedule-interval-ms` | `100` | Reschedule interval. |
| `--reschedule-policies` | `binpacking_mitigation,binpacking_consolidation` | Reschedule policies. |
| `--tpot-migrate-out-ceil-threshold` | `0.95` | Fraction of TPOT SLO above which overload rescheduling triggers. |
| `--tpot-migrate-out-floor-threshold` | `0.60` | Fraction of TPOT SLO below which underload rescheduling triggers. |
| `--enable-instance-status-local-account`(Optional) | `true` | Enable instance status local account. |

---

## Performance

Setup: Qwen3-32B, TP=1, evaluated on the Azure LLM Inference Trace (Conversational) dataset, deployed on 8 vLLM instances.

SLO targets: TPOT ≤ 50 ms, TTFT ≤ 6000 ms.

Adaptive PD is compared against a load balancing scheduling policy based on the number of prefill and decode tokens.

![adaptive_pd_slo_attainment](../image/adaptive_pd_slo_attainment.png)

![adaptive_pd_instance_status_distribution](../image/adaptive_pd_instance_status_distribution.png)

With a static assignment of 3D / 5P, the decode side is adequately handled, but the prefill instances are severely overloaded. Changing to 2D / 6P alleviates prefill pressure, but causes a significant drop in TPOT SLO attainment.

Adaptive PD overcomes this rigidity by continuously adjusting the effective number of P and D instances in response to real-time workload dynamics, as illustrated in the instance status distribution figure above. This enables a substantially high degree of SLO attainment across both TTFT and TPOT dimensions simultaneously — a level of balance that no static P/D assignment can consistently achieve.

## Future Work

Threshold auto-tuning: The parameters tpot-slo-dispatch-threshold, tpot-migrate-out-ceil-threshold, and tpot-migrate-out-floor-threshold currently require manual tuning. Investigating automated or adaptive methods to set these thresholds based on workload characteristics is a promising direction.

Multi-modal inference support: Exploring how Adaptive PD can be extended to support multi-modal inference scenarios in the context of EPD (Encode-Prefill-Decode) disaggregation, is an important next step.
