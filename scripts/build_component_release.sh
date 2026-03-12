#!/bin/bash
set -e

TARGET=""
PUSH_IMAGE=false
CUSTOM_REPOSITORY=""
CUSTOM_TAG=""
BASE_IMAGE=""

while [[ "$#" -gt 0 ]]; do
    case $1 in
        gateway|scheduler|discovery) TARGET=$1 ;;
        --push) PUSH_IMAGE=true ;;
        --repository) CUSTOM_REPOSITORY="$2"; shift ;;
        --tag) CUSTOM_TAG="$2"; shift ;;
        --base-image) BASE_IMAGE="$2"; shift ;;
        *) echo "Unknown parameter: $1"; echo "Usage: $0 [gateway|scheduler|discovery] [--push] [--repository <your-registry>/<your-repo>] [--tag <image-tag>] [--base-image <base-image>]"; exit 1 ;;
    esac
    shift
done

DEFAULT_REPOSITORY="llumnix-registry.cn-beijing.cr.aliyuncs.com/llumnix/${TARGET}"
TIMESTAMP=$(date +"%Y%m%d-%H%M%S")
IMAGE_TAG="${CUSTOM_TAG:-${TIMESTAMP}}"

if [ -n "$CUSTOM_REPOSITORY" ]; then
    REPOSITORY="$CUSTOM_REPOSITORY"
else
    REPOSITORY="$DEFAULT_REPOSITORY"
fi

BUILD_ARGS=""
if [ -n "$BASE_IMAGE" ]; then
    BUILD_ARGS="--build-arg BASE_IMAGE=${BASE_IMAGE}"
fi

echo "Building ${TARGET} image..."

DOCKER_BUILDKIT=1 docker build \
    --network=host \
    ${BUILD_ARGS} \
    -t ${REPOSITORY}:${IMAGE_TAG} \
    -f ./container/Dockerfile.${TARGET} \
    .

echo "✓ Build completed: ${REPOSITORY}:${IMAGE_TAG}"

if [ "$PUSH_IMAGE" = true ]; then
    echo "Pushing image..."
    docker push ${REPOSITORY}:${IMAGE_TAG}
    echo "✓ Push completed: ${REPOSITORY}:${IMAGE_TAG}"
fi
