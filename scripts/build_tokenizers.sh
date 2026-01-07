#!/bin/bash
set -e

IMAGE="beijing-pooling-registry-vpc.cn-beijing.cr.aliyuncs.com/llumnix/llumnix-dev:llumnix-vllm-dev-latest"

echo "Building tokenizers package..."

docker run --rm \
  -v "$(pwd):/workspace" \
  -w /workspace \
  "$IMAGE" \
  bash -c "cd ./lib/tokenizers && make build"

echo "✓ Build completed"
echo "Generated tokenizers package: $(ls -1 ./lib/tokenizers/*.a)"
