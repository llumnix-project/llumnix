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

DEFAULT_REPOSITORY="beijing-pooling-registry.cn-beijing.cr.aliyuncs.com/llumnix/llumnix-dev"
TIMESTAMP=$(date +"%Y%m%d-%H%M%S")
IMAGE_TAG="discovery-${TIMESTAMP}"

if [ -n "$CUSTOM_REPOSITORY" ]; then
    REPOSITORY="$CUSTOM_REPOSITORY"
else
    REPOSITORY="$DEFAULT_REPOSITORY"
fi

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
