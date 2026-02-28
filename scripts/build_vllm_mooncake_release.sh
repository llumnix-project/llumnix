#!/bin/bash

set -e

PUSH_IMAGE=false
while [[ "$#" -gt 0 ]]; do
    case $1 in
        --push) PUSH_IMAGE=true ;;
        *) echo "Unknown parameter: $1"; exit 1 ;;
    esac
    shift
done

REPOSITORY="llumnix-registry.cn-beijing.cr.aliyuncs.com/llumnix/vllm"
TIMESTAMP=$(date +"%Y%m%d-%H%M%S")
IMAGE_TAG="mooncake-${TIMESTAMP}"
BASE_IMAGE="vllm/vllm-openai:v0.12.0"

DOCKER_BUILDKIT=1 docker build \
    --network=host \
    --build-arg BASE_IMAGE=${BASE_IMAGE} \
    -t ${REPOSITORY}:${IMAGE_TAG} \
    -f ./container/Dockerfile.vllm_mooncake \
    .

echo "Build completed: ${REPOSITORY}:${IMAGE_TAG}"

if [ "$PUSH_IMAGE" = true ]; then
    echo "Pushing image..."
    docker push ${REPOSITORY}:${IMAGE_TAG}
    echo "Push completed: ${REPOSITORY}:${IMAGE_TAG}"
fi
