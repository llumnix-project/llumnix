#!/bin/bash

set -e

show_usage() {
    cat << EOF
Usage: $0 <group-name> <kustomize-dir> [OPTIONS]

Arguments:
  group-name         Namespace name (e.g., llumnix1)
  kustomize-dir      Directory containing kustomization.yaml
                     Supports nested directories (e.g., 'pd', 'neutral/lite-mode-scheduling')
Options:
  --repository          Custom image registry (e.g., my-registry.example.com/my-repo)
  --gateway-tag         Gateway image tag
  --scheduler-tag       Scheduler image tag
  --vllm-tag            vLLM image tag
  --discovery-tag       Discovery image tag


Examples:
  # Deploy neutral mode with lite-mode scheduling
  $0 llumnix neutral/lite-mode-scheduling/load-balance
  
  # Deploy neutral mode with full-mode scheduling
  $0 llumnix neutral/full-mode-scheduling/load-balance

  # Deploy pd mode with full-mode scheduling
  $0 llumnix pd/full-mode-scheduling/load-balance

  # Deploy pd+kvs mode with full-mode scheduling
  $0 llumnix pd-kvs/full-mode-scheduling/load-balance
  
  # Deploy with custom image repository and specific image tags
  $0 llumnix neutral/lite-mode-scheduling/load-balance \
      --repository my-registry.example.com \
      --gateway-tag 20260101-120000 \
      --scheduler-tag 20260101-130000 \
      --vllm-tag 20260101-140000  \
      --discovery-tag 20260226-170427
EOF
}

CUSTOM_REPOSITORY=""
GATEWAY_TAG=""
SCHEDULER_TAG=""
VLLM_TAG=""
DISCOVERY_TAG=""
MOONCAKE_VLLM_TAG=""

# Parse arguments
POSITIONAL_ARGS=()
while [[ "$#" -gt 0 ]]; do
    case $1 in
        --repository)    CUSTOM_REPOSITORY="$2"; shift ;;
        --gateway-tag)   GATEWAY_TAG="$2"; shift ;;
        --scheduler-tag) SCHEDULER_TAG="$2"; shift ;;
        --vllm-tag)      VLLM_TAG="$2"; shift ;;
        --discovery-tag) DISCOVERY_TAG="$2"; shift ;;
        --mooncake-vllm-tag) MOONCAKE_VLLM_TAG="$2"; shift ;;
        *) POSITIONAL_ARGS+=("$1") ;;
    esac
    shift
done

set -- "${POSITIONAL_ARGS[@]}"

# Validate arguments
if [ $# -ne 2 ]; then
    show_usage
    exit 1
fi

GROUP_NAME=$1
KUSTOMIZE_DIR=$2

# Validate kustomize directory
if [ ! -d "$KUSTOMIZE_DIR" ]; then
    echo "Error: Directory not found: $KUSTOMIZE_DIR" >&2
    exit 1
fi

if [ ! -f "$KUSTOMIZE_DIR/kustomization.yaml" ]; then
    echo "Error: kustomization.yaml not found in: $KUSTOMIZE_DIR" >&2
    exit 1
fi

DEFAULT_REPOSITORY="llumnix-registry.cn-beijing.cr.aliyuncs.com/llumnix"
export REPOSITORY="${CUSTOM_REPOSITORY:-$DEFAULT_REPOSITORY}"

export GATEWAY_IMAGE_TAG="${GATEWAY_TAG:-20260313-094911}"
export SCHEDULER_IMAGE_TAG="${SCHEDULER_TAG:-20260313-094904}"
export VLLM_IMAGE_TAG="${VLLM_TAG:-20260306-165123}"
export DISCOVERY_IMAGE_TAG="${DISCOVERY_TAG:-20260302-203317}"
export MOONCAKE_VLLM_IMAGE_TAG="${MOONCAKE_VLLM_TAG:-mooncake-20260305-184831}"

echo "[group_deploy] Using repository: $REPOSITORY"
echo "[group_deploy] Gateway tag:      $GATEWAY_IMAGE_TAG"
echo "[group_deploy] Scheduler tag:    $SCHEDULER_IMAGE_TAG"
echo "[group_deploy] vLLM tag:         $VLLM_IMAGE_TAG"
echo "[group_deploy] Discovery tag:    $DISCOVERY_IMAGE_TAG"
echo ""

# Create namespace
echo "Creating namespace: $GROUP_NAME"
kubectl create namespace "$GROUP_NAME" --dry-run=client -o yaml | kubectl apply -f -

# Delegate actual deployment to group_update.sh (handles kustomize + kubectl apply)
echo "[group_deploy] Delegating to group_update.sh for actual deployment..."
echo ""
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
bash "$SCRIPT_DIR/group_update.sh" "$GROUP_NAME" "$KUSTOMIZE_DIR"
