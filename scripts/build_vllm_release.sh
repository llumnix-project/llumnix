#!/bin/bash
# Build vLLM release image. Use --include_mooncake (or --mooncake) for Mooncake-enabled image.

set -e

PUSH_IMAGE=false
CUSTOM_REPOSITORY=""
CUSTOM_TAG=""
INCLUDE_MOONCAKE=false

while [[ "$#" -gt 0 ]]; do
    case $1 in
        --push)             PUSH_IMAGE=true ;;
        --repository)       CUSTOM_REPOSITORY="$2"; shift ;;
        --tag)              CUSTOM_TAG="$2"; shift ;;
        --include_mooncake|--mooncake) INCLUDE_MOONCAKE=true ;;
        *)
            echo "Unknown parameter: $1"
            echo "Usage: $0 [--push] [--repository <your-registry>/vllm] [--tag <image-tag>] [--include_mooncake|--mooncake]"
            exit 1 ;;
    esac
    shift
done

DEFAULT_REPOSITORY="llumnix-registry.cn-beijing.cr.aliyuncs.com/llumnix/vllm"
REPOSITORY="${CUSTOM_REPOSITORY:-$DEFAULT_REPOSITORY}"
TIMESTAMP=$(date +"%Y%m%d-%H%M%S")
BASE_IMAGE="vllm/vllm-openai:v0.12.0"

if [ -n "$CUSTOM_TAG" ]; then
    IMAGE_TAG="${CUSTOM_TAG}"
elif [ "$INCLUDE_MOONCAKE" = "true" ]; then
    IMAGE_TAG="mooncake-${TIMESTAMP}"
else
    IMAGE_TAG="${TIMESTAMP}"
fi

echo "Building vllm image (mooncake=${INCLUDE_MOONCAKE})..."

DOCKER_BUILDKIT=1 docker build \
    --network=host \
    --build-arg BASE_IMAGE=${BASE_IMAGE} \
    --build-arg INCLUDE_MOONCAKE=${INCLUDE_MOONCAKE} \
    -t ${REPOSITORY}:${IMAGE_TAG} \
    -f ./container/Dockerfile.vllm \
    .

echo "✓ Build completed: ${REPOSITORY}:${IMAGE_TAG}"

if [ "$PUSH_IMAGE" = true ]; then
    echo "Pushing image..."
    docker push ${REPOSITORY}:${IMAGE_TAG}
    echo "✓ Push completed: ${REPOSITORY}:${IMAGE_TAG}"
fi
