# Stratum Helm Chart 部署指南

## Chart 结构

```text
helm/
├── Chart.yaml
├── values.yaml
├── values-dev.yaml
├── values-demo-local.yaml
├── values-demo-remote-http.yaml
├── values-demo.yaml
├── values-prod.yaml
└── templates/
    ├── deployment.yaml
    ├── frontend-deployment.yaml
    ├── service.yaml / frontend-service.yaml
    ├── ingress.yaml
    ├── configmap.yaml / frontend-configmap.yaml
    ├── secret.yaml
    ├── dependencies.yaml
    ├── hpa.yaml / pdb.yaml
    └── networkpolicy.yaml
```

Chart 当前通过 `templates/dependencies.yaml` 直接渲染 PostgreSQL、Redis、NATS、etcd、MinIO 和 Milvus，不使用 `Chart.yaml` dependencies 或 `helm/charts/` 子 Chart 包。

## 验证

```bash
helm lint ./helm -f helm/values.yaml
helm template stratum ./helm -f helm/values-demo.yaml >/dev/null
helm lint ./helm \
  -f helm/values-demo.yaml \
  -f helm/values-demo-remote-http.yaml
```

Makefile 入口：

```bash
make helm-lint
make helm-diff
```

## 安装与升级

```bash
helm upgrade --install stratum ./helm \
  -f helm/values-demo.yaml \
  --set app.image.repository=<backend-image> \
  --set app.image.tag=<tag> \
  --set frontend.image.repository=<frontend-image> \
  --set frontend.image.tag=<tag> \
  -n stratum --create-namespace --wait --timeout=10m
```

```bash
helm history stratum -n stratum
helm rollback stratum <revision> -n stratum
helm uninstall stratum -n stratum
```

## Values 选择

| 文件 | 用途 |
|------|------|
| `values.yaml` | Chart 基线与 Makefile 默认值 |
| `values-dev.yaml` | 开发环境覆盖 |
| `values-demo-local.yaml` | 本地 demo 覆盖 |
| `values-demo.yaml` | 远程单机 K3s demo 的 HTTPS 基线 |
| `values-demo-remote-http.yaml` | 当前无域名环境的受约束 HTTP 覆盖 |
| `values-prod.yaml` | 生产导向覆盖，包括外部依赖选项 |

当前 `deploy.yml` 按顺序加载 `values-demo.yaml` 和
`values-demo-remote-http.yaml`。公网链路为 `:6879 -> 主机 :80 -> Traefik
web`，Kubernetes Service 仍使用 80，后端进程仍使用 8080。公网地址由
GitHub Production Environment 的 `PUBLIC_BASE_URL=http://<public-ip>:6879`
注入，禁止将真实 IP 写入 values 文件。

HTTP profile 会关闭 secure cookie，因为浏览器不会通过 HTTP 回传带
`Secure` 标记的 cookie。该环境没有传输加密；申请域名和证书后，应停止
加载 HTTP overlay，使用 `values-demo.yaml` 的 TLS/`websecure` 配置并恢复
secure cookie。

不要在 values 文件中提交真实密码、token、API key、JWT 私钥或 OAuth secret。生产应由外部 Secret manager 或部署流水线注入。

`templates/deployment.yaml` 当前仍声明一个 optional `OPENAI_API_KEY` Secret 引用；`config/config.go` 不读取该环境变量，LLM key 实际由租户 settings 管理。部署时无需把真实 provider key 放入共享 Kubernetes Secret。

## 镜像与依赖

`deploy.yml` 会把 backend / frontend 以 commit SHA 作为 tag，并在 Helm 命令中通过 `--set` 覆盖镜像。PostgreSQL 使用由 `docker/postgres-zhparser.Dockerfile` 构建的自定义镜像；其他依赖由 workflow 镜像化到目标 registry。

## 健康与容量

- backend 健康检查：`/health`
- HPA：`templates/hpa.yaml`，由 values 开关控制
- PDB：`templates/pdb.yaml`，由 values 开关控制
- NetworkPolicy：`templates/networkpolicy.yaml`
- 单节点 demo 不提供真正高可用；详见 [k3s-demo.md](k3s-demo.md)
