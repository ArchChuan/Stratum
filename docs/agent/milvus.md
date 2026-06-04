# Milvus Development Rules

## SDK Version

当前使用 `github.com/milvus-io/milvus-sdk-go/v2` v2.4.2。

核心封装位于 `pkg/vector/vector_store.go`（`VectorStore` struct）。

## VectorStore API

```go
vs := vector.NewVectorStore(host, port, logger, dim)
vs.Connect(ctx)                           // 带 net.Dialer 2s 端口预检 + gRPC 连接
vs.CreateCollection(ctx, name, dim)       // 创建集合（含 primary key schema）
vs.Insert(ctx, name, ids, vectors, meta)  // 插入后自动 Flush
vs.Search(ctx, name, vector, topK)        // 语义相似度搜索
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
- 插入后调用 `Flush` 确保持久化（`VectorStore.Insert` 内部已处理）
- 创建 Collection 时必须指定含主键字段的 schema

## Connection 处理

`pkg/vector/vector_store.go` 使用 `net.Dialer` 预检端口可达性（2s 超时），防止 SDK 无限阻塞。连接在独立 goroutine 中执行，通过 channel + `ctx.Done()` 实现超时控制。

`router.go` 中连接使用 3s 整体超时：

```go
ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
defer cancel()
vectorStore.Connect(ctx)  // 失败仅 Warn，不阻塞启动
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
