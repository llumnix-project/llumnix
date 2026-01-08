#!/bin/bash

set -e  # 遇到错误立即退出

if [ -z "$1" ]; then
  echo "使用方法: $0 <group-name>"
  echo "例如: $0 llumnix1"
  exit 1
fi

GROUP_NAME=$1

# 检查 namespace 是否存在
if ! kubectl get namespace "$GROUP_NAME" &> /dev/null; then
  echo "错误: namespace '$GROUP_NAME' 不存在"
  exit 1
fi

# 显示将要删除的资源
echo "==> 将要删除的资源:"
echo "--- Deployments ---"
kubectl get deployments -n "$GROUP_NAME" 2>/dev/null || echo "无"
echo ""
echo "--- StatefulSets ---"
kubectl get statefulsets -n "$GROUP_NAME" 2>/dev/null || echo "无"
echo ""
echo "--- Services ---"
kubectl get services -n "$GROUP_NAME" 2>/dev/null || echo "无"
echo ""


echo ""
read -p "确认删除服务组 '$GROUP_NAME' 及其所有资源? (yes/no): " CONFIRM

if [ "$CONFIRM" != "yes" ]; then
  echo "已取消删除"
  exit 0
fi

echo "==> 正在删除服务组: $GROUP_NAME"


# 2. 删除 StatefulSets 和 Deployments
echo "==> 删除 StatefulSets"
kubectl delete statefulsets --all -n "$GROUP_NAME" --grace-period=0 --force 2>/dev/null || true

echo "==> 删除 Deployments"
kubectl delete deployments --all -n "$GROUP_NAME" --grace-period=0 --force 2>/dev/null || true

# 3. 强制删除所有 Pod
echo "==> 强制删除所有 Pod"
kubectl delete pods --all -n "$GROUP_NAME" --grace-period=0 --force 2>/dev/null || true

# 4. 删除 Services
echo "==> 删除 Services"
kubectl delete services --all -n "$GROUP_NAME" --grace-period=0 2>/dev/null || true

# 1. 先删除 PVC (关键步骤，必须最先删除)
echo "==> 删除 PVC"
kubectl delete pvc --all -n "$GROUP_NAME" --grace-period=0 --force 2>/dev/null || true

# 等待一下
sleep 1

# 5. 删除 namespace
echo "==> 删除 namespace"
kubectl delete namespace "$GROUP_NAME" --grace-period=0 --force 2>/dev/null || true

# 等待一下，确保 API Server 稳定
sleep 2

# 6. 删除对应的 PV (最后删除集群级别资源)
echo "==> 正在删除 PV: $GROUP_NAME-oss-llm-cache"
if kubectl get pv "$GROUP_NAME-oss-llm-cache" &> /dev/null; then
  kubectl delete pv "$GROUP_NAME-oss-llm-cache" --grace-period=0 --force
  echo "✓ PV 已删除"
else
  echo "⚠️  PV '$GROUP_NAME-oss-llm-cache' 不存在，跳过"
fi
echo "==> 正在删除 PV: $GROUP_NAME-nas"
if kubectl get pv "$GROUP_NAME-nas" &> /dev/null; then
  kubectl delete pv "$GROUP_NAME-nas" --grace-period=0 --force
  echo "✓ PV 已删除"
else
  echo "⚠️  PV '$GROUP_NAME-nas' 不存在，跳过"
fi

echo ""
echo "✓ 服务组 $GROUP_NAME 已删除"
