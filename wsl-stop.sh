#!/bin/bash

# ClawHermes AI Go - WSL/Kubernetes Linux 停止脚本

set -e

echo "🛑 ClawHermes AI Go - WSL/Linux 停止脚本"
echo "======================================="

# 检查 kubectl 是否安装
if ! command -v kubectl &> /dev/null; then
    echo "❌ kubectl 未安装，请先安装 kubectl"
    exit 1
fi

if ! command -v helm &> /dev/null; then
    echo "❌ Helm 未安装，请先安装 Helm"
    exit 1
fi

echo ""

# 1. 卸载 Helm 发布
echo "🧹 卸载 Helm 发布..."
helm uninstall clawhermes-release -n clawhermes-system || echo "⚠️  Helm 发布不存在或已卸载"

# 2. 删除 Kubernetes 部署和服务
echo "🗑️  删除主应用部署..."
kubectl delete -f k8s/deployment.yaml -n clawhermes-system --ignore-not-found=true

# 3. 删除依赖服务
echo "🗑️  删除依赖服务..."
kubectl delete -f k8s/dependencies.yaml -n clawhermes-system --ignore-not-found=true
kubectl delete -f k8s/monitoring.yaml -n clawhermes-system --ignore-not-found=true
kubectl delete -f k8s/security.yaml --ignore-not-found=true

# 4. 清理可能存在的持久卷
echo "🧹 清理持久卷声明..."
kubectl delete pvc nats-pvc etcd-pvc minio-pvc milvus-pvc neo4j-data-pvc neo4j-logs-pvc -n clawhermes-system --ignore-not-found=true

# 5. 删除命名空间（如果存在）
echo "🗑️  删除命名空间..."
kubectl delete namespace clawhermes-system --ignore-not-found=true

# 6. 检查是否在WSL环境中，如果有kind集群则删除
if [[ -f /proc/version ]] && grep -qi microsoft /proc/version; then
    if command -v kind &> /dev/null && kind get clusters | grep -q "^clawhermes$"; then
        echo "🗑️  删除 kind 集群..."
        kind delete cluster --name clawhermes
    fi
fi

# 7. 等待资源清理
echo "⏳ 等待资源清理..."
sleep 10

# 8. 验证清理结果
echo "🔍 验证清理结果..."
kubectl get pods -n clawhermes-system | grep -i clawhermes || echo "✅ 主应用已清理"
kubectl get pods -n clawhermes-system | grep -E "nats|milvus|neo4j|etcd|minio|otel" || echo "✅ 依赖服务已清理"

echo ""
echo "✅ ClawHermes AI Go 已从 Kubernetes 中成功移除!"
echo ""
echo "💡 提示: 要重新启动，请运行 ./wsl-start.sh"