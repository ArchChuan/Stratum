# 完整启动指南

## 当前运行组成

Stratum 本地开发由三部分组成：

- 基础设施：PostgreSQL、PgBouncer、Redis、NATS JetStream、Milvus、etcd、MinIO
- 后端：Go/Gin 服务，默认监听 `8080`
- 前端：React/Vite 开发服务器，默认监听 `3002`

NATS 当前只承载 Memory pipeline。LLM 运行时 provider 为 Qwen 和 Zhipu，API key 与默认模型按租户配置；不要把真实 key 写入 `.env`、前端配置或文档。

## 前置条件

- Go 1.25（以 `go.mod` 为准）
- Docker + Docker Compose
- Node.js/npm（仅前端开发需要）
- Make

## 启动步骤

```bash
cd /home/yang/go-projects/stratum

# 首次运行或 PostgreSQL 镜像定义变化后构建中文分词镜像
make zhparser-build-local

# 启动并等待基础设施
make infra-up
make infra-wait

# 终端 1：后端
make run

# 终端 2：前端
make fe-dev
```

后端启动时自动应用 `pkg/migration/sql/` 的 public migrations，并为租户 provision `pkg/storage/postgres/tenant_schema.sql`。

如需 Prometheus、Grafana、Jaeger 和 OTEL Collector：

```bash
make obs-up
```

也可用 `make dev-up` 同时启动基础设施和可观测性服务，但 backend/frontend 仍需分别运行。

## 验证

```bash
make infra-status
curl -fsS http://localhost:8080/health
curl -fsS http://localhost:8080/models
```

健康检查当前返回：

```json
{"service":"Stratum","status":"ok"}
```

打开前端：<http://localhost:3002>。除 `/health`、`/metrics`、`/models` 外，业务 API 均需要相应的 JWT、tenant context 和角色权限；不要用无鉴权 curl 示例判断业务接口是否可用。

## 常用检查

```bash
# 后端快速校验
go vet ./...
go test -short ./...

# 前端校验
make fe-lint
make fe-build

# 查看依赖日志
docker compose logs --tail=100 postgres redis nats milvus
```

## 停止服务

```bash
make obs-down
make infra-down
```

`docker compose down -v` 会删除本地持久卷，只能在明确接受数据丢失时使用。

## 常见问题

### 基础设施未就绪

```bash
make infra-status
docker compose ps
docker compose logs --tail=200 postgres redis nats milvus
```

Milvus 依赖 etcd 和 MinIO；应先确认三者均健康。Memory pipeline 只有在 `MEMORY_PIPELINE_ENABLED=true` 时启动。

### 登录路由未注册

Auth 路由只有在 `GITHUB_CLIENT_ID` 非空且 JWT 私钥可用时注册。检查本地配置是否提供开发环境所需的 OAuth/JWT 设置，但不要把私钥或凭证提交到仓库。

### 前端无法访问后端

确认后端在 `8080`、前端在 `3002`，并检查 `FRONTEND_URL` 与 Vite 代理配置。当前默认 `FRONTEND_URL` 为 `http://localhost:3002`。

## 相关文档

- [本地开发](local-dev.md)
- [依赖集成](DEPENDENCIES.md)
- [LLM 集成](LLM_INTEGRATION.md)
- [快速开始：LLM](QUICKSTART_LLM.md)
- [数据持久化](DATA_PERSISTENCE.md)
