#!/bin/bash
set -e

# Usage function
usage() {
    echo "Usage: $0 [kvt_branch] [kvt_commit]"
    echo "  kvt_branch must be a valid branch name in kvt repo"
    echo "  kvt_commit must be a valid commit hash in kvt repo"
    exit 1
}

# Check parameters
if [ $# -lt 2 ]; then
    usage
fi

KVT_BRANCH="$1"
KVT_COMMIT="$2"

WORK_DIR=$(pwd)

# Clone kvt repo

cd /tmp && rm -rf blade-kvt

REPO_DIR="blade-kvt"
git clone http://"${AONE_USERNAME}":"${AONE_TOKEN}"@gitlab.alibaba-inc.com/PAI-LLM/blade-kvt.git

cd "${REPO_DIR}"
git fetch origin "${KVT_BRANCH}"
git checkout "${KVT_BRANCH}"
git checkout "${KVT_COMMIT}"

# Build kvt wheel
echo "Building kvt wheel"
docker run --rm -v "$(pwd)":/workspace -w /workspace \
    beijing-pooling-registry-vpc.cn-beijing.cr.aliyuncs.com/llumnix/llumnix-dev:main_vllm_202509101027 \
    /bin/bash -c "apt remove -y accl-barex-cuda \
    && wget https://eflops.oss-cn-beijing.aliyuncs.com/accl-barex/accl-barex-pkg-release_v1.5.1-1/mlx/cuda12/cxx11/accl-barex-cuda12-devel-1.5.1-1.deb \
    && dpkg -i accl-barex-cuda12-devel-1.5.1-1.deb \
    && python3 setup.py bdist_wheel"

mkdir -p ${WORK_DIR}/dist/ && mv dist/*.whl ${WORK_DIR}/dist/
