#!/bin/bash
set -e

IMAGE="beijing-pooling-registry-vpc.cn-beijing.cr.aliyuncs.com/llumnix/llumnix-dev:llumnix-vllm-dev-latest"

echo "Building wheel package..."

docker run --rm \
  -v "$(pwd):/workspace" \
  -w /workspace \
  "$IMAGE" \
  bash -c "cd ./python/llumnix && rm -rf dist && python3 setup.py bdist_wheel"

echo "✓ Build completed"
echo "Generated wheel package: $(ls -1 ./python/llumnix/dist/*.whl)"
