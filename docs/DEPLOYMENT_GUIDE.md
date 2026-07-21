# 部署指南

Stratum 当前提供三种部署入口：本地 Docker Compose、原始 Kubernetes manifests，以及 Helm Chart。生产或公开 demo 优先使用 Helm；本地开发使用 Compose。

## 前置要求

- Go 1.25（本地构建）
- Docker + Docker Compose
- Kubernetes 1.25+ 与 `kubectl`（Kubernetes 部署）
- Helm 3（Helm 部署）

## 本地开发

```bash
make zhparser-build-local
make infra-up
make infra-wait
make run       # backend :8080
make fe-dev    # frontend :3002，另开终端
```

可观测性服务单独启动：

```bash
make obs-up
```

详细流程见 [STARTUP_GUIDE.md](STARTUP_GUIDE.md) 和 [local-dev.md](local-dev.md)。

## 原始 Kubernetes manifests

`k8s/` 包含 namespace、依赖服务、backend/frontend、Ingress、监控和安全策略示例。部署前必须审查镜像、存储类、域名和 secret 引用：

```bash
make k8s-deploy
kubectl -n stratum get pods,svc,ingress
make k8s-logs
```

删除：

```bash
make k8s-delete
```

原始 manifests 更适合作为显式配置参考；当前完整部署说明见 [k8s-deployment.md](k8s-deployment.md)。

## Helm

Chart 位于 `helm/`，主要 values 文件为：

- `helm/values.yaml`：共享默认值
- `helm/values-dev.yaml`：开发环境
- `helm/values-demo.yaml` / `helm/values-demo-local.yaml`：HTTPS 与本地 demo
- `helm/values-demo-remote-http.yaml`：无域名远程 HTTP demo 的受约束覆盖层
- `helm/values-prod.yaml`：生产覆盖

部署前渲染并检查：

```bash
make helm-lint
make helm-diff
```

安装、升级和卸载：

```bash
make helm-install
make helm-upgrade
make helm-uninstall
```

Makefile 默认使用 `stratum` release 和 namespace；环境、镜像与 secret 配置以 Makefile 和 values 的当前定义为准。公开单机 demo 的完整操作见 [deployment/k3s-demo.md](deployment/k3s-demo.md)，Chart 字段说明见 [deployment/HELM_GUIDE.md](deployment/HELM_GUIDE.md)。

### 当前远程 HTTP Profile

当前公网入口为 <http://101.200.181.141:6879>，公网健康检查为
<http://101.200.181.141:6879/api/health>。该 profile 有意使用公网 IPv4 和明文 HTTP：

- 不要求域名或证书，部署校验也不应以缺少域名或 HTTPS 为由拒绝；
- `PUBLIC_BASE_URL` 必须是 `http://<public-ip>:6879`，不能带路径或尾随 `/`；
- K3s ServiceLB 在公网端口 `6879` 暴露 Traefik `web2` entrypoint；
- Traefik 将请求交给 frontend Service `:80`，Nginx 再把 `/api/*` 转发到 backend Service `:80`；
- 内部端口 `80` 不是公网访问地址，不能省略公网 URL 中的 `:6879`。

明文 HTTP 不提供传输加密，只适用于当前受控 Demo profile；不要在该环境提交敏感数据。

## 配置与 Secrets

Backend 的主要运行配置包括 PostgreSQL、Redis、NATS、Milvus、OTEL、OAuth/JWT、前端回调地址和 Memory pipeline 开关。准确字段与默认值以 `config/config.go`、Helm templates 和 `k8s/configmap.yaml` 为准。

敏感值必须通过 Kubernetes Secret 或外部 secrets manager 注入。不要在 values、ConfigMap、文档或前端 `.env` 中放入真实 password、token、API key、JWT 私钥或私有 URL。仓库中的 `k8s/secret.example.yaml` 仅用于展示键名。

LLM provider key 是 tenant-scoped 运行时设置，不是部署时共享的 `OPENAI_API_KEY`/`ANTHROPIC_API_KEY` 环境变量。当前运行时 provider 见 [LLM_INTEGRATION.md](LLM_INTEGRATION.md)。

## 数据与迁移

- public migrations：后端启动时从 `pkg/migration/sql/` 自动应用。
- tenant schema：由 `pkg/storage/postgres/tenant_schema.sql` provision。
- Milvus 依赖 etcd 与对象存储；生产部署需提供持久卷或外部服务。
- 删除 Helm release 不等于安全删除外部数据库；升级前按运行环境执行 PostgreSQL、Milvus 和对象存储备份。

详见 [DATA_PERSISTENCE.md](DATA_PERSISTENCE.md)。

## 验证与回滚

```bash
kubectl -n stratum rollout status deployment/stratum
kubectl -n stratum get pods,svc,ingress
kubectl -n stratum port-forward svc/stratum-frontend 18080:80
curl -fsS http://127.0.0.1:18080/api/health
```

远程 HTTP profile 还必须从集群外验证
`$PUBLIC_BASE_URL/api/health` 返回 HTTP 200。当前 CD workflow 先通过 frontend
Service 的 port-forward 验证集群内的 `/api/health` 代理链路，再检查公网入口；失败诊断会列出
Ingress 和 backend/frontend Service endpoints，以区分集群内路由与公网转发故障。

资源名称可能被 Helm fullname 覆盖；若命令中的名称不匹配，先用 `kubectl -n stratum get deploy,svc` 查询实际名称。

回滚 Helm release：

```bash
helm -n stratum history stratum
helm -n stratum rollback stratum <revision>
```
