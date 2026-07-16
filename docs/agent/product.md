# 产品设计原则与规范

> 前端 / 产品任务适用。

## 核心

意图优先——用户目标是"让 AI 完成任务"，不是"配置参数"。技术参数折叠进高级设置，首屏 ≤3 个决策点。

## 信息层级

- 列表页 ≤5 列（名称 + 状态 + 1 个关键指标）
- 表单首屏 ≤8 项，基础展开 + 高级折叠

## AI 可解释

- 执行中：流式输出 + 工具调用步骤可见
- 执行后：步骤折叠面板（工具名 + 耗时 + 摘要）
- 失败必须定位到具体步骤

## 用户分层

管理员（配置）vs 终端用户（对话）：管理操作必须二次确认；终端用户界面不暴露配置入口。

## 实体约束

- **知识库**：`description` 必填（直接影响 AI 检索判断）；`name` 创建后不可改（向量 collection 绑定）
- **Agent**：max_iterations slider 1-20；知识库绑定展示各自 description
- **技能**：以版本化 instruction bundle 编辑 capability、activation、instructions 与 requirements；发布后由 Agent 激活，不提供独立执行入口
- **记忆**：当前用户入口提供清空本人记忆；后端同时提供添加、查询、删除、会话清理与统计 API

## 交互三态

进行中 → 按钮 loading / Skeleton；成功 → `message.success` ≤2s；失败 → `message.error` 不自动消失。

## 空状态

所有列表必须有 Empty + 引导操作；无数据 → "X 还是空的"；搜索无结果 → "没有找到…"。

## 危险操作

删除 / 停用 / 清空用 `Modal.confirm` 并描述后果；必填用 `rules` 不用星号；`extra` 说明字段、`tooltip` 补充，不重叠。
