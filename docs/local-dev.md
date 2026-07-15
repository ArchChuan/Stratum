# 本地开发与 CI 快速开始

> 当前仓库不再提供 Minikube 专用脚本或 `make local-*` 目标。本地开发以 Docker Compose + Makefile 为准；Kubernetes 验证使用 `k8s/` 或 `helm/`。

## 前置条件

- Go 1.25
- Node.js 20+ 与 npm
- Docker Engine + Docker Compose v2
- 可选：`kubectl` 1.28+ / Helm 3.13+

## 本地启动

```bash
# 首次或 postgres zhparser 镜像变更后
make zhparser-build-local

# PostgreSQL / PgBouncer / Redis / NATS / Milvus / 管理 UI
make infra-up
make infra-wait

# 后端，http://localhost:8080
make run

# 另一终端启动前端，http://localhost:3002
make fe-dev
```

如需 Prometheus、Grafana、Jaeger 和 OTEL Collector：

```bash
make obs-up
```

## 日常验证

```bash
go vet ./...
go test -short ./...
make fe-lint
make fe-build
```

PR 前完整验证：

```bash
go test -v -race -timeout 30s ./...
```

Makefile 的 `make be-test` 额外生成覆盖率并对 80% 阈值做强制检查；GitHub Actions 主 CI 会上传覆盖率产物，当前只对低于 80% 发出 warning。

## 容器与 Kubernetes

```bash
make be-docker-build
make fe-docker-build

# 原生 manifests
make k8s-deploy

# Helm 预检 / 部署
make helm-lint
make helm-install IMAGE_TAG=<tag>
```

当前远程部署链路为 `.github/workflows/deploy.yml` → 镜像仓库 → `helm/values-demo.yaml` → K3s。不要把本地 `docker-compose.yml` 当作远程部署配置。

## 停止与清理

```bash
make obs-down
make infra-down
make clean
```

`make clean` 会删除构建产物与前端缓存，不会删除 Docker volume。如需清空数据，请先按 [DATA_PERSISTENCE.md](DATA_PERSISTENCE.md) 备份。
