#!/bin/bash
set -e

CUSTOM_IMAGE=""

while [[ "$#" -gt 0 ]]; do
    case $1 in
        --image) CUSTOM_IMAGE="$2"; shift ;;
        *) echo "Unknown parameter: $1"; echo "Usage: $0 [--image <dev-image>]"; exit 1 ;;
    esac
    shift
done

DEFAULT_IMAGE="llumnix-registry.cn-beijing.cr.aliyuncs.com/llumnix/vllm:dev-20260204-140225"
IMAGE="${CUSTOM_IMAGE:-${DEFAULT_IMAGE}}"

echo "Building wheel package..."

docker run --rm \
  --network host \
  -v "$(pwd):/workspace" \
  -w /workspace \
  "$IMAGE" \
  bash -c "cd ./python/discovery && rm -rf dist && pip install -r requirements.txt && python3 setup.py bdist_wheel"

echo "✓ Build completed"
echo "Generated wheel package: $(ls -1 ./python/discovery/dist/*.whl)"
