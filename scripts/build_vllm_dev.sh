#!/bin/bash

set -e

PUSH_IMAGE=false
CUSTOM_TAG=""
BASE_IMAGE="vllm/vllm-openai:v0.18.0"

while [[ "$#" -gt 0 ]]; do
    case $1 in
        --push) PUSH_IMAGE=true ;;
        --tag) CUSTOM_TAG="$2"; shift ;;
        --base-image) BASE_IMAGE="$2"; shift ;;
        *) echo "Unknown parameter: $1"; echo "Usage: $0 [--push] [--tag <image-tag>] [--base-image <base-image>]"; exit 1 ;;
    esac
    shift
done

REPOSITORY="llumnix-registry.cn-beijing.cr.aliyuncs.com/llumnix/vllm"
TIMESTAMP=$(date +"%Y%m%d-%H%M%S")
IMAGE_TAG="${CUSTOM_TAG:-dev-${TIMESTAMP}}"

DOCKER_BUILDKIT=1 docker build \
    --no-cache \
    --network=host \
    --build-arg BASE_IMAGE=${BASE_IMAGE} \
    -t ${REPOSITORY}:${IMAGE_TAG} \
    -f ./container/Dockerfile.vllm_dev \
    .

echo "Build completed: ${REPOSITORY}:${IMAGE_TAG}"

if [ "$PUSH_IMAGE" = true ]; then
    echo "Pushing image..."
    docker push ${REPOSITORY}:${IMAGE_TAG}
    echo "Push completed: ${REPOSITORY}:${IMAGE_TAG}"
fi
