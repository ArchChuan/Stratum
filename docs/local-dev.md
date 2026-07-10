# 本地 CI/CD 流程 - Minikube + Makefile 快速开始

## 前置要求

### 安装必要工具

```bash
# 1. 安装 Minikube
curl -LO https://github.com/kubernetes/minikube/releases/latest/download/minikube-linux-amd64
sudo install minikube-linux-amd64 /usr/local/bin/minikube

# 2. 安装 kubectl
curl -LO "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl"
sudo install -o root -g root -m 0755 kubectl /usr/local/bin/kubectl

# 3. 安装 Docker（如果未安装）
curl -fsSL https://get.docker.com -o get-docker.sh
sudo sh get-docker.sh

# 4. 安装 Go 依赖工具
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
```

### 系统要求

- **CPU**: 至少 4 核（推荐 8 核）
- **内存**: 至少 8GB（推荐 16GB）
- **磁盘**: 至少 50GB
- **Docker**: 支持 Docker 驱动或其他虚拟化驱动

## 快速开始（3 步）

### 1️⃣ 初始化本地环境

```bash
make setup-local
```

这将：

- ✅ 启动 Minikube
- ✅ 创建 stratum namespace
- ✅ 应用基础配置

### 2️⃣ 运行完整 CI/CD 流程

```bash
make local-pipeline
```

这将：

- ✅ 类型检查 (go vet)
- ✅ Lint 检查 (golangci-lint)
- ✅ 安全扫描 (semgrep)
- ✅ 运行测试
- ✅ 构建 Docker 镜像
- ✅ 部署到 Minikube
- ✅ 验证部署

### 3️⃣ 查看应用

```bash
# 查看日志
make dev-logs

# 或使用端口转发
make dev-port-forward
# 然后访问 http://localhost:8080
```

## 常用命令

### Minikube 管理

```bash
make minikube-start    # 启动 Minikube
make minikube-status   # 查看状态
make minikube-stop     # 停止 Minikube
make minikube-clean    # 删除 Minikube
```

### CI/CD 流程

```bash
make local-ci          # 只运行 CI 检查（不部署）
make local-deploy      # 部署到 Minikube
make local-verify      # 验证部署
make local-rollback    # 回滚部署
```

### 开发调试

```bash
make dev-logs          # 查看应用日志
make dev-port-forward  # 启动端口转发
make dev-shell         # 进入 Pod shell
make dev-metrics       # 查看资源使用
make dev-describe      # 查看 Deployment 详情
make dev-events        # 查看事件日志
```

## 完整工作流示例

### 场景 1: 修改代码后部署

```bash
# 1. 修改代码
vim internal/agent/agent.go

# 2. 运行完整 CI/CD 流程
make local-pipeline

# 3. 查看日志验证
make dev-logs
```

### 场景 2: 快速测试某个模块

```bash
# 1. 类型检查
make typecheck

# 2. 运行该模块的测试
go test -v ./internal/agent -short

# 3. 查看覆盖率
make test-coverage
```

### 场景 3: 性能基准测试

```bash
# 运行基准测试
make bench

# 或特定包
go test -bench=. -benchmem ./internal/memory
```

### 场景 4: 紧急回滚

```bash
# 查看部署历史
kubectl rollout history deployment/stratum-ai -n stratum

# 回滚到上一个版本
make local-rollback

# 或回滚到特定版本
bash scripts/deploy/rollback.sh stratum 2
```

## 监控和调试

### 查看实时日志

```bash
# 所有 Pod 日志
make dev-logs

# 特定 Pod（需要 Pod 名称）
kubectl logs -n stratum POD_NAME -f

# 之前的日志（Pod 重启时）
kubectl logs -n stratum POD_NAME --previous
```

### 进入 Pod 调试

```bash
# 进入 Pod shell
make dev-shell

# 或手动进入
POD=$(kubectl get pods -n stratum -l app=stratum-ai -o jsonpath='{.items[0].metadata.name}')
kubectl exec -it $POD -n stratum -- sh
```

### 查看资源使用

```bash
# 实时资源使用
make dev-metrics

# 或更详细的信息
kubectl top pods -n stratum -l app=stratum-ai --containers

# 查看 Node 资源
kubectl top nodes
```

### 访问可视化面板

```bash
# 启动端口转发
make dev-port-forward

# 然后访问：
# Prometheus:  http://localhost:9090
# Grafana:     http://localhost:3000 (admin/admin)
# Jaeger:      http://localhost:16686
# Minikube:    minikube dashboard
```

## Docker 镜像管理

### 构建镜像

```bash
# 构建镜像（用于 Docker）
make docker-build

# 为 Minikube 构建镜像（推荐）
make docker-build-minikube

# 运行镜像
make docker-run
```

### Minikube Docker 环境

```bash
# 配置 shell 使用 Minikube 的 Docker daemon
eval $(minikube docker-env)

# 验证配置
docker images  # 应该看到 Minikube 内的镜像
```

## 故障排查

### Minikube 无法启动

```bash
# 检查状态
minikube status

# 查看日志
minikube logs

# 删除并重新初始化
minikube delete
make setup-local
```

### Pod 无法启动

```bash
# 查看 Pod 详情
kubectl describe pod POD_NAME -n stratum

# 查看事件
kubectl get events -n stratum --sort-by='.lastTimestamp'

# 查看日志
kubectl logs POD_NAME -n stratum --previous
```

### 镜像相关问题

```bash
# 检查镜像是否存在
eval $(minikube docker-env)
docker images | grep stratum

# 重新构建镜像
make docker-build-minikube

# 删除并重新部署
kubectl delete deployment stratum-ai -n stratum
make local-deploy
```

### 内存/CPU 不足

```bash
# 增加 Minikube 资源
minikube stop
minikube start --cpus=8 --memory=16384

# 或检查当前资源
minikube ssh -- free -h
minikube ssh -- nproc
```

## 环境变量和配置

### 本地开发配置

所有配置通过 `stratum-config` ConfigMap 管理，存储在：

```bash
kubectl get configmap stratum-config -n stratum -o yaml
```

修改配置：

```bash
kubectl edit configmap stratum-config -n stratum
# 然后重启 Pod
kubectl rollout restart deployment/stratum-ai -n stratum
```

## 性能优化建议

### Minikube 启动优化

```bash
# 使用 KVM 驱动（Linux）
minikube start --driver=kvm2

# 预分配资源
minikube start --cpus=8 --memory=16384 --disk-size=100g
```

### 镜像拉取优化

```bash
# 启用本地镜像仓库缓存
minikube addons enable registry
```

### 测试优化

```bash
# 并行运行测试
go test -v -parallel 4 ./...

# 只运行快速测试
make test-local  # 已配置 -short 标志
```

## 常见问题

**Q: 每次都需要重新构建镜像吗？**
A: 不是。使用 `make docker-build-minikube` 后，镜像存储在 Minikube 内，可以重用。只需修改代码后重新构建。

**Q: 如何在本地测试多个副本？**
A: 编辑 deployment.yaml 的 `replicas` 字段或使用：

```bash
kubectl scale deployment/stratum-ai -n stratum --replicas=3
```

**Q: 如何模拟生产环境？**
A: 使用生产 values 文件：

```bash
helm install stratum ./charts/stratum-ai -n stratum -f charts/stratum-ai/values-prod.yaml
```

**Q: 如何持久化数据？**
A: 配置 Minikube 的 PersistentVolume，详见 k8s/dependencies.yaml

## 更多资源

- [Minikube 文档](https://minikube.sigs.k8s.io/)
- [Kubernetes 文档](https://kubernetes.io/docs/)
- [本项目 k8s 配置](../k8s/)
- [部署脚本](../scripts/deploy/)
