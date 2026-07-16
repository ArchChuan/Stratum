# Milvus Development Rules

## SDK Version

当前使用 `github.com/milvus-io/milvus-sdk-go/v2` v2.4.2。

核心封装：

- `pkg/storage/milvus/client.go` — `VectorStore` 主实现（Knowledge 与 Memory 共用）
- `pkg/vector/` — 旧 import path 的兼容 re-export；Knowledge/Memory 的部分现有代码仍通过该路径引用
- `internal/memory/infrastructure/pipeline/vector_adapter.go` — Memory pipeline 专用适配器，封装 tenant-scoped collection 命名与批量写入

## VectorStore API

```go
vs := milvus.NewVectorStore(host, port, logger)
vs.Connect(ctx)                           // 带 net.Dialer 2s 端口预检 + gRPC 连接
vs.CreateCollectionWithDim(ctx, name, dim)// 创建集合（含 primary key schema）
vs.Insert(ctx, name, docs, partition)     // 批量插入 DocumentChunk
vs.Flush(ctx, name)                       // 需要持久化可见性时显式 Flush
vs.Search(ctx, name, vector, topK)        // 可选 variadic partitions
vs.DeleteCollection(ctx, name)            // 删除集合
```

## Search 参数顺序

```go
client.Search(
    ctx,
    collectionName,
    partitions,   // []string{}
    expr,         // 过滤表达式
    outputFields,
    vectors,
    vectorField,
    metricType,   // entity.MetricType
    topK,
    searchParams, // entity.NewIndexFlatSearchParam()
)
```

- `searchParams`：使用 `entity.NewIndexFlatSearchParam()`，返回 `(SearchParam, error)`
- 结果得分：使用 `result.Scores`，不要用 `result.Core`

## Collection 操作规范

- 搜索前必须调用 `LoadCollection`
- 插入与 `Flush` 是两个 API；需要立即持久化/可见性的调用链必须显式调用 `Flush`
- 创建 Collection 时必须指定含主键字段的 schema

## Connection 处理

`pkg/storage/milvus/client.go` 使用 `net.Dialer` 预检端口可达性（2s 超时），防止 SDK 无限阻塞。连接在独立 goroutine 中执行，通过 channel + `ctx.Done()` 实现超时控制；`ensureConnected` 负责惰性重连并用锁保护 client 生命周期。

连接由 `api/wiring/storage.go` 在容器构建阶段发起，沿用 `BuildContainer` 的启动 context；`VectorStore` 自身的 TCP 预检超时为 2 秒：

```go
vectorStore.Connect(ctx)  // 失败记录 Warn；后续调用仍会尝试 ensureConnected
```

## Common Errors

| 错误 | 原因 | 解决 |
|------|------|------|
| `collection not loaded` | 搜索前未 Load | 调用 `LoadCollection` |
| `field not found` | schema 字段名不匹配 | 检查 `CreateCollection` 中的字段定义 |
| `dimension mismatch` | 向量维度与 schema 不符 | 确保 embedding 维度与集合定义一致 |
| `Milvus port not reachable` | 服务未启动或网络不通 | 检查 `MILVUS_HOST:MILVUS_PORT` 配置 |

## Migration Notes

升级 Milvus SDK 版本时：

1. 检查 `Search` API 签名是否变化
2. 检查 `entity` 包类型是否重命名
3. 运行 `go build ./...` 验证编译
4. 更新 `docs/agent/project.md` 中的版本记录
