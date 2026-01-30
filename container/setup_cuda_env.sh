export CUDA_HOME=/usr/local/cuda
export CUDA_PATH=/usr/local/cuda
export PATH=$CUDA_HOME/bin:$PATH
export LD_LIBRARY_PATH=$CUDA_HOME/lib64:$LD_LIBRARY_PATH

if [ -d "${CUDA_PATH}" ]; then
    export CUDA_TOOLKIT_PATH="${CUDA_PATH}"
    export CUDA_BIN="${CUDA_PATH}/bin"
    export NVCC="${CUDA_BIN}/nvcc"
    export CUDA_INC="${CUDA_PATH}/include"
    export CUDA_LIB="${CUDA_PATH}/lib64"
    export CUDA_X86_64_LIB="${CUDA_PATH}/targets/x86_64-linux/lib/"

    export C_INCLUDE_PATH="${C_INCLUDE_PATH}:${CUDA_INC}"
    export LIBRARY_PATH="${LIBRARY_PATH}:${CUDA_LIB}:${CUDA_X86_64_LIB}"
    export LD_LIBRARY_PATH="${LD_LIBRARY_PATH}:${CUDA_LIB}:${CUDA_X86_64_LIB}"
    export PATH="${PATH}:${CUDA_BIN}"

    export CUDA_VERSION="$(nvcc --version | /bin/sed -n 's/^.*release \([0-9]\+\.[0-9]\+\).*$/\1/p')"
    export CUDA_VERSION_NUM="$(echo ${CUDA_VERSION/./})"

    if [ ! -z "${CUDA_VERSION_NUM}" ] && [ ${CUDA_VERSION_NUM} -ge 121 ]; then
        export CUDA_COMPUTE_CAPABILITIES="8.0,8.6,8.9,9.0"
        export TORCH_CUDA_ARCH_LIST="8.0;8.6;8.9;9.0"
    else
        export CUDA_COMPUTE_CAPABILITIES="7.5,8.0,8.6"
        export TORCH_CUDA_ARCH_LIST="7.5;8.0;8.6"
    fi
else
    unset CUDA_PATH
fi
