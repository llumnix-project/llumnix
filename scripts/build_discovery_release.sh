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

REPOSITORY="beijing-pooling-registry.cn-beijing.cr.aliyuncs.com/llumnix/llumnix-dev"
TIMESTAMP=$(date +"%Y%m%d-%H%M%S")
IMAGE_TAG="discovery-${TIMESTAMP}"

DOCKER_BUILDKIT=1 docker build \
    -t ${REPOSITORY}:${IMAGE_TAG} \
    -f ./container/Dockerfile.discovery \
    .

echo "Build completed: ${REPOSITORY}:${IMAGE_TAG}"

if [ "$PUSH_IMAGE" = true ]; then
    echo "Pushing image..."
    docker push ${REPOSITORY}:${IMAGE_TAG}
    echo "Push completed: ${REPOSITORY}:${IMAGE_TAG}"
fi
