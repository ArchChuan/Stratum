# 依赖集成指南

本文档说明 Stratum 如何集成各个底层依赖。

## 架构概览

```
┌─────────────────────────────────────────────────┐
│         Stratum 应用                    │
├─────────────────────────────────────────────────┤
│  API Layer (Gin)                                │
├─────────────────────────────────────────────────┤
│  Agent / Skill / Memory / LLM Gateway           │
├─────────────────────────────────────────────────┤
│  RAG | VectorStore | Config                     │
├─────────────────────────────────────────────────┤
│  NATS | Milvus | PostgreSQL | Redis | OTEL      │
└─────────────────────────────────────────────────┘
```

## 依赖服务

### 1. NATS (事件总线)

**用途**: 异步事件驱动通信

**Docker 镜像**: `nats:2.10.25-alpine`

**端口**: 4222

**集成代码** (`pkg/messaging/nats.go`, memory pipeline 用 JetStream `nats.go` 包):

```go
nc, err := nats.Connect("nats://localhost:4222")
js, _ := nc.JetStream()
// 发布到 JetStream subject
js.Publish("memory.raw", data)
```

**启动检查**:

```bash
# 检查连接
nc -z localhost 4222

# 查看日志
docker logs stratum-nats-1
```

### 3. Milvus (向量数据库)

**用途**: 向量存储和相似度搜索

**Docker 镜像**: `milvusdb/milvus:v2.4.15`

**端口**: 19530

**集成代码** (`pkg/vector/vector_store.go`):

```go
vectorStore := mcp.NewVectorStore("localhost", "19530", logger)
err := vectorStore.Connect(ctx)
defer vectorStore.Close()

// 插入向量
vectors := [][]float32{
    {0.1, 0.2, 0.3},
    {0.4, 0.5, 0.6},
}
vectorStore.Insert(ctx, "embeddings", vectors)

// 搜索
results, err := vectorStore.Search(ctx, "embeddings", []float32{0.1, 0.2, 0.3}, 10)
```

**启动检查**:

```bash
# 检查连接
nc -z localhost 19530

# 查看日志
docker logs stratum-milvus-1
```

### 4. OpenTelemetry Collector (可观测)

**用途**: 收集和导出指标、日志、链路

**Docker 镜像**: `otel/opentelemetry-collector:latest`

**端口**: 4317 (gRPC)

**集成代码** (`pkg/observability/logger.go`):

```go
logger, err := observability.NewLogger("production")
defer logger.Sync()

// 记录指标
metrics := observability.NewMetrics(logger)
metrics.RecordSkillExecution("skill-1", 123.45, true)
metrics.RecordAPIRequest("POST", "/skills", 201, 45.67)
```

**启动检查**:

```bash
# 检查连接
nc -z localhost 4317

# 查看日志
docker logs stratum-otel-collector-1
```

## 启动流程

### 方式 1: 使用启动脚本（推荐）

```bash
# 启动所有服务
./start.sh

# 停止所有服务
./stop.sh
```

### 方式 2: 手动启动

```bash
# 1. 启动 Docker 容器
docker-compose up -d

# 2. 等待服务启动
sleep 10

# 3. 构建应用
go build -o bin/server ./cmd/server

# 4. 运行应用
./bin/server
```

### 方式 3: 使用 Makefile

```bash
# 启动依赖
make docker-up

# 运行应用
make run

# 停止依赖
make docker-down
```

## 健康检查

### 应用健康检查

```bash
curl http://localhost:8080/health
# 响应: {"status":"ok"}
```

### 依赖服务检查

```bash
# NATS
nc -z localhost 4222 && echo "NATS OK" || echo "NATS FAILED"

# Neo4j

# Milvus
nc -z localhost 19530 && echo "Milvus OK" || echo "Milvus FAILED"

# OTEL
nc -z localhost 4317 && echo "OTEL OK" || echo "OTEL FAILED"
```

## 故障排除

### NATS 连接失败

```bash
# 检查容器状态
docker ps | grep nats

# 查看日志
docker logs stratum-nats-1

# 重启容器
docker-compose restart nats
```

### Milvus 连接失败

```bash
# 检查容器状态
docker ps | grep milvus

# 查看日志
docker logs stratum-milvus-1

# 重启容器
docker-compose restart milvus
```

### 应用启动失败

```bash
# 查看应用日志
./bin/server

# 检查环境变量
env | grep -E "NATS|NEO4J|MILVUS|OTEL"

# 检查端口占用
lsof -i :8080
lsof -i :4222
lsof -i :7687
lsof -i :19530
```

## 环境变量配置

编辑 `.env` 文件：

```env
# 应用
PORT=8080

# NATS
NATS_URL=nats://localhost:4222


# Milvus
MILVUS_HOST=localhost
MILVUS_PORT=19530

# OpenTelemetry
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317

# LLM
OPENAI_API_KEY=sk-your-key
ANTHROPIC_API_KEY=sk-ant-your-key
OLLAMA_ENDPOINT=http://localhost:11434
```

## 性能优化

### 1. 连接池

所有客户端都使用连接池，自动管理连接复用。

### 2. 缓存

可在应用层实现缓存：

```go
type CachedGraphRAG struct {
    *GraphRAG
    cache map[string]interface{}
}
```

### 3. 批量操作

Milvus 支持批量插入向量：

```go
vectorStore.Insert(ctx, "collection", batchVectors)
```

## 监控和日志

### 查看应用日志

```bash
# 实时日志
./bin/server

# 后台运行并保存日志
./bin/server > app.log 2>&1 &
tail -f app.log
```

### 查看 Docker 日志

```bash
# 所有容器
docker-compose logs -f

# 特定容器
docker-compose logs -f nats
docker-compose logs -f milvus
```

### 指标收集

应用自动记录：

- Skill 执行时间和成功率
- API 请求延迟和状态码
- 事件发布数量

## 下一步

- 查看 [LLM 集成指南](LLM_INTEGRATION.md)
- 查看 [快速开始](QUICKSTART_LLM.md)
- 查看 [README.md](../README.md)
