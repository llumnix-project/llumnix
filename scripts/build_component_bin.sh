#!/bin/bash
set -e

TARGET=""

while [[ "$#" -gt 0 ]]; do
    case $1 in
        gateway|scheduler) TARGET=$1 ;;
        *) echo "Unknown target: $1"; echo "Usage: $0 [gateway|scheduler]"; exit 1 ;;
    esac
    shift
done

IMAGE="beijing-pooling-registry-vpc.cn-beijing.cr.aliyuncs.com/llumnix/llumnix-dev:llumnix-vllm-dev-20260130-105003"

echo "Building ${TARGET} binary..."

docker run --rm \
  -v "$(pwd):/workspace" \
  -w /workspace \
  "$IMAGE" \
  bash -c "go mod tidy && make ${TARGET}-build"

echo "✓ Build completed"
echo "Generated llm-${TARGET} binary package: $(ls -1 ./bin/${TARGET}*)"
