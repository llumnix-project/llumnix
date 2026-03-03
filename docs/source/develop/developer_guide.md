# Development Guide

This guide covers setting up a development environment on your local machine.

---

## Development Image

We provide a pre-built development image with all necessary dependencies pre-installed:

```
llumnix-registry.cn-beijing.cr.aliyuncs.com/llumnix/vllm:dev-20260204-140225
```

If you need to build a custom development image, see [Build Development Image](#build-development-image-optional).

---

## Step 1: Clone the Repository

```bash
git clone https://github.com/your-org/llumnix.git
cd llumnix
```

## Step 2: Start the Development Container

```bash
docker run -it --rm \
  --gpus all \
  -v $(pwd):/workspace \
  -w /workspace \
  llumnix-registry.cn-beijing.cr.aliyuncs.com/llumnix/vllm:dev-20260204-140225 \
  bash
```

| Flag | Description |
|------|-------------|
| `--gpus all` | Pass through all GPUs to the container |
| `-v $(pwd):/workspace` | Mount the current code directory into the container |
| `-w /workspace` | Set working directory inside the container |

> **Note**: Since the code directory is mounted from the host, changes made inside the 
> container are reflected on your local machine and will persist after the container exits.

## Step 3: Set Up the Environment

Inside the container, run:

```bash
go mod tidy

# Install patched vLLM
make vllm-install

# Install Llumlet package
make llumlet-install

# Install Discovery package
make discovery-install

# Build lib-tokenizers (required for Gateway)
make lib-tokenizers-build

# Build Blade KVT
make blade-kvt-install

# Build Mooncake
make mooncake-install
```

## Step 4: Build Components

```bash
# Build Gateway binary
make gateway-build

# Build Scheduler binary
make scheduler-build
```

---

## Development Workflow

Once the environment is set up, the typical development cycle is:

### 1. Make Code Changes

Edit the source code with your preferred editor on your local machine. Changes are 
automatically reflected inside the running container.

### 2. Rebuild Affected Components

Inside the container, rebuild only the affected components:

```bash
# If you modified Gateway code
make gateway-build

# If you modified Scheduler code
make scheduler-build

# If you modified Python packages (Llumlet, Discovery, vLLM patches)
make llumlet-install
make discovery-install
make vllm-install
```

### 3. Run Tests

Verify your changes with the relevant tests:

```bash
# Run all end-to-end tests
make e2e-test

# Run all Go unit tests
make unit-test
```

Refer to [tests/local/utils.py](../../tests/local/utils.py)
for details on how test environments are launched and configured.


---

## Build Development Image (Optional)

If you need to build a custom development image (e.g., modifying base dependencies):

```bash
bash scripts/build_image_dev.sh
```

The script builds from `container/Dockerfile.vllm_dev` and automatically tags the image 
with a timestamp:

```
llumnix-registry.cn-beijing.cr.aliyuncs.com/llumnix/vllm:dev-<YYYYMMDD-HHMMSS>
```

After building, update the image reference in the `docker run` command above to use the 
newly generated tag.
