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

Environment Variables:
  ALIYUN_DOCKER_SERVER    (default: beijing-pooling-registry-vpc.cn-beijing.cr.aliyuncs.com)
  ALIYUN_DOCKER_USERNAME  (required)
  ALIYUN_DOCKER_PASSWORD  (required)
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

# Check environment variables
DOCKER_SERVER="${ALIYUN_DOCKER_SERVER:-beijing-pooling-registry-vpc.cn-beijing.cr.aliyuncs.com}"
DOCKER_USERNAME="${ALIYUN_DOCKER_USERNAME}"
DOCKER_PASSWORD="${ALIYUN_DOCKER_PASSWORD}"

if [ -z "$DOCKER_USERNAME" ] || [ -z "$DOCKER_PASSWORD" ]; then
    echo "Error: Missing required environment variables" >&2
    cat << EOF >&2
Please set the following environment variables:
  export ALIYUN_DOCKER_USERNAME=<your-username>
  export ALIYUN_DOCKER_PASSWORD=<your-password>
EOF
    exit 1
fi

# Create namespace
echo "Creating namespace: $GROUP_NAME"
kubectl create namespace "$GROUP_NAME" --dry-run=client -o yaml | kubectl apply -f -

# Create docker registry secret
echo "Creating aliyun-registry-secret"
kubectl delete secret aliyun-registry-secret -n "$GROUP_NAME" --ignore-not-found=true

kubectl create secret docker-registry aliyun-registry-secret \
    --docker-server="$DOCKER_SERVER" \
    --docker-username="$DOCKER_USERNAME" \
    --docker-password="$DOCKER_PASSWORD" \
    -n "$GROUP_NAME"

# Deploy with kustomize
echo "Deploying $DEPLOY_TYPE to namespace: $GROUP_NAME"
kubectl apply -k "$DEPLOY_TYPE/" -n "$GROUP_NAME"

# Wait a moment for resources to be created
sleep 2

# Show deployment status
echo ""
echo "Pods status:"
kubectl get pods -o wide -n "$GROUP_NAME"

echo ""
echo "Services status:"
kubectl get service -o wide -n "$GROUP_NAME"

# Summary
echo ""
echo "Deployment completed successfully"
echo "  Namespace: $GROUP_NAME"
echo "  Type: $DEPLOY_TYPE"
echo ""
echo "Useful commands:"
echo "  kubectl get all -n $GROUP_NAME"
echo "  kubectl get pods -n $GROUP_NAME"
echo "  kubectl logs -f <pod-name> -n $GROUP_NAME"
