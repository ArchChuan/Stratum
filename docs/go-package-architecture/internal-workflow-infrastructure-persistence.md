# internal/workflow/infrastructure/persistence

使用 tenant PostgreSQL schema 实现 Workflow 的统一持久化 store。

完整导入路径：`github.com/byteBuilderX/stratum/internal/workflow/infrastructure/persistence`

该 store 负责定义、运行、事件、审批和租约相关 SQL，并以集成测试验证事务行为。
