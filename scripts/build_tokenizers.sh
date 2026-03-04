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

echo "Building tokenizers package..."

docker run --rm \
  --network host \
  -v "$(pwd):/workspace" \
  -e PATH="/root/.cargo/bin:$PATH" \
  -w /workspace \
  "$IMAGE" \
  bash -c "cd ./lib/sgl-model-gateway/sgl-model-gateway/bindings/golang && make build"

echo "✓ Build completed"
echo "Generated tokenizers package: $(ls -1 ./lib/sgl-model-gateway/sgl-model-gateway/bindings/golang/target/release/*.a)"
