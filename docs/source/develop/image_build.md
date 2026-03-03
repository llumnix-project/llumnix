# Build Images

Llumnix provides pre-built images for all components. You can skip this section if you are using the 
pre-built images. If you need to build images manually (e.g., for custom modifications or private 
registry deployment), follow the steps below.

## Prerequisites

- Docker installed and running
- Access to the target image registry
- Configure registry credentials:

```bash
export ALIYUN_DOCKER_USERNAME=<your-username>
export ALIYUN_DOCKER_PASSWORD=<your-password>
```

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

# Build and push gateway image
bash scripts/build_component_release.sh gateway --push
```

### Step 3: Build Scheduler

```bash
# Build scheduler binary
bash scripts/build_component_bin.sh scheduler

# Build and push scheduler image
bash scripts/build_component_release.sh scheduler --push
```

### Step 4: Build Discovery

```bash
# Build discovery python wheel
bash scripts/build_discovery_whl.sh

# Build and push discovery image
bash scripts/build_component_release.sh discovery --push
```

### Step 5: Build LLM Backend (vLLM)

```bash
# Build llumnix python wheel
bash scripts/build_llumnix_whl.sh

# Build and push vLLM image
bash scripts/build_vllm_release.sh --push
```

## Notes

- Remove `--push` flag if you only want to build locally without pushing to registry
- After building custom images, update the image references in your kustomize configuration 
  before deploying with `group_deploy.sh`
