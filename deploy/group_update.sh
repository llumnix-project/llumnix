#!/bin/bash

set -e  # 遇到错误立即退出

if [ -z "$1" ]; then
  echo "使用方法: $0 <group-name>"
  echo "例如: $0 llumnix"
  exit 1
fi

GROUP_NAME=$1

# 检查 namespace 是否存在
if ! kubectl get namespace "$GROUP_NAME" &>/dev/null; then
  echo "❌ Namespace $GROUP_NAME 不存在"
  echo "请先运行: sh group_deploy.sh $GROUP_NAME"
  exit 1
fi

echo "==> 更新服务组: $GROUP_NAME"
echo "==> 更新所有组件"
kubectl apply -k yaml/ -n "$GROUP_NAME"

echo ""
echo "✓ 服务组 $GROUP_NAME 更新完成"
echo "✓ 查看资源: kubectl get all -n $GROUP_NAME"
echo "✓ 查看日志: kubectl logs -f <pod-name> -n $GROUP_NAME"
