#!/bin/bash

set -e

show_usage() {
    cat << EOF
Usage: $0 <group-name> <kustomize-dir> [OPTIONS]

Arguments:
  group-name         Namespace name (e.g., llumnix1)
  kustomize-dir      Directory containing kustomization.yaml

Options:
  --repository          Custom image registry
  --gateway-tag         Gateway image tag
  --scheduler-tag       Scheduler image tag
  --vllm-tag            vLLM image tag
  --discovery-tag       Discovery image tag
  --mooncake-vllm-tag   Mooncake vLLM image tag

Examples:
  $0 llumnix1 pd/full-mode-scheduling
  $0 llumnix2 neutral/lite-mode-scheduling/load-balance --vllm-tag 20260306-165123
  $0 llumnix3 neutral/full-mode-scheduling/load-balance \
      --repository my-registry.example.com \
      --gateway-tag 20260101-120000
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
        --repository)        CUSTOM_REPOSITORY="$2"; shift ;;
        --gateway-tag)       GATEWAY_TAG="$2"; shift ;;
        --scheduler-tag)     SCHEDULER_TAG="$2"; shift ;;
        --vllm-tag)          VLLM_TAG="$2"; shift ;;
        --discovery-tag)     DISCOVERY_TAG="$2"; shift ;;
        --mooncake-vllm-tag) MOONCAKE_VLLM_TAG="$2"; shift ;;
        *) POSITIONAL_ARGS+=("$1") ;;
    esac
    shift
done

set -- "${POSITIONAL_ARGS[@]}"

if [ $# -ne 2 ]; then
    show_usage
    exit 1
fi

GROUP_NAME=$1
KUSTOMIZE_DIR=$2

if [ ! -d "$KUSTOMIZE_DIR" ]; then
    echo "Error: Directory not found: $KUSTOMIZE_DIR" >&2
    exit 1
fi

if [ ! -f "$KUSTOMIZE_DIR/kustomization.yaml" ]; then
    echo "Error: kustomization.yaml not found in: $KUSTOMIZE_DIR" >&2
    exit 1
fi

if ! kubectl get namespace "$GROUP_NAME" &>/dev/null; then
    echo "Error: Namespace $GROUP_NAME does not exist" >&2
    echo "Please run deployment first: ./group_deploy.sh $GROUP_NAME $KUSTOMIZE_DIR" >&2
    exit 1
fi

DEFAULT_REPOSITORY="llumnix-registry.cn-beijing.cr.aliyuncs.com/llumnix"
export REPOSITORY="${CUSTOM_REPOSITORY:-${REPOSITORY:-$DEFAULT_REPOSITORY}}"
# Priority: 1) command line args, 2) parent script env vars, 3) default values
export GATEWAY_IMAGE_TAG="${GATEWAY_TAG:-${GATEWAY_IMAGE_TAG:-20260313-155940}}"
export SCHEDULER_IMAGE_TAG="${SCHEDULER_TAG:-${SCHEDULER_IMAGE_TAG:-20260313-094904}}"
export VLLM_IMAGE_TAG="${VLLM_TAG:-${VLLM_IMAGE_TAG:-20260306-165123}}"
export DISCOVERY_IMAGE_TAG="${DISCOVERY_TAG:-${DISCOVERY_IMAGE_TAG:-20260302-203317}}"
export MOONCAKE_VLLM_IMAGE_TAG="${MOONCAKE_VLLM_TAG:-${MOONCAKE_VLLM_IMAGE_TAG:-mooncake-20260305-184831}}"

echo "[group_update] Using repository:  $REPOSITORY"
echo "[group_update] Gateway tag:       $GATEWAY_IMAGE_TAG"
echo "[group_update] Scheduler tag:     $SCHEDULER_IMAGE_TAG"
echo "[group_update] vLLM tag:          $VLLM_IMAGE_TAG"
echo "[group_update] Discovery tag:     $DISCOVERY_IMAGE_TAG"
# Only show mooncake tag if it's actually used in the deployment
if grep -r "MOONCAKE_VLLM_IMAGE_TAG\|mooncake" "$KUSTOMIZE_DIR"/*.yaml >/dev/null 2>&1; then
    echo "[group_update] Mooncake-vLLM tag: $MOONCAKE_VLLM_IMAGE_TAG"
fi
echo ""
echo "Updating deployment in namespace: $GROUP_NAME"
echo "Kustomize directory: $KUSTOMIZE_DIR"

kubectl kustomize "$KUSTOMIZE_DIR/" \
  | envsubst '${REPOSITORY} ${VLLM_IMAGE_TAG} ${DISCOVERY_IMAGE_TAG} ${GATEWAY_IMAGE_TAG} ${SCHEDULER_IMAGE_TAG} ${MOONCAKE_VLLM_IMAGE_TAG}' \
  | kubectl apply -f - -n "$GROUP_NAME"

sleep 2

echo ""
echo "Pods status:"
kubectl get pods -o wide -n "$GROUP_NAME"

echo ""
echo "Services status:"
kubectl get service -o wide -n "$GROUP_NAME"

echo ""
echo "Update completed successfully"
echo "  Namespace: $GROUP_NAME"
echo "  Kustomize directory: $KUSTOMIZE_DIR"
echo ""
echo "Useful commands:"
echo "  kubectl get all -n $GROUP_NAME"
echo "  kubectl logs -f <pod-name> -n $GROUP_NAME"
