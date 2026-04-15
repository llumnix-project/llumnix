#!/bin/bash
set -euo pipefail

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

DEFAULT_IMAGE="llumnix-registry.cn-beijing.cr.aliyuncs.com/llumnix/vllm:dev-20260326-165612"
IMAGE="${CUSTOM_IMAGE:-${DEFAULT_IMAGE}}"
REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
HOST_GO_BUILD_CACHE="${HOME}/.cache/llumnix/go-build"
HOST_GO_MOD_CACHE="${HOME}/.cache/llumnix/go-mod"
GOPROXY_LIST="https://goproxy.cn|https://goproxy.io|https://proxy.golang.org|direct"

mkdir -p "$HOST_GO_BUILD_CACHE" "$HOST_GO_MOD_CACHE"

echo "Building ${TARGET} binary..."

docker run --rm \
  --network host \
  -e GOPROXY="$GOPROXY_LIST" \
  -e GOCACHE=/root/.cache/go-build \
  -e GOMODCACHE=/root/go/pkg/mod \
  -v "$REPO_ROOT:/workspace" \
  -v "$HOST_GO_BUILD_CACHE:/root/.cache/go-build" \
  -v "$HOST_GO_MOD_CACHE:/root/go/pkg/mod" \
  -w /workspace \
  "$IMAGE" \
  bash -lc "set -euo pipefail && go mod download && make ${TARGET}-build"

echo "✓ Build completed"
echo "Generated llm-${TARGET} binary package: $(ls -1 "$REPO_ROOT"/bin/${TARGET}*)"
