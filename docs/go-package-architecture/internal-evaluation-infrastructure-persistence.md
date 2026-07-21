# internal/evaluation/infrastructure/persistence

使用 tenant PostgreSQL schema 实现评估上下文的 suite、run、job、优化、实验和反馈仓储。

完整导入路径：`github.com/byteBuilderX/stratum/internal/evaluation/infrastructure/persistence`

该包承担 SQL、租约更新和数据库错误传播；关键仓储包含单元或集成测试。
