# internal/llmgateway/infrastructure/embedding

该包把 llmgateway 的嵌入客户端包装成面向文本列表的嵌入服务，并固定或覆盖所用模型。

完整导入路径：`github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure/embedding`

```mermaid
flowchart LR
  service["embedding.go<br/>EmbeddingService<br/>NewEmbeddingService / NewEmbeddingServiceWithModel<br/>EmbedVector·EmbedBatch·GetVectorDimension"]
  gateway["internal/llmgateway/infrastructure<br/>EmbeddingClient / EmbeddingRequest"]
  constants["pkg/constants<br/>默认模型/超时"]
  log["外部：zap"]
  tests["测试汇总<br/>embedding_test.go"]
  service --> gateway
  service --> constants
  service --> log
  service -.-> tests
```

`EmbedVector` 为单段文本调用 `CreateEmbeddings`；`EmbedBatch` 按客户端 `BatchSize` 顺序分批，并为每批设置超时；`GetVectorDimension` 按模型名返回维度。两个构造函数分别使用默认模型或调用方指定模型。
