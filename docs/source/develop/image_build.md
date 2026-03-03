# Build Images

Llumnix provides pre-built images for all components. You can skip this section if you are using the 
pre-built images. If you need to build images manually (e.g., for custom modifications or private 
registry deployment), follow the steps below.

## Prerequisites

- Docker installed and running
- Access to your own image registry

## Build Steps


The only strict requirement is that **lib-tokenizers must be built before Gateway**, as Gateway 
depends on it for tokenization. All other components can be built independently in any order.
### Step 1: Build lib-tokenizers

```bash
bash scripts/build_tokenizers.sh
```

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

# Build vLLM image
bash scripts/build_vllm_release.sh
```

## Push Images to Your Registry

After building, you need to push the images to your own registry before deployment. 
The build scripts do **not** push to the Llumnix registry, as external users do not have write access.
After building, push the image to your own registry:

```bash
bash scripts/build_discovery_release.sh --push --repository <your-registry>/<your-repo>
```
Replace <your-registry>/<your-repo> with your own registry address, for example:

```bash
bash scripts/build_discovery_release.sh --push --repository my-registry.example.com/llumnix
```
## Deploy with Custom Images

Use group_deploy.sh with the --repository flag and specify each component's image tag:

```bash
bash deploy/group_deploy.sh <group-name> <kustomize-dir> \
    --repository <your-registry>/<your-repo> \
    --gateway-tag <gateway-tag> \
    --scheduler-tag <scheduler-tag> \
    --vllm-tag <vllm-tag> \
    --discovery-tag <discovery-tag>
```
Example:

```bash
bash deploy/group_deploy.sh llumnix deploy/normal/full-mode-scheduling/load-balance \
    --repository my-registry.example.com/my-repo \
    --gateway-tag gateway-20260101-120000 \
    --scheduler-tag scheduler-20260101-130000 \
    --vllm-tag 20260101-140000 \
    --discovery-tag discovery-20260101-150000
```

