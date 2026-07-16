# Kubernetes 部署指南

Stratum 保留两套 Kubernetes 入口：

- `k8s/`：原生 manifests，适合本地或手工验证。
- `helm/`：当前远程 K3s 和多环境部署的主要入口。

## 前置条件

- Kubernetes 1.28+
- kubectl
- Helm 3.13+（Helm 路径）
- 可用的 backend / frontend 镜像
- 在集群 Secret 管理中配置的运行时凭据

不要把真实 kubeconfig、JWT key、OAuth secret、数据库密码或镜像仓库凭据写入 `docs/` 或提交到 Git。

## 原生 manifests

```bash
kubectl apply -f k8s/
kubectl get pods -n clawhermes-system
kubectl get services -n clawhermes-system
```

Makefile 等价入口：

```bash
make k8s-deploy
make k8s-logs
make k8s-delete
```

`k8s/secret.example.yaml` 仅是字段示例；部署前应通过外部 Secret manager、Sealed Secrets 或命令行注入真实值。

原始 manifests 尚保留 `NEO4J_PASSWORD`、`OPENAI_API_KEY`、`JWT_SECRET` 等 legacy 环境变量名，而当前后端读取 `POSTGRES_URL`、`REDIS_URL`、`JWT_PRIVATE_KEY_PEM` 等配置，LLM key 则来自 tenant settings。因此直接应用前必须按 `config/config.go` 校正 ConfigMap/Secret；Helm 是当前更接近运行时契约的入口。

## Helm

```bash
make helm-lint

helm upgrade --install stratum ./helm \
  -f helm/values-demo.yaml \
  --set app.image.repository=<backend-image> \
  --set app.image.tag=<tag> \
  --set frontend.image.repository=<frontend-image> \
  --set frontend.image.tag=<tag> \
  -n stratum --create-namespace --wait --timeout=10m
```

环境文件：

- `helm/values-dev.yaml`
- `helm/values-demo-local.yaml`
- `helm/values-demo.yaml`
- `helm/values-prod.yaml`

详细参数见 [deployment/HELM_GUIDE.md](deployment/HELM_GUIDE.md)。

## 远程 CD

`.github/workflows/deploy.yml` 在 `main` push、`v*` tag 或手动触发时：

1. 运行短测与 `go vet`。
2. 构建并推送 backend / frontend 镜像。
3. 构建 PostgreSQL + zhparser，并镜像化其他依赖。
4. 通过 GitHub Secrets 配置集群访问和 Kubernetes Secrets。
5. 执行 `helm upgrade --install` 部署到 K3s。
6. 输出 Pod / Deployment / Event 诊断并检查 rollout。

注意：主 CI 与 `go.mod` 使用 Go 1.25；当前 `deploy.yml` 的测试/构建 job 仍显式指定 Go 1.22。这是仓库当前存在的工具链不一致，升级依赖后应优先检查该 workflow。

## 验证与回滚

```bash
kubectl get pods -n <namespace>
kubectl rollout status deployment/<deployment> -n <namespace>
kubectl logs deployment/<deployment> -n <namespace> --tail=100

kubectl rollout history deployment/<deployment> -n <namespace>
kubectl rollout undo deployment/<deployment> -n <namespace>
```

单机演示环境的限制、备份和 DNS 说明见 [deployment/k3s-demo.md](deployment/k3s-demo.md)。
