# Development Guide

`beijing-pooling-registry-vpc.cn-beijing.cr.aliyuncs.com/llumnix/llumnix-dev:llumnix-vllm-dev-20260128-150632` is recommended for development. Then, you should run the following commands to set up the environment:

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
```

Run `make gateway-build` to build the gateway binary. 

Please refer to [tests/local/utils.py](tests/local/utils.py) for the details of launching commands.

And `make e2e-tests` is used to run all tests.

# How to deploy

```bash
cd deploy

# deploy
bash group_deploy.sh $NAMESPACE $DEPLOY_MODE

# delete (if hang, please CTRL+C and re-run the delete command)
bash group_delete.sh $NAMESPACE
```

kubectl get pods -n $NAMESPACE

kubectl logs 

kubectl describe pods
