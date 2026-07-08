# 本地 CI/CD 快速参考

## 🚀 3 步快速开始

```bash
# 1. 初始化环境
make setup-local

# 2. 运行完整 CI/CD
make local-pipeline

# 3. 查看日志
make dev-logs
```

## 📋 核心命令

| 命令 | 说明 |
|------|------|
| `make minikube-start` | 启动 Minikube |
| `make local-pipeline` | 完整 CI/CD 流程 |
| `make local-ci` | 只运行检查（无部署） |
| `make local-deploy` | 部署到 Minikube |
| `make local-verify` | 验证部署 |
| `make dev-logs` | 查看应用日志 |
| `make dev-port-forward` | 端口转发 |
| `make dev-shell` | 进入 Pod shell |

## 🔄 完整 CI/CD 流程

```
代码提交 
   ↓
类型检查 (go vet)
   ↓
Lint (golangci-lint)
   ↓
安全扫描 (semgrep)
   ↓
本地测试 (short)
   ↓
全量测试 (race, coverage)
   ↓
构建镜像
   ↓
部署到 Minikube
   ↓
验证部署
   ↓
✅ 完成
```

## 📍 访问地址（端口转发后）

- 应用: `http://localhost:8080`
- Prometheus: `http://localhost:9090`
- Grafana: `http://localhost:3000` (admin/admin)
- Jaeger: `http://localhost:16686`

## 🛠️ 故障快速修复

### Pod 无法启动

```bash
kubectl describe pod POD_NAME -n clawhermes
kubectl logs POD_NAME -n clawhermes --previous
```

### 镜像问题

```bash
eval $(minikube docker-env)
make docker-build-minikube
make local-deploy
```

### 资源不足

```bash
minikube stop
minikube start --cpus=8 --memory=16384
```

## 📊 常用检查

```bash
# 查看状态
make minikube-status

# 查看 Pod
kubectl get pods -n clawhermes -w

# 查看资源
make dev-metrics

# 查看事件
make dev-events
```

## 💾 快速回滚

```bash
# 回滚到上一个版本
make local-rollback

# 或指定版本
bash scripts/deploy/rollback.sh clawhermes VERSION_NUM
```

## 📝 完整工作流示例

```bash
# 修改代码
vim internal/agent/agent.go

# 运行 CI/CD
make local-pipeline

# 查看结果
make dev-logs

# 如果出错，回滚
make local-rollback
```

## ⚡ 单个阶段执行

```bash
make typecheck      # 类型检查
make lint           # Lint 检查
make security-scan  # 安全扫描
make test-local     # 快速测试
make test-full      # 完整测试
make docker-build-minikube  # 构建镜像
make local-deploy   # 部署
make local-verify   # 验证
```
