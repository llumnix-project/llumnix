#!/bin/bash

set -e  # 遇到错误立即退出

if [ -z "$1" ]; then
  echo "使用方法: $0 <group-name>"
  echo "例如: $0 llumnix1"
  exit 1
fi

GROUP_NAME=$1

# 创建 namespace（如果不存在）
echo "==> 创建 namespace: $GROUP_NAME"
kubectl create namespace "$GROUP_NAME" --dry-run=client -o yaml | kubectl apply -f -

# 创建阿里云镜像拉取 Secret
echo "==> 创建 aliyun-registry-secret"
DOCKER_SERVER="${ALIYUN_DOCKER_SERVER:-beijing-pooling-registry-vpc.cn-beijing.cr.aliyuncs.com}"
DOCKER_USERNAME="${ALIYUN_DOCKER_USERNAME:-cuikuilong.ckl@1748815774914563}"
DOCKER_PASSWORD="${ALIYUN_DOCKER_PASSWORD:-Ckl110971}"

if [ -z "$DOCKER_USERNAME" ] || [ -z "$DOCKER_PASSWORD" ]; then
  echo "⚠️  警告: 未设置 ALIYUN_DOCKER_USERNAME 或 ALIYUN_DOCKER_PASSWORD 环境变量"
  echo "请设置环境变量后重试:"
  echo "  export ALIYUN_DOCKER_USERNAME=<你的用户名>"
  echo "  export ALIYUN_DOCKER_PASSWORD=<你的密码>"
  exit 1
fi

# 检查 secret 是否已存在,如果存在则先删除
if kubectl get secret aliyun-registry-secret -n "$GROUP_NAME" &>/dev/null; then
  echo "==> Secret 已存在,删除旧的 secret"
  kubectl delete secret aliyun-registry-secret -n "$GROUP_NAME"
fi

# 创建新的 secret
kubectl create secret docker-registry aliyun-registry-secret \
  --docker-server="$DOCKER_SERVER" \
  --docker-username="$DOCKER_USERNAME" \
  --docker-password="$DOCKER_PASSWORD" \
  -n "$GROUP_NAME"

echo "✓ Secret 创建成功"

# 创建 PV 和 PVC
echo "==> 创建 PV 和 PVC"
echo "==> OSS"
sed "s/GROUP_NAME_PLACEHOLDER/$GROUP_NAME/g" yaml/oss-pv.yaml | kubectl apply -f -
sleep 1
sed "s/GROUP_NAME_PLACEHOLDER/$GROUP_NAME/g" yaml/oss-pvc.yaml | kubectl apply -f -
sleep 1
echo "==> NAS"
sed "s/GROUP_NAME_PLACEHOLDER/$GROUP_NAME/g" yaml/nas-pv.yaml | kubectl apply -f -
sleep 1
sed "s/GROUP_NAME_PLACEHOLDER/$GROUP_NAME/g" yaml/nas-pvc.yaml | kubectl apply -f -
sleep 1
echo "✓ PV 和 PVC 创建成功"

# 部署到指定 namespace
echo "==> 部署到 namespace: $GROUP_NAME"
kubectl apply -k yaml/ -n "$GROUP_NAME"

echo "==> pods"
kubectl get pods -o wide -n "$GROUP_NAME"
echo "==> services"
kubectl get service -o wide -n "$GROUP_NAME"

echo ""
echo "✓ 服务组 $GROUP_NAME 部署完成"
echo "✓ 查看资源: kubectl get all -n $GROUP_NAME"
echo "✓ 查看 Pod 状态: kubectl get pods -n $GROUP_NAME"