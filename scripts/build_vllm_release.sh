#!/bin/bash

set -e

PUSH_IMAGE=false
CUSTOM_REPOSITORY=""

while [[ "$#" -gt 0 ]]; do
    case $1 in
        --push) PUSH_IMAGE=true ;;
        --repository) CUSTOM_REPOSITORY="$2"; shift ;;
        *) echo "Unknown parameter: $1"; echo "Usage: $0 [--push] [--repository <your-registry>/<your-repo>]"; exit 1 ;;
    esac
    shift
done

DEFAULT_REPOSITORY="llumnix-registry.cn-beijing.cr.aliyuncs.com/llumnix/vllm"
TIMESTAMP=$(date +"%Y%m%d-%H%M%S")
IMAGE_TAG="${TIMESTAMP}"
BASE_IMAGE="vllm/vllm-openai:v0.12.0"

if [ -n "$CUSTOM_REPOSITORY" ]; then
    REPOSITORY="$CUSTOM_REPOSITORY"
else
    REPOSITORY="$DEFAULT_REPOSITORY"
fi

DOCKER_BUILDKIT=1 docker build \
    --network=host \
    --build-arg BASE_IMAGE=${BASE_IMAGE} \
    -t ${REPOSITORY}:${IMAGE_TAG} \
    -f ./container/Dockerfile.vllm \
    .

echo "Build completed: ${REPOSITORY}:${IMAGE_TAG}"

if [ "$PUSH_IMAGE" = true ]; then
    echo "Pushing image..."
    docker push ${REPOSITORY}:${IMAGE_TAG}
    echo "✓ Push completed: ${REPOSITORY}:${IMAGE_TAG}"
fi
