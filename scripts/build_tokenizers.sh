#!/bin/bash
set -e

IMAGE="beijing-pooling-registry.cn-beijing.cr.aliyuncs.com/llumnix/llumnix-dev:llumnix-vllm-dev-20260204-140225"

echo "Building tokenizers package..."

docker run --rm \
  -v "$(pwd):/workspace" \
  -w /workspace \
  "$IMAGE" \
  bash -c "cd ./lib/sgl-model-gateway/sgl-model-gateway/bindings/golang && make build"

echo "✓ Build completed"
echo "Generated tokenizers package: $(ls -1 ./lib/sgl-model-gateway/sgl-model-gateway/bindings/golang/target/release/*.a)"
