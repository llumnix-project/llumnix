#!/bin/bash
set -e

IMAGE="beijing-pooling-registry-vpc.cn-beijing.cr.aliyuncs.com/llumnix/llumnix-dev:llumnix-vllm-dev-latest"

echo "Building llm-gateway binary..."

docker run --rm \
  -v "$(pwd):/workspace" \
  -w /workspace \
  "$IMAGE" \
  bash -c "make llm-gateway-build"

echo "✓ Build completed"
echo "Generated llm-gateway binary package: $(ls -1 ./bin/llm-gateway*)"

