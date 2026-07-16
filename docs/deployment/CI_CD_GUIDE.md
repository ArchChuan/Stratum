# CI/CD 指南

## 当前 Workflows

| 文件 | 触发 | 职责 |
|------|------|------|
| `.github/workflows/ci.yml` | `main` push / PR | 迁移边界护栏、golangci-lint、race 测试与覆盖率、构建、`go vet`、gosec |
| `.github/workflows/deploy.yml` | `main` push、`v*` tag、手动 | 构建镜像、准备依赖镜像、Helm 部署 K3s、rollout 验证 |
| `.github/workflows/memory-e2e.yml` | 以 workflow 定义为准 | Memory pipeline 专项端到端验证 |
| `.github/workflows/mirror.yml` | 以 workflow 定义为准 | 仓库镜像/同步任务 |

仓库当前没有 GitLab CI 配置，也没有 `.github/workflows/ci-cd.yml`。

## 本地对齐主 CI

```bash
make migration-guardrails
make be-lint
go test -v -race -coverprofile=coverage.out ./... -timeout=5m
go build -o bin/server ./cmd/server
go vet ./...
make fe-lint
make fe-build
```

`ci.yml` 使用 Go 1.25 和 golangci-lint v2.12.2。覆盖率低于 80% 在 GitHub Actions 中仅告警；Makefile 的 `be-test` 会直接失败。

## 部署所需 GitHub Secrets

`deploy.yml` 通过 GitHub Secrets 读取镜像仓库、Kubernetes、SSH、数据库、JWT 和 OAuth 所需的敏感值。具体名称以 workflow 中的 `${{ secrets.* }}` 引用为准。

- 不在文档中复制真实 secret 值。
- 不把已解码 kubeconfig 或私钥作为 artifact 上传。
- 轮换凭据后应手动触发一次 workflow 验证。

## 部署链路

```text
GitHub Actions
  → backend/frontend 镜像
  → PostgreSQL+zhparser 与其他依赖镜像
  → Kubernetes namespace / Secret / registry pull secret
  → helm upgrade --install ./helm -f helm/values-demo.yaml
  → rollout status + pod/event diagnostics
```

本地 `docker-compose.yml` 不在该链路中。基础设施版本或自定义镜像变更时，必须同时核对 Compose、`deploy.yml` 和 Helm values。

## 已知不一致

`go.mod` 与主 CI 使用 Go 1.25，而 `deploy.yml` 当前仍指定 Go 1.22。文档不将其描述为已对齐；这一差异需在后续代码/流水线任务中处理。
