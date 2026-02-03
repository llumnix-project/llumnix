#!/bin/bash

set -e

setup_cuda_env() {
    local cuda_home="/usr/local/cuda"
    
    if [ ! -d "$cuda_home" ]; then
        echo "Error: CUDA installation not found at $cuda_home" >&2
        return 1
    fi
    
    export CUDA_HOME="$cuda_home"
    export CUDA_PATH="$cuda_home"
    export CUDA_TOOLKIT_PATH="$cuda_home"
    
    export CUDA_BIN="$cuda_home/bin"
    export NVCC="$CUDA_BIN/nvcc"
    export PATH="$CUDA_BIN:$PATH"
    
    export CUDA_INC="$cuda_home/include"
    export CUDA_LIB="$cuda_home/lib64"
    export CUDA_X86_64_LIB="$cuda_home/targets/x86_64-linux/lib"
    
    export C_INCLUDE_PATH="${C_INCLUDE_PATH}:${CUDA_INC}"
    export LIBRARY_PATH="${LIBRARY_PATH}:${CUDA_LIB}:${CUDA_X86_64_LIB}"
    export LD_LIBRARY_PATH="${LD_LIBRARY_PATH}:${CUDA_LIB}:${CUDA_X86_64_LIB}"
    
    local cuda_version
    cuda_version="$(nvcc --version | sed -n 's/^.*release \([0-9]\+\.[0-9]\+\).*$/\1/p')"
    local cuda_version_num="${cuda_version//./}"
    
    export CUDA_VERSION="$cuda_version"
    export CUDA_VERSION_NUM="$cuda_version_num"
    
    if [ -n "$cuda_version_num" ] && [ "$cuda_version_num" -ge 121 ]; then
        export CUDA_COMPUTE_CAPABILITIES="8.0,8.6,8.9,9.0"
        export TORCH_CUDA_ARCH_LIST="8.0;8.6;8.9;9.0"
    else
        export CUDA_COMPUTE_CAPABILITIES="7.5,8.0,8.6"
        export TORCH_CUDA_ARCH_LIST="7.5;8.0;8.6"
    fi
}

main() {
    local repo_url="http://gitlab.alibaba-inc.com/PAI-LLM/blade-kvt.git"
    local branch="zy-oc"
    local work_dir="/tmp/blade-kvt"

    rm -rf "$work_dir"
    git clone "$repo_url" -b "$branch" "$work_dir"
    
    setup_cuda_env
    
    cd "$work_dir"
    python3 setup.py bdist_wheel
    pip install dist/*.whl
}

main "$@"
