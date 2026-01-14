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

# Delete resources in proper order
delete_resource() {
  local resource_type=$1
  local resource_name=${2:-"--all"}
  echo "==> Deleting $resource_type"
  kubectl delete "$resource_type" $resource_name -n "$GROUP_NAME" --grace-period=0 --force 2>/dev/null || true
}

# 1. Delete workloads
delete_resource "statefulsets"
delete_resource "deployments"

# 2. Delete pods
delete_resource "pods"

# 3. Delete services
delete_resource "services"

# 4. Delete PVCs first
delete_resource "pvc"

sleep 1

# 5. Delete namespace
echo "==> Deleting namespace"
kubectl delete namespace "$GROUP_NAME" --grace-period=0 --force 2>/dev/null || true

sleep 2

# 6. Delete associated PVs
delete_pv() {
  local pv_name=$1
  echo "==> Deleting PV: $pv_name"
  if kubectl get pv "$pv_name" &> /dev/null; then
    kubectl delete pv "$pv_name" --grace-period=0 --force
    echo "✓ PV deleted"
  else
    echo "⚠ PV '$pv_name' not found, skipping"
  fi
}

delete_pv "$GROUP_NAME-oss-llm-cache"
delete_pv "$GROUP_NAME-nas"

echo ""
echo "✓ Service group '$GROUP_NAME' deleted successfully"
