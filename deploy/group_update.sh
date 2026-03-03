#!/bin/bash

set -e

show_usage() {
    cat << EOF
Usage: $0 <group-name> <kustomize-dir>

Arguments:
  group-name         Namespace name (e.g., llumnix1)
  kustomize-dir      Directory containing kustomization.yaml
                     Supports nested directories (e.g., 'pd', 'neutral/lite-mode-scheduling')

Examples:
  # Update pd mode with full-mode scheduling
  $0 llumnix1 pd/full-mode-scheduling
  
  # Update neutral mode with lite-mode scheduling
  $0 llumnix2 neutral/lite-mode-scheduling/load-balance
  
  # Update neutral mode with full-mode scheduling
  $0 llumnix3 neutral/full-mode-scheduling/load-balance
EOF
}

# Validate arguments
if [ $# -ne 2 ]; then
    show_usage
    exit 1
fi

GROUP_NAME=$1
KUSTOMIZE_DIR=$2

# Validate kustomize directory
if [ -z "$KUSTOMIZE_DIR" ]; then
    echo "Error: KUSTOMIZE_DIR is required" >&2
    show_usage
    exit 1
fi

if [ ! -d "$KUSTOMIZE_DIR" ]; then
    echo "Error: Directory not found: $KUSTOMIZE_DIR" >&2
    exit 1
fi

if [ ! -f "$KUSTOMIZE_DIR/kustomization.yaml" ]; then
    echo "Error: kustomization.yaml not found in: $KUSTOMIZE_DIR" >&2
    exit 1
fi

# Check namespace exists
if ! kubectl get namespace "$GROUP_NAME" &>/dev/null; then
    echo "Error: Namespace $GROUP_NAME does not exist" >&2
    echo "Please run deployment first: ./group_deploy.sh $GROUP_NAME $KUSTOMIZE_DIR" >&2
    exit 1
fi

# Update deployment
echo "Updating deployment in namespace: $GROUP_NAME"
echo "Kustomize directory: $KUSTOMIZE_DIR"

kubectl kustomize "$KUSTOMIZE_DIR/" \
  | envsubst '${REPOSITORY} ${VLLM_IMAGE_TAG} ${DISCOVERY_IMAGE_TAG} ${GATEWAY_IMAGE_TAG} ${SCHEDULER_IMAGE_TAG} ${MOONCAKE_VLLM_IMAGE_TAG}' \
  | kubectl apply -f - -n "$GROUP_NAME"


# Wait for rollout
sleep 2

# Show status
echo ""
echo "Pods status:"
kubectl get pods -o wide -n "$GROUP_NAME"

echo ""
echo "Services status:"
kubectl get service -o wide -n "$GROUP_NAME"

# Summary
echo ""
echo "Update completed successfully"
echo "  Namespace: $GROUP_NAME"
echo "  Kustomize directory: $KUSTOMIZE_DIR"
echo ""
echo "Useful commands:"
echo "  kubectl get all -n $GROUP_NAME"
echo "  kubectl logs -f <pod-name> -n $GROUP_NAME"
