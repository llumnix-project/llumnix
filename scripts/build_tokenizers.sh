#!/bin/bash
set -e

CUSTOM_IMAGE=""
APPLY_PATCH=false

while [[ "$#" -gt 0 ]]; do
    case $1 in
        --image) CUSTOM_IMAGE="$2"; shift ;;
        --apply-patch) APPLY_PATCH=true ;;
        *) echo "Unknown parameter: $1"; echo "Usage: $0 [--image <dev-image>] [--apply-patch]"; exit 1 ;;
    esac
    shift
done

DEFAULT_IMAGE="llumnix-registry.cn-beijing.cr.aliyuncs.com/llumnix/vllm:dev-20260204-140225"
IMAGE="${CUSTOM_IMAGE:-${DEFAULT_IMAGE}}"

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

echo "Building tokenizers package..."

if [ "$APPLY_PATCH" = true ]; then
  echo "Applying sgl-model-gateway patch..."
  PATCH_FILE="$REPO_ROOT/patches/sgl-model-gateway/sgl_model_gateway_820e97d6.patch"
  SGLANG_DIR="$REPO_ROOT/lib/sglang"
  git -C "$SGLANG_DIR" checkout -- .
  git -C "$SGLANG_DIR" clean -fd sgl-model-gateway/bindings/golang/
  git -C "$SGLANG_DIR" apply "$PATCH_FILE"
fi

docker run --rm \
  --network host \
  -v "$REPO_ROOT:/workspace" \
  -e PATH="/root/.cargo/bin:$PATH" \
  -v "$REPO_ROOT:/workspace" \
  -w /workspace \
  "$IMAGE" \
  bash -c ". /root/.cargo/env && cd ./lib/sglang/sgl-model-gateway/bindings/golang && make build"

echo "✓ Build completed"
echo "Generated tokenizers package: $(ls -1 "$REPO_ROOT/lib/sglang/sgl-model-gateway/bindings/golang/target/release/"*.a)"
