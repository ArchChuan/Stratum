# CI/CD 指南

## 当前 Workflows

| 文件 | 触发 | 职责 |
|------|------|------|
| `.github/workflows/ci.yml` | `main` push / PR | 迁移边界护栏、golangci-lint、race 测试与覆盖率、构建、`go vet`、gosec |
| `.github/workflows/deploy.yml` | `main` push、`v*` tag、手动 | 构建镜像、准备依赖镜像、Helm 部署 K3s、rollout 验证 |
| `.github/workflows/memory-e2e.yml` | 以 workflow 定义为准 | Memory pipeline 专项端到端验证 |

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

`make ci-backend` 会组合上述后端步骤并启动依赖，但它还会执行 `go fmt` 和
`gofmt -s -w` 改写源码；只在准备接受格式化变更时使用。

`ci.yml` 使用 Go 1.25 和 golangci-lint v2.12.2。覆盖率低于 80% 在 GitHub Actions 中仅告警；Makefile 的 `be-test` 会直接失败。

## 部署所需 GitHub Secrets

`deploy.yml` 通过 GitHub Secrets 读取镜像仓库、Kubernetes、SSH、数据库、JWT 和 OAuth 所需的敏感值。具体名称以 workflow 中的 `${{ secrets.* }}` 引用为准。

- 不在文档中复制真实 secret 值。
- 不把已解码 kubeconfig 或私钥作为 artifact 上传。
- 轮换凭据后应手动触发一次 workflow 验证。

## 部署链路

```text
GitHub Actions
  → 并行测试并构建 backend/frontend SHA 镜像
  → 进入 stratum-production 串行部署区
  → 过期 main SHA 门禁
  → 发布固定版本依赖镜像并解析全部镜像 digest
  → Kubernetes namespace / Secret / registry pull secret
  → 以 repository@digest 执行 Helm upgrade
  → rollout status + Pod/Ingress/Service endpoint diagnostics
  → frontend Service 内部 /api/health + 公网 /api/health 双重验证
```

本地 `docker-compose.yml` 不在该链路中。基础设施版本或自定义镜像变更时，必须同时核对 Compose、`deploy.yml` 和 Helm values。

生产变更使用 GitHub Actions job-level concurrency 串行执行，且不会取消正在运行的 Helm
操作。`main` push 在修改镜像仓库或集群前必须再次核对远端最新 SHA；已过期任务正常跳过。
生产镜像由 registry digest 固定，禁止使用 `latest` 作为部署契约。

部署后的健康检查先将 `stratum-frontend` Service 的 80 端口转发到 runner 的
`127.0.0.1:18080`，验证 frontend 对 `/api/health` 的集群内代理；随后请求
`$PUBLIC_BASE_URL/api/health` 并要求 HTTP 200。前者失败指向 Pod/Service/代理链路，后者失败
则应结合 workflow 输出的 Ingress 与 Service endpoints 排查公网转发或 Ingress。

## 已知不一致

`go.mod` 与主 CI 使用 Go 1.25，而 `deploy.yml` 当前仍指定 Go 1.22。文档不将其描述为已对齐；这一差异需在后续代码/流水线任务中处理。
