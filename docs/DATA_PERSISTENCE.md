# 数据持久化指南

## 概述

Stratum 使用四种存储组件，均通过 Docker volume 持久化：

1. **PostgreSQL** — 主关系数据库（用户、租户、Agent、Skill 版本、Evaluation、Knowledge 元数据、执行 trace/checkpoint、Memory facts/snapshots/history）
2. **NATS JetStream** — 异步消息队列（memory pipeline）
3. **Milvus** — 向量存储（embedding + RAG 检索），依赖 etcd 和 MinIO
4. **Redis** — 运行时状态（memory fact-extraction buffer、refresh-token blacklist）

---

## 1. PostgreSQL（主数据库）

### 数据卷挂载

```yaml
# docker-compose.yml
volumes:
  - postgres_data:/var/lib/postgresql/data
```

**特点**：

- 多租户 schema 隔离：`public` 保存全局表（tenants、users），每个租户有独立 `tenant_<id>` schema
- 迁移由 `pkg/migration/sql/` 下编号 SQL 文件管理，启动时自动应用（当前最高版本：025）
- 租户级表（skill_revisions、evaluation_jobs、eval_runs、evaluation_experiments、agent_tool_traces、agent_execution_checkpoints、knowledge_parent_chunks、memory_active_snapshots、memory_summaries 等）由 `pkg/storage/postgres/tenant_schema.sql` 定义，`ProvisionAllTenantSchemas` 在各 `tenant_<id>` schema 中创建
- pgx v5 连接池，支持 `SET LOCAL search_path` 切换租户 schema

### 备份

```bash
docker exec stratum-postgres-1 pg_dump -U stratum stratum > backup.sql
```

### 恢复

```bash
docker exec -i stratum-postgres-1 psql -U stratum stratum < backup.sql
```

---

## 2. NATS JetStream（事件总线）

### 数据卷挂载

```yaml
volumes:
  - nats_data:/data
```

**特点**：

- 启用 JetStream 模式（`-js`），存储目录 `-sd /data`
- 事件消息持久化到磁盘，容器重启后消息不丢失
- 使用的 Stream：`MEMORY_RAW`（subject `memory.raw.>`）/ `MEMORY_ENRICHED`（`memory.enriched.>`）/ `MEMORY_DLQ`（死信，TTL 168h）

### 验证

```bash
docker exec stratum-nats-1 nats stream list
```

### 备份 / 清理

```bash
# 备份
docker run --rm -v stratum_nats_data:/data -v $(pwd):/backup \
  alpine tar czf /backup/nats_backup.tar.gz -C /data .

# 清理（开发环境重置）
docker-compose down
docker volume rm stratum_nats_data
docker-compose up -d nats
```

---

## 3. Milvus（向量存储）

Milvus 依赖 etcd（元数据）和 MinIO（对象存储），三个组件均需持久化。

### 数据卷挂载

```yaml
# Milvus 本体
volumes:
  - milvus_data:/var/lib/milvus

# etcd
volumes:
  - etcd_data:/bitnami/etcd

# MinIO
volumes:
  - minio_data:/minio_data
```

**Collection 命名规范**（`pkg/storage/tenantnaming/milvus.go`）：

- 知识库：每租户单 collection `tenant_{tenant_id}_kb`，workspace 以 partition 隔离
- 记忆消息：`memory_{tenant_id}`；事实向量：`memory_facts_{tenant_id}`

tenant ID 中的连字符会替换为下划线；workspace partition 对不符合 Milvus 标识符规则的 ID 使用稳定哈希。

### 备份

```bash
# MinIO 数据（向量原始数据）
docker run --rm -v stratum_minio_data:/data -v $(pwd):/backup \
  alpine tar czf /backup/minio_backup.tar.gz -C /data .

# etcd 元数据
docker run --rm -v stratum_etcd_data:/data -v $(pwd):/backup \
  alpine tar czf /backup/etcd_backup.tar.gz -C /data .
```

### 恢复

```bash
docker run --rm -v stratum_minio_data:/data -v $(pwd):/backup \
  alpine tar xzf /backup/minio_backup.tar.gz -C /data

docker run --rm -v stratum_etcd_data:/data -v $(pwd):/backup \
  alpine tar xzf /backup/etcd_backup.tar.gz -C /data
```

### 故障排查

```bash
# 检查 etcd 和 MinIO 日志
docker-compose logs etcd
docker-compose logs minio

# 重启顺序：etcd → minio → milvus
docker-compose restart etcd minio
docker-compose restart milvus
```

### 清理（开发环境重置）

```bash
docker-compose down
docker volume rm stratum_milvus_data stratum_minio_data stratum_etcd_data
docker-compose up -d etcd minio milvus
```

---

## 4. Redis（缓存与缓冲）

### 数据卷挂载

```yaml
volumes:
  - redis_data:/data
```

**用途**：

- Memory buffer：消费 NATS 消息后按 `MemoryBufferFlushSize=5` 批量写入 PG
- refresh token 注销黑名单

`TenantGatewayCache` 是进程内 TTL cache，不存于 Redis。Redis volume 通常不作为业务主数据备份，但丢失会清空尚未 flush 的 memory buffer 和 refresh-token blacklist；重置前应评估在途数据与已注销 token 的安全窗口。

---

## 5. 完整数据卷清单

```bash
docker volume ls | grep stratum
```

| Volume | 对应服务 | 是否需要备份 |
|---|---|---|
| `stratum_postgres_data` | PostgreSQL | 是（业务数据） |
| `stratum_nats_data` | NATS JetStream | 按需（未处理消息） |
| `stratum_milvus_data` | Milvus | 随 MinIO |
| `stratum_etcd_data` | etcd（Milvus 依赖） | 随 MinIO |
| `stratum_minio_data` | MinIO（Milvus 后端） | 是（向量数据） |
| `stratum_redis_data` | Redis | 否（运行时缓存） |

---

## 6. 数据卷位置（Linux/WSL）

```bash
docker volume inspect stratum_postgres_data
# "Mountpoint": "/var/lib/docker/volumes/stratum_postgres_data/_data"
```

---

## 7. 故障恢复

### PostgreSQL 无法启动

```bash
docker logs stratum-postgres-1
docker-compose down
docker volume rm stratum_postgres_data
docker-compose up -d postgres
# 注意：此操作会清除所有业务数据
```

### Milvus 连接失败

```bash
# 确认 etcd 和 minio 已就绪
docker-compose logs etcd
docker-compose logs minio
docker-compose restart etcd minio
docker-compose restart milvus
```

### NATS 数据损坏

```bash
docker-compose down
docker volume rm stratum_nats_data
docker-compose up -d nats
# 注意：未消费消息将丢失
```

---

## 8. 性能调优

### etcd 自动压缩

```yaml
environment:
  ETCD_AUTO_COMPACTION_MODE: revision
  ETCD_AUTO_COMPACTION_RETENTION: 1000
  ETCD_QUOTA_BACKEND_BYTES: 4294967296  # 4GB
```

### MinIO 存储策略

```yaml
environment:
  MINIO_BROWSER: on
  MINIO_STORAGE_CLASS_STANDARD: EC:2
```
