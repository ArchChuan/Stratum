# 本地 CI/CD 现状

> 历史说明：早期仓库曾规划 Minikube 专用脚本、`make local-*` 目标和 `charts/clawhermes-ai/`。这些文件与目标已不存在，不应继续使用。

## 当前实现

- 本地依赖：`docker-compose.yml` + `make infra-*`
- 本地可观测性：`make obs-*`
- 后端 CI：`.github/workflows/ci.yml`（迁移护栏、lint、race 测试、构建、安全扫描）
- 远程构建与部署：`.github/workflows/deploy.yml`
- Kubernetes：`k8s/`
- Helm Chart：`helm/`，环境 values 为 `values-dev.yaml` / `values-demo.yaml` / `values-prod.yaml`

## 本地 CI

```bash
make migration-guardrails
make be-lint
go test -v -race -timeout 30s ./...
make fe-lint
make fe-build
```

`make ci-backend` 会启动基础设施并执行完整后端链路；它包含 `go fmt` / `gofmt -s -w`，会改写源码，因此只应在准备接受格式化变更时运行。

## 部署预检

```bash
make helm-lint
make helm-diff
```

`helm-diff` 需要 helm-diff 插件；Makefile 在缺失时会尝试安装。详细本地流程见 [local-dev.md](local-dev.md)，命令速查见 [quick-ref.md](quick-ref.md)。
