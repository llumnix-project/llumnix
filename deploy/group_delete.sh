#!/bin/bash

set -e

# Usage check
if [ -z "$1" ]; then
  echo "Usage: $0 <group-name>"
  echo "Example: $0 llumnix1"
  exit 1
fi

GROUP_NAME=$1

# Check if namespace exists
if ! kubectl get namespace "$GROUP_NAME" &> /dev/null; then
  echo "Error: namespace '$GROUP_NAME' does not exist"
  exit 1
fi

# Display resources to be deleted
echo "==> Resources to be deleted:"
echo "--- Deployments ---"
kubectl get deployments -n "$GROUP_NAME" 2>/dev/null || echo "None"
echo ""
echo "--- StatefulSets ---"
kubectl get statefulsets -n "$GROUP_NAME" 2>/dev/null || echo "None"
echo ""
echo "--- Jobs ---"
kubectl get jobs -n "$GROUP_NAME" 2>/dev/null || echo "None"
echo ""
echo "--- Services ---"
kubectl get services -n "$GROUP_NAME" 2>/dev/null || echo "None"
echo ""

# Confirmation prompt
read -p "Confirm deletion of group '$GROUP_NAME' and all its resources? (yes/no): " CONFIRM
if [ "$CONFIRM" != "yes" ]; then
  echo "Deletion cancelled"
  exit 0
fi

echo "==> Deleting service group: $GROUP_NAME"

# Helper function to delete resources
delete_resource() {
  local resource_type=$1
  echo "==> Deleting $resource_type"
  kubectl delete "$resource_type" --all -n "$GROUP_NAME" --ignore-not-found=true --timeout=10s 2>/dev/null || true
}

# 0. Delete LeaderWorkerSets (CRD)
if kubectl api-resources | grep -q "leaderworkersets"; then
  echo "==> Deleting LeaderWorkerSets (CRD)"
  kubectl delete leaderworkersets --all -n "$GROUP_NAME" \
    --ignore-not-found=true \
    --timeout=60s 2>/dev/null || true
else
  echo "==> LeaderWorkerSet CRD not found, skipping"
fi

# 1. Delete workloads
delete_resource "statefulsets"
delete_resource "deployments"
delete_resource "daemonsets"
delete_resource "jobs"

# 2. Delete pods
delete_resource "pods"

# 3. Delete services
delete_resource "services"

# 4. Delete namespace
echo "==> Deleting namespace"
kubectl delete namespace "$GROUP_NAME" --timeout=10s 2>/dev/null || true

echo ""
echo "✓ Service group '$GROUP_NAME' deleted successfully"
