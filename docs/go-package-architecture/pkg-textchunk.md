# pkg/textchunk

提供文本清洗、去重和递归、语义、结构化切块策略，以及旧版 Chunker API。

- 完整导入路径：`github.com/byteBuilderX/stratum/pkg/textchunk`

```mermaid
flowchart LR
  pkg["textchunk 包<br/>pkg/textchunk"]
  api["核心类型 / 接口 / 函数<br/>TextChunk；Chunker；TextCleaner；Strategy；Embedder；ChunkResult；RecursiveStrategy；SemanticStrategy；StructureRecursiveStrategy"]
  subgraph source[非测试源码]
    f0["chunker.go"]
    f1["cleaner.go"]
    f2["recursive.go"]
    f3["semantic.go"]
    f4["strategy.go"]
    f5["structure.go"]
  end
  f0 -->|声明或实现| api
  f1 -->|声明或实现| api
  f2 -->|声明或实现| api
  f3 -->|声明或实现| api
  f4 -->|声明或实现| api
  f5 -->|声明或实现| api
  api -->|组成包能力| pkg
  subgraph external[关键外部依赖]
    ex0["go.uber.org/zap"]
  end
  pkg -->|直接 import| ex0
  tests["测试汇总<br/>chunker_test.go, cleaner_test.go"]
  tests -.->|验证| api
```

图中每个源码节点均对应 `go list -json` 返回的非测试 Go 文件；核心节点概括这些文件共同暴露或实现的主要架构表面。 当前包没有直接导入其他 stratum 项目包。 关键外部依赖为：`go.uber.org/zap`。 测试文件合并为一个节点：`chunker_test.go`、`cleaner_test.go`。
