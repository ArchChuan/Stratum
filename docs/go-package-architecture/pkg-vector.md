# pkg/vector

向后兼容地重导出 storage/milvus 的向量存储类型与构造函数。

- 完整导入路径：`github.com/byteBuilderX/stratum/pkg/vector`

```mermaid
flowchart LR
  pkg["vector 包<br/>pkg/vector"]
  api["核心类型 / 接口 / 函数<br/>VectorStore/DocumentChunk/SearchResult/MCPRequest/MCPResponse 别名；NewVectorStore"]
  subgraph source[非测试源码]
    f0["types.go"]
    f1["vector_store.go"]
  end
  f0 -->|声明或实现| api
  f1 -->|声明或实现| api
  api -->|组成包能力| pkg
  subgraph projectdeps[直接项目依赖]
    pd0["pkg/storage/milvus"]
  end
  pkg -->|直接 import| pd0
  subgraph external[关键外部依赖]
    ex0["go.uber.org/zap"]
  end
  pkg -->|直接 import| ex0
```

图中每个源码节点均对应 `go list -json` 返回的非测试 Go 文件；核心节点概括这些文件共同暴露或实现的主要架构表面。 项目内箭头仅表示当前包的直接 import，包含：`pkg/storage/milvus`。 关键外部依赖为：`go.uber.org/zap`。
