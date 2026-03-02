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
  # Deploy neutral mode with lite-mode scheduling
  $0 llumnix neutral/lite-mode-scheduling/load-balance
  
  # Deploy neutral mode with full-mode scheduling
  $0 llumnix neutral/full-mode-scheduling/load-balance

  # Deploy pd mode with full-mode scheduling
  $0 llumnix pd/full-mode-scheduling/load-balance

  # Deploy pd+kvs mode with full-mode scheduling
  $0 llumnix pd-kvs/full-mode-scheduling/load-balance
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

# Create namespace
echo "Creating namespace: $GROUP_NAME"
kubectl create namespace "$GROUP_NAME" --dry-run=client -o yaml | kubectl apply -f -

# Delegate deploy + status reporting to group_update.sh
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
bash "$SCRIPT_DIR/group_update.sh" "$GROUP_NAME" "$KUSTOMIZE_DIR"
