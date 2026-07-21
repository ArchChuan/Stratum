# internal/evaluation/application

评估应用层编排 suite 发布、异步 job 执行、优化候选、实验阶段判定和反馈写入。

完整导入路径：`github.com/byteBuilderX/stratum/internal/evaluation/application`

该包只依赖 evaluation 领域模型和端口；worker 通过租约领取任务，并把执行结果和失败状态写回仓储。单元测试覆盖各服务和 worker 主路径。
