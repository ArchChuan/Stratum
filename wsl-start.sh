#!/bin/bash

# ClawHermes AI Go - WSL/Kubernetes Linux 启动脚本

set -e

echo "🚀 ClawHermes AI Go - WSL/Linux Kubernetes 启动脚本"
echo "=================================================="

# 检查必要的工具
check_tool() {
    if ! command -v "$1" &> /dev/null; then
        echo "❌ $2 未安装，请先安装 $2"
        exit 1
    fi
}

check_tool kubectl "kubectl"
check_tool docker "Docker"
check_tool helm "Helm"

echo "✓ 依赖检查通过"
echo ""

# 检查是否在WSL环境中
if [[ -f /proc/version ]] && grep -qi microsoft /proc/version; then
    echo "✅ 检测到 WSL 环境"
    
    # 确保Docker守护进程正在运行
    if ! pgrep -x "dockerd" > /dev/null; then
        echo "🔄 启动 Docker 服务..."
        sudo systemctl start docker 2>/dev/null || echo "⚠️  无法启动 Docker 服务，请确保 Docker 已正确安装"
    fi
    
    # 检查Kubernetes集群是否可用
    if ! kubectl cluster-info &> /dev/null; then
        echo "⚠️  Kubernetes 集群不可用，请确保已启用 WSL 的 Kubernetes 功能或连接到集群"
        
        # 如果使用Docker Desktop的Kubernetes
        if docker info 2>/dev/null | grep -q "Kubernetes"; then
            echo "🔧 尝试启用 Docker Desktop 的 Kubernetes..."
            echo "💡 提示: 请在 Docker Desktop 设置中启用 Kubernetes 并重启"
        else
            # 检查是否有kind或minikube
            if command -v kind &> /dev/null; then
                echo "✅ 检测到 kind，尝试启动 kind 集群..."
                if ! kind get clusters | grep -q "^clawhermes$"; then
                    echo "🔧 创建 kind 集群..."
                    cat <<EOF | kind create cluster --name clawhermes --config=-
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
  kubeadmConfigPatches:
  - |
    kind: InitConfiguration
    nodeRegistration:
      kubeletExtraArgs:
        node-labels: "ingress-ready=true"
  extraPortMappings:
  - containerPort: 80
    hostPort: 80
    protocol: TCP
  - containerPort: 443
    hostPort: 443
    protocol: TCP
EOF
                fi
            elif command -v minikube &> /dev/null; then
                echo "✅ 检测到 minikube，尝试启动 minikube 集群..."
                if ! minikube status &> /dev/null; then
                    minikube start
                fi
            else
                echo "💡 提示: 请设置 Kubernetes 集群 (例如 kind, minikube, Docker Desktop with K8s)"
                echo "💡 推荐安装 kind: go install sigs.k8s.io/kind@latest"
                exit 1
            fi
        fi
    fi
else
    echo "ℹ️  检测到非WSL Linux 环境"
    if ! kubectl cluster-info &> /dev/null; then
        echo "⚠️  Kubernetes 集群不可用，请确保已正确配置集群访问"
        exit 1
    fi
fi

echo ""

# 1. 构建 Docker 镜像
echo "🐳 构建 Docker 镜像..."
make docker-build

# 2. 将镜像加载到 K8s 集群（如果是本地集群）
if kubectl config current-context | grep -qE "(minikube|docker-desktop|kind|k3d|microk8s)"; then
    echo "🔄 将镜像加载到集群..."
    if kubectl config current-context | grep -q "kind"; then
        kind load docker-image clawhermes-ai-go:latest --name clawhermes
    elif kubectl config current-context | grep -q "minikube"; then
        eval $(minikube docker-env)
        make docker-build
    fi
fi

# 3. 创建命名空间（如果不存在）
echo "🔧 检查并创建命名空间..."
kubectl create namespace clawhermes-system --dry-run=client -o yaml | kubectl apply -f -

# 4. 部署依赖服务到 Kubernetes
echo "📦 部署依赖服务到 Kubernetes..."
kubectl apply -f k8s/security.yaml
kubectl apply -f k8s/dependencies.yaml
kubectl apply -f k8s/monitoring.yaml

echo "⏳ 等待依赖服务启动..."
sleep 15

# 检查依赖服务状态
echo "🔍 检查依赖服务状态..."
kubectl wait --for=condition=ready pod -l app=nats -n clawhermes-system --timeout=180s || echo "⚠️  NATS 服务启动超时"
kubectl wait --for=condition=ready pod -l app=neo4j -n clawhermes-system --timeout=180s || echo "⚠️  Neo4j 服务启动超时"
kubectl wait --for=condition=ready pod -l app=milvus -n clawhermes-system --timeout=180s || echo "⚠️  Milvus 服务启动超时"
kubectl wait --for=condition=ready pod -l app=otel-collector -n clawhermes-system --timeout=180s || echo "⚠️  OTel Collector 服务启动超时"

# 5. 使用Helm部署主应用
echo "🚢 使用 Helm 部署主应用..."

# 创建Helm所需的secret
kubectl create secret generic neo4j-secret \
  --from-literal=username=neo4j \
  --from-literal=password=password \
  --namespace clawhermes-system \
  --dry-run=client -o yaml | kubectl apply -f -

# 检查Helm发布是否存在
if helm status clawhermes-release -n clawhermes-system &> /dev/null; then
    echo "🔄 更新现有发布版本..."
    helm upgrade clawhermes-release ./helm -f helm/values.yaml -n clawhermes-system
else
    echo "🚀 安装新发布版本..."
    helm install clawhermes-release ./helm -f helm/values.yaml -n clawhermes-system
fi

# 6. 等待应用启动
echo "⏳ 等待主应用启动..."
kubectl wait --for=condition=ready pod -l app.kubernetes.io/name=clawhermes-ai-go -n clawhermes-system --timeout=180s

# 7. 显示访问信息
echo ""
echo "📋 服务信息:"
kubectl get svc clawhermes-ai-go -n clawhermes-system

# 8. 显示访问地址
if kubectl config current-context | grep -q "kind"; then
    echo "🌐 服务可通过以下地址访问:"
    echo "   - 本地访问: http://localhost:8080 (通过端口转发)"
    echo "   - 如需外部访问，请使用端口转发: kubectl port-forward -n clawhermes-system svc/clawhermes-ai-go 8080:80"
elif kubectl config current-context | grep -q "minikube"; then
    IP=$(minikube ip)
    PORT=$(kubectl get svc clawhermes-ai-go -n clawhermes-system -o jsonpath='{.spec.ports[0].nodePort}')
    echo "🌐 访问地址: http://$IP:$PORT"
elif kubectl config current-context | grep -q "docker-desktop"; then
    IP="localhost"
    PORT=$(kubectl get svc clawhermes-ai-go -n clawhermes-system -o jsonpath='{.spec.ports[0].nodePort}')
    echo "🌐 访问地址: http://$IP:$PORT"
else
    SVC_TYPE=$(kubectl get svc clawhermes-ai-go -n clawhermes-system -o jsonpath='{.spec.type}')
    if [ "$SVC_TYPE" = "LoadBalancer" ]; then
        IP=$(kubectl get svc clawhermes-ai-go -n clawhermes-system -o jsonpath='{.status.loadBalancer.ingress[0].ip}' 2>/dev/null || echo "pending...")
        echo "🌐 访问地址: http://$IP:80"
        echo "💡 如果地址显示为 pending，请使用端口转发: kubectl port-forward -n clawhermes-system svc/clawhermes-ai-go 8080:80"
    else
        echo "🌐 服务地址: $(kubectl get svc clawhermes-ai-go -n clawhermes-system -o jsonpath='{.spec.clusterIP}')"
        echo "💡 如需外部访问，请使用端口转发: kubectl port-forward -n clawhermes-system svc/clawhermes-ai-go 8080:80"
    fi
fi

echo ""
echo "✅ ClawHermes AI Go 已成功部署到 Kubernetes!"
echo "🔧 要查看主应用日志: kubectl logs -f -n clawhermes-system deployment/clawhermes-ai-go"
echo "🔄 要停止服务: ./wsl-stop.sh"