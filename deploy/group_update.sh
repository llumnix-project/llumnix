#!/bin/bash

set -e

show_usage() {
    cat << EOF
Usage: $0 <group-name> <deployment-type>

Arguments:
  group-name        Namespace name (e.g., llumnix1)
  deployment-type   Deployment type: 'pd' or 'normal'

Example:
  $0 llumnix1 pd
  $0 llumnix2 normal
EOF
}

# Validate arguments
if [ $# -ne 2 ]; then
    show_usage
    exit 1
fi

GROUP_NAME=$1
DEPLOY_TYPE=$2

# Validate deployment type
if [[ "$DEPLOY_TYPE" != "pd" && "$DEPLOY_TYPE" != "normal" ]]; then
    echo "Error: Invalid deployment type: $DEPLOY_TYPE" >&2
    echo "Must be 'pd' or 'normal'" >&2
    exit 1
fi

# Check namespace exists
if ! kubectl get namespace "$GROUP_NAME" &>/dev/null; then
    echo "Error: Namespace $GROUP_NAME does not exist" >&2
    echo "Please run deployment first: ./group_deploy.sh $GROUP_NAME $DEPLOY_TYPE" >&2
    exit 1
fi

# Update deployment
echo "Updating deployment in namespace: $GROUP_NAME"
echo "Deployment type: $DEPLOY_TYPE"

kubectl apply -k "$DEPLOY_TYPE/" -n "$GROUP_NAME"

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
echo "  Type: $DEPLOY_TYPE"
echo ""
echo "Useful commands:"
echo "  kubectl get all -n $GROUP_NAME"
echo "  kubectl logs -f <pod-name> -n $GROUP_NAME"
