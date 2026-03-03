#!/bin/bash
set -e

IMAGE="llumnix-registry.cn-beijing.cr.aliyuncs.com/llumnix/vllm:dev-20260204-140225"

echo "Building wheel package..."

docker run --rm \
  -v "$(pwd):/workspace" \
  -w /workspace \
  "$IMAGE" \
  bash -c "cd ./python/llumnix && rm -rf dist && make vllm_install && python3 setup.py bdist_wheel"

echo "✓ Build completed"
echo "Generated wheel package: $(ls -1 ./python/llumnix/dist/*.whl)"
