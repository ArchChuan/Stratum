# 依赖集成指南

## 本地依赖矩阵

| 服务 | Compose 镜像/实现 | 端口 | 主要用途 |
|------|-------------------|------|----------|
| PostgreSQL | 本地 `docker/postgres-zhparser.Dockerfile` | 5432 | public + per-tenant schema，知识库中文全文检索 |
| PgBouncer | `docker/pgbouncer.Dockerfile` | 6432 | PostgreSQL 连接池 |
| Redis | `redis:7-alpine` | 6379 | memory fact-extraction buffer、refresh-token blacklist |
| NATS | `nats:2.10.25-alpine` | 4222 / 8222 | Memory pipeline JetStream |
| etcd | `quay.io/coreos/etcd:v3.5.16` | 容器内 2379 | Milvus 元数据 |
| MinIO | `minio/minio` | 9000 / 9001 | Milvus 对象存储 |
| Milvus | `milvusdb/milvus:v2.4.15` | 19530 | Knowledge / Memory 向量检索 |
| OTEL Collector | contrib 0.96 | 4317 / 4318 | telemetry 收集 |
| Prometheus | v2.52 | 9090 | 指标存储 |
| Grafana | 11.0 | 3000 | 可视化 |
| Jaeger | 1.57 | 16686 | 链路查询 |

NATS 仅用于 Memory pipeline，其他 business domain 不直接依赖 `nats.Conn`。Milvus 依赖 etcd + MinIO，不能单独启动。

## 启动

```bash
make zhparser-build-local
make infra-up
make infra-wait
make infra-status
make obs-up      # 可选
make run
```

`make infra-up` 会启动基础设施及管理 UI，不启动 backend/frontend 应用容器。

## 健康检查

```bash
curl -fsS http://localhost:8080/health
docker compose ps
docker compose logs --tail=100 nats milvus postgres redis
```

`/health` 当前返回 `status` 和 `service` 字段；它是应用存活检查，不代表每个外部依赖都已就绪。

## 停止

```bash
make obs-down
make infra-down
```

Compose volume 默认保留。备份与数据重置见 [DATA_PERSISTENCE.md](DATA_PERSISTENCE.md)。

## 故障排查

- NATS：检查 4222 连接和 8222 monitoring。
- Milvus：先检查 etcd / MinIO，再查 Milvus；应用连接失败会 Warn 而不阻止启动。
- PostgreSQL：本地使用 zhparser 镜像；扩展缺失时 schema 会降级为 `simple` 分词。
- Redis：检查 6379 与 `redis-cli ping`。
- OTEL：确认 Collector 4317 可达；可观测性服务不是本地业务启动的必需条件。
