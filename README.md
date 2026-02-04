# Development Guide

`beijing-pooling-registry-vpc.cn-beijing.cr.aliyuncs.com/llumnix/llumnix-dev:llumnix-vllm-dev-20260204-140225` is recommended for development. Then, you should run the following commands to set up the environment:

```bash
go mod tidy

# install patched vllm
make vllm-install

# install llumlet package
make llumlet-install

# install discovery package
make discovery-install

# build lib-tokenizers
make lib-tokenizers-build

# build blade-kvt
make blade-kvt-install
```

Run `make gateway-build` to build the gateway binary and `make scheduler-build` to build the scheduler binary. And `make e2e-test` is used to run all end-to-end tests. Please refer to [tests/local/utils.py](tests/local/utils.py) for the details of launching commands. `make unit-test` is used to run all go unit tests.

# How to deploy

```bash

# --------- build release images ----------
# build lib-tokenizers, only need run once
bash scripts/build_tokenizers.sh

# build gateway and scheduler bin and release
bash scripts/build_component_bin.sh gateway
bash scripts/build_component_release.sh gateway --push

bash scripts/build_component_bin.sh scheduler
bash scripts/build_component_release.sh scheduler --push

# build discovery
bash scripts/build_discovery_whl.sh
bash scripts/build_discovery_release.sh --push

# build llm backend
bash scripts/build_llumnix_whl.sh
bash scripts/scripts/build_vllm_release.sh --push

# -------- deploy in k8s --------

# deploy, $DEPLOY_MODE is the mode to deploy, can be "pb" or "normal"
bash group_deploy.sh $NAMESPACE $DEPLOY_MODE

# delete
bash group_delete.sh $NAMESPACE
```
