# pkg/crypto

提供基于 SHA-256 派生密钥的 AES-256-GCM 字符串加解密。

- 完整导入路径：`github.com/byteBuilderX/stratum/pkg/crypto`

```mermaid
flowchart LR
  pkg["crypto 包<br/>pkg/crypto"]
  api["核心类型 / 接口 / 函数<br/>DeriveAESKey；Encrypt；Decrypt"]
  subgraph source[非测试源码]
    f0["aes.go"]
  end
  f0 -->|声明或实现| api
  api -->|组成包能力| pkg
  subgraph external[关键外部依赖]
    ex0["crypto/aes"]
    ex1["crypto/cipher"]
    ex2["crypto/sha256"]
  end
  pkg -->|直接 import| ex0
  pkg -->|直接 import| ex1
  pkg -->|直接 import| ex2
  tests["测试汇总<br/>aes_test.go"]
  tests -.->|验证| api
```

图中每个源码节点均对应 `go list -json` 返回的非测试 Go 文件；核心节点概括这些文件共同暴露或实现的主要架构表面。 当前包没有直接导入其他 stratum 项目包。 关键外部依赖为：`crypto/aes`、`crypto/cipher`、`crypto/sha256`。 测试文件合并为一个节点：`aes_test.go`。
