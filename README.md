# How to build dev image and release image


## build dev image

python wheel and go binaries are built in the dev image.

```bash
bash scripts/build_vllm_dev_images.sh
```

## build vllm release image

```bash
# build python/llumnix whl
bash scripts/build_vllm_whl.sh

# build vllm release image
bash scripts/build_vllm_release.sh
```

## build tokenizers lib

```bash
bash scripts/build_tokenizers.sh
```

## build gateway binary

```bash
bash scripts/build_gw_bin.sh
```

## build gateway release image

```bash
bash scripts/build_gw_release.sh
```

# How to run test

```bash

CURRENT_DIR=$(pwd)
cd ./python/llumnix && pip install -e .[vllm]

cd $CURRENT_DIR
bash scripts/build_tokenizers.sh
make llm-gateway-build
pytest -x -v -s ./tests/local/vllm_e2e.py # NOTE: check the output
pytest -x -v -s ./tests/local/vllm_pd_e2e.py # NOTE: check the output

```

# How to deploy

update the image name in normal.yaml,prefill.yaml,decode.yaml,gateway.yaml,scheduler.yaml

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
