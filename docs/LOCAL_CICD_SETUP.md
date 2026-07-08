# 本地 CI/CD 环境配置完成总结

## 📦 已创建的文件

### 1. 增强的 Makefile

**位置**: `Makefile`

新增命令分类：

- **Minikube 管理**: `minikube-start`, `minikube-stop`, `minikube-status`, `minikube-clean`
- **本地 CI/CD**: `local-pipeline`, `local-ci`, `local-deploy`, `local-verify`, `local-rollback`
- **开发辅助**: `dev-logs`, `dev-port-forward`, `dev-shell`, `dev-metrics`, `dev-events`, `dev-describe`
- **工作流**: `setup-local`, `full-reset`, `help-local`

### 2. Minikube 初始化脚本

**位置**: `scripts/minikube/start.sh`

功能：

- ✅ 检查依赖工具（minikube, kubectl, docker）
- ✅ 启动/重新启动 Minikube
- ✅ 配置集群资源（4 CPU, 8GB 内存, 50GB 磁盘）
- ✅ 启用必要的 addons（ingress, dashboard, metrics-server）
- ✅ 创建 namespace 和初始配置

### 3. 本地 CI/CD 运行脚本

**位置**: `scripts/local-ci/run.sh`

执行流程：

1. 类型检查 (go vet)
2. Lint 检查 (golangci-lint)
3. 安全扫描 (semgrep)
4. 本地测试 (快速)
5. 全量测试 (race, coverage)

### 4. 本地部署脚本

**位置**: `scripts/deploy/deploy-local.sh`

功能：

- ✅ 配置 Minikube Docker 环境
- ✅ 构建本地镜像
- ✅ 应用配置
- ✅ 部署应用
- ✅ 等待部署就绪

### 5. 部署验证脚本

**位置**: `scripts/deploy/verify.sh`

验证内容：

- ✅ Deployment 存在性
- ✅ Pod 就绪状态
- ✅ 健康检查端点
- ✅ 指标端点
- ✅ 最近事件日志

### 6. 其他部署脚本

- `scripts/deploy/init.sh` - K8s 环境初始化
- `scripts/deploy/pre-deploy-check.sh` - 部署前检查
- `scripts/deploy/post-deploy-check.sh` - 部署后检查
- `scripts/deploy/rollback.sh` - 快速回滚

### 7. 本地 CI/CD 文档

**位置**: `docs/local-dev.md`

包含：

- 前置要求和安装指南
- 快速开始（3 步）
- 常用命令参考
- 工作流示例
- 监控和调试方法
- 故障排查指南

### 8. 快速参考卡片

**位置**: `docs/quick-ref.md`

包含：

- 3 步快速开始
- 核心命令速查表
- CI/CD 流程图
- 访问地址列表
- 故障快速修复

### 9. Helm 多环境配置

**位置**: `charts/clawhermes-ai/`

- `values-dev.yaml` - 开发环境配置（1 副本，debug 日志）
- `values-staging.yaml` - 测试环境配置（2 副本，HPA 启用）
- `values-prod.yaml` - 生产环境配置（3 副本，Pod 反亲和性）

### 10. GitHub Actions 增强

**位置**: `.github/workflows/ci-cd.yml`

新增 jobs：

- `version` - 生成版本信息
- `deploy-dev` - 部署到 Dev 环境
- `deploy-staging` - 部署到 Staging 环境
- `deploy-prod` - 部署到 Production（需要审批）

## 🚀 快速开始（三步搞定）

### 步骤 1: 初始化环境

```bash
make setup-local
```

这将自动：

- 安装/启动 Minikube
- 配置 Kubernetes 集群
- 创建必要的 namespace 和配置

### 步骤 2: 运行完整 CI/CD

```bash
make local-pipeline
```

完整流程包括：

- 代码检查和测试
- 镜像构建
- 部署到 Minikube
- 自动验证

### 步骤 3: 验证和调试

```bash
# 查看日志
make dev-logs

# 或启用端口转发
make dev-port-forward
# 然后访问 http://localhost:8080
```

## 📋 常用场景

### 场景 1: 修改代码并快速验证

```bash
vim internal/agent/agent.go
make local-pipeline
make dev-logs
```

### 场景 2: 运行特定测试

```bash
# 快速测试
make test-local

# 完整测试
make test-full

# 特定包测试
go test -v ./internal/agent -short
```

### 场景 3: 快速回滚

```bash
make local-rollback
```

### 场景 4: 进 Pod 调试

```bash
make dev-shell
# 在容器内执行命令
curl http://localhost:8080/health
```

## 📊 可视化工作流

```
┌─────────────────────────────────────────────┐
│  make setup-local                           │
│  启动 Minikube + 初始化配置                  │
└────────────────────┬────────────────────────┘
                     ↓
┌─────────────────────────────────────────────┐
│  make local-pipeline                        │
│  ├─ typecheck (go vet)                     │
│  ├─ lint (golangci-lint)                   │
│  ├─ security-scan (semgrep)                │
│  ├─ test-local                             │
│  ├─ test-full                              │
│  ├─ docker-build-minikube                  │
│  ├─ local-deploy                           │
│  └─ local-verify                           │
└────────────────────┬────────────────────────┘
                     ↓
┌─────────────────────────────────────────────┐
│  make dev-logs / make dev-port-forward      │
│  查看应用日志和访问应用                      │
└─────────────────────────────────────────────┘
```

## 🔧 Makefile 命令参考

| 命令 | 说明 | 场景 |
|------|------|------|
| `make setup-local` | 初始化本地环境 | 首次设置 |
| `make local-pipeline` | 完整 CI/CD 流程 | 完整验证 |
| `make local-ci` | 仅运行 CI 检查 | 快速检查 |
| `make local-deploy` | 部署到 Minikube | 仅部署 |
| `make local-verify` | 验证部署 | 验证部署 |
| `make local-rollback` | 回滚部署 | 紧急回滚 |
| `make dev-logs` | 查看日志 | 日志调试 |
| `make dev-shell` | 进入 Pod | Pod 调试 |
| `make dev-port-forward` | 端口转发 | 访问应用 |
| `make dev-metrics` | 查看资源 | 性能监控 |
| `make minikube-clean` | 清理 Minikube | 重新开始 |
| `make full-reset` | 完全重置 | 彻底清理 |

## 📚 文档位置

- **快速参考**: `docs/quick-ref.md` ⭐ 优先阅读
- **详细指南**: `docs/local-dev.md`
- **部署配置**: `k8s/` 目录
- **脚本说明**: `scripts/` 目录

## 🎯 核心特性

✅ **一键启动**: `make setup-local` 完成所有配置
✅ **完整 CI/CD**: 从代码到部署全自动化
✅ **本地镜像**: 无需推送到远程仓库
✅ **快速反馈**: 完整流程 < 10 分钟
✅ **灵活调试**: 支持日志、shell、端口转发
✅ **快速回滚**: 一条命令回滚到上一个版本
✅ **多环境支持**: Dev/Staging/Prod 配置就绪
✅ **监控就绪**: Prometheus, Grafana, Jaeger 已配置

## 🔗 相关资源

- Minikube 文档: <https://minikube.sigs.k8s.io/>
- Kubernetes 文档: <https://kubernetes.io/docs/>
- Docker 文档: <https://docs.docker.com/>
- Go 测试文档: <https://golang.org/pkg/testing/>

## 💡 最佳实践

1. **定期运行 CI**: 每次修改后运行 `make local-pipeline`
2. **查看日志**: 部署失败时优先查看 Pod 日志
3. **资源监控**: 使用 `make dev-metrics` 监控资源使用
4. **备份配置**: 修改前备份 ConfigMap
5. **版本管理**: 使用 git tag 标记重要版本

## ❓ 常见问题

**Q: 首次设置需要多长时间？**
A: 约 5-10 分钟（取决于网络和硬件）

**Q: 能否同时运行多个环境？**
A: 可以，为每个环境创建不同的 Minikube 配置文件

**Q: 如何快速清理？**
A: 执行 `make full-reset`

**Q: 本地开发和云部署有什么区别？**
A: 本地使用 Docker 驱动，云使用 kubectl 连接真实集群

---

祝开发愉快！🎉

有任何问题，查看 `docs/local-dev.md` 获取更详细的说明。
