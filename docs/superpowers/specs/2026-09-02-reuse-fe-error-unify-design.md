# 设计文档：FE0 —— 前端错误文案抽取统一

- 日期：2026-09-02
- 分支：`feat/reuse-fe-error-unify`
- 关联审计：web/src 组件/Hook 复用审计（FE0）
- 状态：待用户评审

## 背景与问题

`web/src` 中服务端错误文案抽取存在两套写法并行：

1. **共享入口（已存在，被 45 文件使用）**：`web/src/shared/lib/errorMessage.ts` 的
   `extractErrorMessage(err, fallback = '操作失败')`，分支链为
   `response.data.error → response.data.message → err.message → fallback`。
2. **模块内联写法**：`(err as RequestError).response?.data?.error || '<站点文案>'`，
   分布在 19 个文件、37 处文本内联站点（不含共享实现自身），其中 14 个文件各自手写同构的
   `interface RequestError` 或局部 `errorText`/`errorMessage`/`errorContent` 包装；部分
   文件的局部包装在 1 处定义后被多处 `message.error` 调用点复用。

同一错误语义在代码库中同时存在两种实现 → 读取路径漂移风险 + 未来改造需双处同步。

## 目标

把服务端错误文案抽取收敛到**唯一入口** `extractErrorMessage`；消除全部模块内联 cast 与手写
`interface RequestError`/局部包装。

## 非目标

- 不改 `message.error` 的 `duration: 3` 与各站点 fallback 文案（保持每站文案不变）。
- 不改成功提示（`message.success`）、不改 `isForbidden` 静默语义。
- 不新增统一 fallback/文案常量（属于后续 FE3/FE4 范畴）。
- 不改任何请求/响应类型契约。

## 方案

逐站将 `(err as RequestError).response?.data?.error || '<F>'` 替换为
`extractErrorMessage(err, '<F>')`。`extractErrorMessage` 的分支顺序覆盖原内联分支：
后端正常返回 `error` 字段时结果**逐字一致**；仅当响应无 `error` 字段却带 `message` 或抛普通
`Error` 时，共享版会展示更真实的底层文案（见"行为保持"）。

### 变更映射（分类）

**A 类 —— `message.error` 单点直接替换（保留各站 fallback 与 duration）**

- `web/src/modules/memory/hooks/useEntitiesTab.ts`（2 处）
- `web/src/modules/memory/hooks/useEntriesTab.ts`（2 处）
- `web/src/modules/memory/hooks/useFactsTab.ts`（3 处）
- `web/src/modules/memory/hooks/useSummariesTab.ts`（2 处）
- `web/src/modules/memory/hooks/useSnapshotsTab.ts`（3 处）
- `web/src/modules/memory/hooks/useMyMemoriesPage.ts`（2 处）
- `web/src/modules/workflow/pages/WorkflowDetailPage.tsx`（1 处）
- `web/src/modules/workflow/pages/WorkflowVersionPage.tsx`（1 处）
- `web/src/modules/workflow/components/WorkflowRunActions.tsx`（1 处）
- `web/src/modules/workflow/components/WorkflowManualInterventionPanel.tsx`（1 处）
- `web/src/modules/workflow/components/WorkflowApprovalPanel.tsx`（1 处）
- `web/src/modules/workflow/hooks/useWorkflowExecution.ts`（2 处；含 `|| (error as Error).message` 特例，由共享分支链覆盖）
- `web/src/modules/operation-gate/hooks/useOperationProposals.ts`（7 处）
- `web/src/modules/audit/hooks/useAuditListPage.ts`（2 处）
- `web/src/modules/evaluation/components/ReviewPoolPanel.tsx`（3 处）
- `web/src/shared/hooks/useRequestEditorAccess.ts`（1 处，共享层自举统一）

**B 类 —— 删除局部包装 + 手写类型，改 import `extractErrorMessage`**

- `web/src/modules/approvals/hooks/useApprovalsPage.ts`：删 `interface RequestError` 与
  局部 `errorMessage(err, fallback)`；调用点改 `extractErrorMessage(err, '<F>')`。
- `web/src/modules/workflow/hooks/useWorkflowResources.ts`：删 `interface RequestError` 与
  局部 `errorText(error)`；3 处调用点改 `extractErrorMessage(reason)`（默认 fallback
  `'操作失败'` 与原语义一致）。
- `web/src/modules/agent/hooks/useResourceChangeProposal.ts`：删局部 `errorContent(error)`；
  5 处调用点改 `extractErrorMessage(error, '操作失败')`。

**C 类 —— 已核对语义、统一纳入共享**

- 上述三处局部包装在核对后均与 `extractErrorMessage` 语义等价（均取 `response.data.error`
  后回落站点文案），统一走共享入口。

**收尾清理**：替换后移除不再使用的 `interface RequestError` 声明（涉及约 14 文件）；
`import { extractErrorMessage } from '@/shared/lib'` 按各文件既有 import 分组规范补齐；
无残留内联 `response?.data?.error`（`shared/lib/errorMessage.ts` 自身除外）。

## 行为保持

- 正常路径（后端响应含 `error` 字段）：新旧文案逐字一致。
- 边缘路径差异（可接受、更优）：后端仅返回 `data.message` 或抛普通 `Error` 时，共享版
  展示底层 `message` 而非站点 fallback。FE 已约定 `{"error": ...}` 为冻结响应契约，
  因此该分支主要兜底非标准来源，属期望的统一而非回归。
- 不改任何成功/轮询/静默路径。

## 测试与验证

- 改动文件均为 hooks/components 的错误分支；测试代码中 0 处引用内联写法（已核）。
- 执行：`make fe-lint`、`make fe-build`；对改动模块跑 vitest（memory/workflow/
  operation-gate/approvals/audit/evaluation/agent 相关 `*.test.ts(x)`）。
- 验证分级：FE 纯错误文案重构、正常路径行为不变、不涉数据库/能力/权限链路 → 判 R0/R1
  最小档（fe-lint/fe-build/相关单测），不触发浏览器 E2E。该判级在进入实现时对照
  `.test/verification.yaml` 复核，若命中更高档则显式升级，不静默降级。

## 风险与回滚

- 风险极低：语法级机械替换；每处替换是单表达式等价映射。
- 回滚：单 commit 移除即可；PR 保持一个关注点，便于 revert。
