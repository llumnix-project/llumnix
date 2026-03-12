# Build Images

Llumnix provides pre-built images for all components. You can skip this section if you are using the 
pre-built images. If you need to build images manually (e.g., for custom modifications or private 
registry deployment), follow the steps below.

## Prerequisites

- Docker installed and running
- Access to your own image registry
- Repository cloned with submodules initialized:
  ```bash
  git clone https://github.com/llumnix-project/llumnix.git
  cd llumnix
  git submodule update --init --recursive
  ```

## Build Steps


The only strict requirement is that **lib-tokenizers must be built before Gateway**, as Gateway 
depends on it for tokenization. All other components can be built independently in any order.
### Step 1: Build lib-tokenizers

This step applies the sgl-model-gateway patch to the `lib/sglang` submodule and compiles the Rust tokenizer library inside a Docker container.

```bash
bash scripts/build_tokenizers.sh --apply-patch
```

> **Note**: The `--apply-patch` flag applies the `patches/sgl-model-gateway/` patch to the submodule before building. This is required to include Llumnix-specific modifications. The patch only needs to be applied once; subsequent builds can omit this flag.

### Step 2: Build Gateway

```bash
# Build gateway binary
bash scripts/build_component_bin.sh gateway

# Build gateway image
bash scripts/build_component_release.sh gateway
```

### Step 3: Build Scheduler

```bash
# Build scheduler binary
bash scripts/build_component_bin.sh scheduler

# Build scheduler image
bash scripts/build_component_release.sh scheduler
```

### Step 4: Build Discovery

```bash
# Build discovery python wheel
bash scripts/build_discovery_whl.sh

# Build and push discovery image
bash scripts/build_component_release.sh discovery
```

### Step 5: Build LLM Backend (vLLM)

```bash
# Build llumnix python wheel
bash scripts/build_llumnix_whl.sh

# Build standard vLLM image (Neutral / PD mode)
# Image tag: <timestamp>, e.g. 20260101-140000
bash scripts/build_vllm_release.sh

# Build vLLM image with Mooncake support (required for PD-KVS mode)
# Image tag: mooncake-<timestamp>, e.g. mooncake-20260101-140000
bash scripts/build_vllm_release.sh --include_mooncake
```

Optional: use `--push` to push after build, `--repository <your-registry>/vllm` for a custom registry, and `--tag <image-tag>` for a fixed tag (e.g. for CI).

## Push Images to Your Registry

After building, you need to push the images to your own registry before deployment. 
The build scripts do **not** push to the Llumnix registry, as external users do not have write access.
After building, push the image to your own registry:

```bash
bash scripts/build_component_release.sh discovery --push --repository <your-registry>/discovery
```
Replace <your-registry> with your own registry address, for example:

```bash
bash scripts/build_component_release.sh discovery --push --repository my-registry.example.com/discovery
```
> Note: To work correctly with group_deploy.sh, ensure your image repository name matches the project convention (e.g. `<registry>/llumnix/<gateway|scheduler|discovery>`). All build scripts support `--tag <image-tag>` for a custom tag. 
 
## Deploy with Custom Images

Use group_deploy.sh with the --repository flag and specify each component's image tag:

```bash
bash deploy/group_deploy.sh <group-name> <kustomize-dir> \
    --repository <your-registry> \
    --gateway-tag <gateway-tag> \
    --scheduler-tag <scheduler-tag> \
    --vllm-tag <vllm-tag> \
    --discovery-tag <discovery-tag> \
    [--mooncake-vllm-tag <mooncake-vllm-tag>]   # required for PD-KVS when using custom images
```

Example (Neutral / PD mode):

```bash
bash deploy/group_deploy.sh llumnix deploy/neutral/full-mode-scheduling/load-balance \
    --repository my-registry.example.com/my-repo \
    --gateway-tag 20260101-120000 \
    --scheduler-tag 20260101-130000 \
    --vllm-tag 20260101-140000 \
    --discovery-tag 20260101-150000
```

Example (PD-KVS mode with custom Mooncake vLLM image):

```bash
# Build Mooncake vLLM image first: bash scripts/build_vllm_release.sh --include_mooncake
bash deploy/group_deploy.sh llumnix deploy/pd-kvs/full-mode-scheduling/load-balance \
    --repository my-registry.example.com/my-repo \
    --gateway-tag 20260101-120000 \
    --scheduler-tag 20260101-130000 \
    --vllm-tag 20260101-140000 \
    --mooncake-vllm-tag mooncake-20260101-140000 \
    --discovery-tag 20260101-150000
```

