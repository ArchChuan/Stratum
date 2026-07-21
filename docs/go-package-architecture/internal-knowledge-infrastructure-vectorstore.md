# internal/knowledge/infrastructure/vectorstore

将 `pkg/storage/milvus` 的通用向量索引适配为 knowledge 领域端口所需的 VectorStore。

完整导入路径：`github.com/byteBuilderX/stratum/internal/knowledge/infrastructure/vectorstore`

该包只做接口形状转换，集合命名、搜索和删除行为委托底层向量索引实现。
