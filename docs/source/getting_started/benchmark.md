# Benchmark Guide

Use `benchmarks/run_benchmark.sh` to run a throughput/latency benchmark against a deployed Llumnix gateway. The script submits a Kubernetes Job, waits for the pod to be scheduled, and prints the commands needed to retrieve results.

## Quick Start

```bash
cd benchmarks/
./run_benchmark.sh -n <namespace>

# Example
./run_benchmark.sh -n llumnix
```

By default the script runs:

```
vllm bench serve \
  --base-url http://gateway:8089 \
  --model Qwen/Qwen2.5-7B \
  --num-prompts 100 \
  --ready-check-timeout-sec 0 \
  --request-rate 5 \
  --save-result \
  --save-detailed \
  --result-dir /tmp/benchmark-result
```

## Options

| Option | Description | Default |
|---|---|---|
| `-n`, `--namespace` | Target Kubernetes namespace (required) | — |
| `-i`, `--image` | Benchmark container image | `llumnix-registry.cn-beijing.cr.aliyuncs.com/llumnix/vllm:20260130-105854` |
| `-j`, `--job-name` | Kubernetes Job name | `llumnix-benchmark` |
| `-c`, `--command` | Full benchmark command to run inside the pod | see above |
| `-t`, `--ttl` | Seconds before the job is auto-deleted after failure | `3600` |

## Retrieving Results

After the benchmark succeeds, the pod sleeps indefinitely so you can copy results at any time. The script prints the exact command with the actual pod name, for example:

```bash
# Copy the entire result directory to local
kubectl cp llumnix-benchmark-fxfx9:/tmp/benchmark-result ./llumnix-benchmark-fxfx9 -n llumnix

# Delete the job once you are done
kubectl delete job llumnix-benchmark -n llumnix
```

The result directory (`/tmp/benchmark-result/`) contains:

| File | Description |
|---|---|
| `benchmark-log.txt` | Full stdout/stderr of the benchmark run |
| `*.json` | Result files produced by `--save-result` / `--save-detailed` |

> On failure the pod exits immediately and the job is auto-deleted after `--ttl` seconds.

## Custom Commands

Pass a custom benchmark command with `-c`. If you use `--save-result` or `--save-detailed`, always set `--result-dir /tmp/benchmark-result` so the output files are placed alongside the log and included in the `kubectl cp`:

```bash
./run_benchmark.sh -n llumnix \
  --command "vllm bench serve \
    --base-url http://gateway:8089 \
    --model Qwen/Qwen2.5-7B \
    --num-prompts 500 \
    --request-rate 10 \
    --save-result \
    --save-detailed \
    --result-dir /tmp/benchmark-result"
```

## Useful Commands

```bash
# Check job status
kubectl get job llumnix-benchmark -n llumnix

# Follow live logs
kubectl logs -f job/llumnix-benchmark -n llumnix

# Delete job manually
kubectl delete job llumnix-benchmark -n llumnix
```
