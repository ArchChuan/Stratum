# internal/workflow/infrastructure/executor

提供按节点类型注册和解析确定性 Workflow 节点执行器的注册表。

完整导入路径：`github.com/byteBuilderX/stratum/internal/workflow/infrastructure/executor`

注册表拒绝无效或重复注册；查找和执行边界由单元测试覆盖。
