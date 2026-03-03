#!/bin/bash

set -e

show_usage() {
    cat << EOF
Usage: $0 [options]

Options:
  -n, --namespace      Target Kubernetes namespace for benchmark job (required)
  -i, --image          Benchmark image (default: llumnix-registry.cn-beijing.cr.aliyuncs.com/llumnix/vllm:20260130-105854)
  -j, --job-name       Job name (default: llumnix-benchmark)
  -c, --command        Benchmark command (default: "vllm bench serve --base-url http://gateway:8089 --model Qwen/Qwen2.5-7B --num-prompts 100 --request-rate 5 --save-result --save-detailed --result-dir /tmp/benchmark-result")
  -t, --ttl            Seconds before job is auto-deleted after completion/failure (default: 3600)
  -h, --help           Show this help

Examples:
  # Run benchmark in namespace 'llumnix' with default settings
  $0 --namespace llumnix
  $0 -n llumnix

  # Run benchmark with custom command
  # Note: if using --save-result or --save-detailed, always set --result-dir /tmp/benchmark-result
  $0 -n llumnix --command "vllm bench serve --base-url http://gateway:8089 --model Qwen/Qwen2.5-7B --num-prompts 200 --request-rate 5 --save-result --save-detailed --result-dir /tmp/benchmark-result"

  # Run benchmark with a 2-hour ttl for auto-cleanup after failure
  $0 -n llumnix --ttl 7200

  # Run benchmark with custom image and job name
  $0 -n llumnix --image llumnix-registry.cn-beijing.cr.aliyuncs.com/llumnix/vllm:latest --job-name my-benchmark
EOF
}

# Default values
NAMESPACE=""
IMAGE="llumnix-registry.cn-beijing.cr.aliyuncs.com/llumnix/vllm:20260130-105854"
JOB_NAME="llumnix-benchmark"
BENCHMARK_CMD="vllm bench serve --base-url http://gateway:8089 --model Qwen/Qwen2.5-7B --num-prompts 100 --ready-check-timeout-sec 0 --request-rate 5 --save-result --save-detailed --result-dir /tmp/benchmark-result"
TTL=3600

# Parse options
while [[ $# -gt 0 ]]; do
    case $1 in
        -n|--namespace)
            NAMESPACE="$2"
            shift 2
            ;;
        -i|--image)
            IMAGE="$2"
            shift 2
            ;;
        -j|--job-name)
            JOB_NAME="$2"
            shift 2
            ;;
        -c|--command)
            BENCHMARK_CMD="$2"
            shift 2
            ;;
        -t|--ttl)
            TTL="$2"
            shift 2
            ;;
        -h|--help)
            show_usage
            exit 0
            ;;
        *)
            echo "Error: Unknown option $1" >&2
            show_usage
            exit 1
            ;;
    esac
done

# Validate required arguments
if [ -z "$NAMESPACE" ]; then
    echo "Error: -n/--namespace is required" >&2
    echo "" >&2
    show_usage
    exit 1
fi

# Check namespace exists 
if ! kubectl get namespace "$NAMESPACE" &>/dev/null; then
    echo "Error: Namespace $NAMESPACE does not exist" >&2
    exit 1
fi

# Delete existing benchmark job if present
echo "Cleaning up existing benchmark job (if any)..."
kubectl delete job "$JOB_NAME" -n "$NAMESPACE" --ignore-not-found=true

# Wait for old pods to fully terminate before proceeding
echo "Waiting for old pods to terminate..."
kubectl wait --for=delete pod -l job-name="$JOB_NAME" -n "$NAMESPACE" --timeout=60s 2>/dev/null || true

# Locate the benchmark YAML template
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BENCHMARK_YAML="${SCRIPT_DIR}/../deploy/benchmark/benchmark.yaml"

if [ ! -f "$BENCHMARK_YAML" ]; then
    echo "Error: Benchmark YAML template not found at $BENCHMARK_YAML" >&2
    exit 1
fi

# Create benchmark job from template
echo "Creating benchmark job: $JOB_NAME in namespace: $NAMESPACE"
sed -e "s|BENCHMARK_JOB_NAME|${JOB_NAME}|g" \
    -e "s|BENCHMARK_IMAGE|${IMAGE}|g" \
    -e "s|BENCHMARK_COMMAND|${BENCHMARK_CMD}|g" \
    -e "s|BENCHMARK_TTL_SECONDS|${TTL}|g" \
    -e "s|BENCHMARK_NAMESPACE|${NAMESPACE}|g" \
    "$BENCHMARK_YAML" | kubectl apply -f - -n "$NAMESPACE"

# Wait briefly for the pod to be scheduled so we can show the actual pod name
echo ""
echo "Waiting for pod to be created..."
POD_NAME=""
for i in $(seq 1 30); do
    POD_NAME=$(kubectl get pods -n "$NAMESPACE" -l job-name="$JOB_NAME" \
        -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
    [ -n "$POD_NAME" ] && break
    sleep 2
done

echo ""
echo "Benchmark job submitted successfully!"
echo ""
echo "Useful commands:"
echo "  # View job status"
echo "  kubectl get job $JOB_NAME -n $NAMESPACE"
echo ""
echo "  # Follow live logs (pod sleeps indefinitely after success)"
echo "  kubectl logs -f job/$JOB_NAME -n $NAMESPACE"
echo ""
echo "  # Copy benchmark results while pod is alive (run after benchmark succeeds)"
echo "  kubectl cp $POD_NAME:/tmp/benchmark-result ./$POD_NAME -n $NAMESPACE"
echo ""
echo "  # Delete job (and its pod) after copying results"
echo "  kubectl delete job $JOB_NAME -n $NAMESPACE"
echo ""
echo "On failure: job is auto-deleted after ${TTL}s (ttlSecondsAfterFinished)."
