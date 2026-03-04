#!/bin/bash
set -e
source "$(dirname "$0")/lib/common.sh"

TARGET=""
CUSTOM_IMAGE=""

while [[ "$#" -gt 0 ]]; do
    case $1 in
        gateway|scheduler) TARGET=$1 ;;
        --image) CUSTOM_IMAGE="$2"; shift ;;
        *) echo "Unknown target: $1"; echo "Usage: $0 [gateway|scheduler] [--image <dev-image>]"; exit 1 ;;
    esac
    shift
done

DEFAULT_IMAGE="llumnix-registry.cn-beijing.cr.aliyuncs.com/llumnix/vllm:dev-20260204-140225"
IMAGE="${CUSTOM_IMAGE:-${DEFAULT_IMAGE}}"

echo "Building ${TARGET} binary..."

docker run --rm \
  --network host \
  -v "$(pwd):/workspace" \
  -w /workspace \
  "$IMAGE" \
  bash -c "go mod tidy && make ${TARGET}-build"

echo "✓ Build completed"
echo "Generated llm-${TARGET} binary package: $(ls -1 ./bin/${TARGET}*)"
