# internal/workflow/application

编排 Workflow 定义发布、DAG 调度、ready set、运行租约、暂停/恢复/取消、审批和人工介入。

完整导入路径：`github.com/byteBuilderX/stratum/internal/workflow/application`

控制状态机由 Go 代码确定性执行，不由模型决策。服务、运行时与调度行为均有测试覆盖。
