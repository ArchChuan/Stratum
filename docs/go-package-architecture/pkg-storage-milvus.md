# pkg/storage/milvus

封装 Milvus 连接、集合/分区管理、向量写入、过滤搜索、关键词与混合检索，并提供通用 VectorIndex 适配器。

- 完整导入路径：`github.com/byteBuilderX/stratum/pkg/storage/milvus`

```mermaid
flowchart LR
  pkg["milvus 包<br/>pkg/storage/milvus"]
  api["核心类型 / 接口 / 函数<br/>VectorStore；VectorIndex；Index；Document/DocumentChunk；SearchHit/SearchResult；Connect；CreateCollection*；Insert；Search*；HybridSearch；Delete*"]
  subgraph source[非测试源码]
    f0["client.go"]
    f1["index.go"]
    f2["types.go"]
  end
  f0 -->|声明或实现| api
  f1 -->|声明或实现| api
  f2 -->|声明或实现| api
  api -->|组成包能力| pkg
  subgraph external[关键外部依赖]
    ex0["github.com/milvus-io/milvus-sdk-go/v2/client"]
    ex1["github.com/milvus-io/milvus-sdk-go/v2/entity"]
    ex2["go.uber.org/zap"]
  end
  pkg -->|直接 import| ex0
  pkg -->|直接 import| ex1
  pkg -->|直接 import| ex2
  tests["测试汇总<br/>vector_store_test.go, index_test.go"]
  tests -.->|验证| api
```

图中每个源码节点均对应 `go list -json` 返回的非测试 Go 文件；核心节点概括这些文件共同暴露或实现的主要架构表面。 当前包没有直接导入其他 stratum 项目包。 关键外部依赖为：`github.com/milvus-io/milvus-sdk-go/v2/client`、`github.com/milvus-io/milvus-sdk-go/v2/entity`、`go.uber.org/zap`。 测试文件合并为一个节点：`vector_store_test.go`、`index_test.go`。
