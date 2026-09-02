# FE0 —— 错误文案抽取统一 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 web/src 服务端错误文案抽取收敛到唯一入口 `extractErrorMessage`，删除 14 个文件手写 `interface RequestError` 与局部包装。

**Architecture:** 共享入口 `web/src/shared/lib/errorMessage.ts` 的 `extractErrorMessage(err, fallback='操作失败')` 分支链 `data.error → data.message → err.message → fallback` 已覆盖原内联 `(err as RequestError).response?.data?.error || '<F>'`。本计划仅做逐站等价替换 + 清理，不改行为/文案/契约。

**Tech Stack:** TypeScript / React / Ant Design `message.error` / Vite / Vitest / ESLint。

## Global Constraints

- 每个替换点**保留原站点的 fallback 文案字面量与 `duration: 3`**，逐字不变。
- 不碰 `message.success`、`isForbidden` 静默分支、轮询路径。
- 统一 import：`import { extractErrorMessage } from '@/shared/lib';`，插入文件既有 import 分组规范位置（third-party 之后、内部别名分组，按各文件现状对齐）。
- `web/src/shared/lib/errorMessage.ts` 自身不得改动。
- 替换后仓库内不允许残留 `(…).response?.data?.error` 或 `interface RequestError`（本计划范围外文件 0 残留为验收硬门槛）。
- 新增代码遵循既有风格；本计划纯重构，不新增行为测试（行为契约由既有 vitest 守护），用仓库级 grep 守卫 + 全量校验代替。

---

## File Structure（改 19 文件）

统一两类操作，落到每个文件：

- **操作 A（message.error 单点）**：把该行
  `message.error({ content: (err as RequestError).response?.data?.error || '<F>', duration: 3 })`
  替换为
  `message.error({ content: extractErrorMessage(err, '<F>'), duration: 3 })`
  其中 `<F>` 是该行**原有** fallback 字面量（下表逐文件列出，缺失项以文件实际行为准并原样保留）。
- **操作 B（局部包装 + 手写类型）**：删除局部 `errorText/errorMessage/errorContent` 与
  `interface RequestError`，调用点改走 `extractErrorMessage`（见 Task 2/3/4 内完整代码）。

| 模块 | 文件 | 操作 | 站点 fallback 字面量（按出现顺序） |
|---|---|---|---|
| memory | hooks/useEntitiesTab.ts | A + 删接口 | '加载实体失败', '删除实体失败' |
| memory | hooks/useEntriesTab.ts | A + 删接口 | 以文件实际为准（2 处） |
| memory | hooks/useFactsTab.ts | A + 删接口 | '加载事实失败', '删除事实失败', '更新事实失败' |
| memory | hooks/useSnapshotsTab.ts | A + 删接口 | '加载快照失败', '更新快照失败', '清空快照失败' |
| memory | hooks/useSummariesTab.ts | A + 删接口 | '加载摘要失败', '删除摘要失败' |
| memory | hooks/useMyMemoriesPage.ts | A + 删接口 | '加载记忆统计失败', '清空记忆失败' |
| workflow | hooks/useWorkflowExecution.ts | A + 删接口 | 两处均 '操作失败'（其一含 `\|\| (error as Error).message`，删除该分支由共享链覆盖） |
| workflow | hooks/useWorkflowResources.ts | B | `errorText(reason)` ×3，默认 '操作失败' |
| workflow | pages/WorkflowDetailPage.tsx | A + 删接口 | '操作失败' |
| workflow | pages/WorkflowVersionPage.tsx | A + 删接口 | '操作失败' |
| workflow | components/WorkflowRunActions.tsx | A + 删接口 | 以文件实际为准（1 处） |
| workflow | components/WorkflowManualInterventionPanel.tsx | A | '操作失败'（内联对象 cast） |
| workflow | components/WorkflowApprovalPanel.tsx | A | '操作失败'（内联对象 cast） |
| operation-gate | hooks/useOperationProposals.ts | A + 删接口 | '加载待审批列表失败','加载审批历史失败','加载操作详情失败','开始审批失败','批准失败','拒绝失败','取消失败'（7 处） |
| approvals | hooks/useApprovalsPage.ts | B | 见 Task 3 代码（6 个 message.error 调用点） |
| audit | hooks/useAuditListPage.ts | A + 删接口 | '加载审计记录失败', '加载审计详情失败' |
| agent | hooks/useResourceChangeProposal.ts | B | `errorContent(error)` ×5，fallback '操作失败' |
| evaluation | components/ReviewPoolPanel.tsx | A | 以文件实际为准（3 处） |
| shared | hooks/useRequestEditorAccess.ts | A | '操作失败' |

---

### Task 1: memory 模块 6 文件

**Files:**

- Modify: `web/src/modules/memory/hooks/useEntitiesTab.ts`, `useEntriesTab.ts`, `useFactsTab.ts`, `useSnapshotsTab.ts`, `useSummariesTab.ts`, `useMyMemoriesPage.ts`
- Test: 运行该模块既有 vitest

- [ ] **Step 1: 逐文件迁移**

每个文件：删除底部 `interface RequestError { response?: { data?: { error?: string } } }`（若替换后无引用）；
`catch` 内每个 `message.error({ content: (err as RequestError).response?.data?.error || '<F>', duration: 3 })`
替换为 `message.error({ content: extractErrorMessage(err, '<F>'), duration: 3 })`，`<F>` 用上表对应字面量。
补充 import（按文件既有分组插入）：

```ts
import { extractErrorMessage } from '@/shared/lib';
```

以 useSummariesTab.ts 第 28 行为例（替换结果）：

```ts
      message.error({ content: extractErrorMessage(err, '加载摘要失败'), duration: 3 });
```

注意：只替换 `message.error` 的内容表达式，`duration`/分支结构/`if (seq !== …)` 守卫一律不动。

- [ ] **Step 2: 验证模块测试**
Run: `cd web && npx vitest run src/modules/memory`
Expected: PASS（若该模块无可运行用例 vitest 报 "No test files"，记入注释并继续，最终 Task 5 全量兜底）。

- [ ] **Step 3: 类型/编译**
Run: `cd web && npx tsc --noEmit`
Expected: 无错误。

- [ ] **Step 4: Commit**

```bash
git add web/src/modules/memory/hooks
git commit -m "refactor(fe): memory 模块错误抽取统一走 shared extractErrorMessage"
```

---

### Task 2: workflow 模块 6 文件（含 useWorkflowResources 包装）

**Files:**

- Modify: `web/src/modules/workflow/hooks/useWorkflowExecution.ts`, `useWorkflowResources.ts`, `web/src/modules/workflow/pages/WorkflowDetailPage.tsx`, `WorkflowVersionPage.tsx`, `web/src/modules/workflow/components/WorkflowRunActions.tsx`, `WorkflowManualInterventionPanel.tsx`, `WorkflowApprovalPanel.tsx`
- Test: 运行该模块既有 vitest

- [ ] **Step 1: 5 个直接点按操作 A 替换**

`useWorkflowExecution.ts` 两处：第 31 行原含 `…response?.data?.error || (error as Error).message || '操作失败'`，
统一替换为 `message.error({ content: extractErrorMessage(error, '操作失败'), duration: 3 })`（共享链已含 `err.message` 分支，故删去该子分支）；第 47 行同样替换。替换后删除该文件 `interface RequestError`。
pages/components 共 4 个单点按上表 fallback（'操作失败'）替换；`WorkflowManualInterventionPanel.tsx` 与 `WorkflowApprovalPanel.tsx` 是内联对象 cast（`(error as { response?: … })…`），同样替换为 `extractErrorMessage(error, '操作失败')`；`WorkflowRunActions.tsx` 单点以文件实际 fallback 为准并删其 `interface RequestError`。
5 个文件补充 `import { extractErrorMessage } from '@/shared/lib';`。

- [ ] **Step 2: useWorkflowResources.ts 删包装**

删除第 9 行 `interface RequestError` 与第 11 行局部 `errorText`：

```ts
interface RequestError { response?: { data?: { error?: string } } }
const errorText = (error: unknown) => (error as RequestError).response?.data?.error || '操作失败';
```

三处调用点（agentResult/skillResult/mcpResult 的 rejected 分支）替换为：

```ts
message.error({ content: extractErrorMessage(agentResult.reason), duration: 3 });
```

（共享默认 fallback '操作失败' 与原 `errorText` 语义一致；skill/mcp 分支同理。）
补充 import `extractErrorMessage`。

- [ ] **Step 3: 验证 + Commit**
Run: `cd web && npx vitest run src/modules/workflow` → PASS（无用例同上备注）。
Run: `cd web && npx tsc --noEmit` → 无错误。

```bash
git add web/src/modules/workflow
git commit -m "refactor(fe): workflow 模块错误抽取统一走 shared extractErrorMessage"
```

---

### Task 3: operation-gate + approvals（含包装）

**Files:**

- Modify: `web/src/modules/operation-gate/hooks/useOperationProposals.ts`, `web/src/modules/approvals/hooks/useApprovalsPage.ts`
- Test: `src/modules/operation-gate`、`src/modules/approvals` 既有 vitest

- [ ] **Step 1: operation-gate 7 单点**

`useOperationProposals.ts` 7 处 `message.error` 按上表 fallback 逐个替换（每个 `<F>` 不同，见 File Structure 表）。删除文件内 `interface RequestError`，补 import。

- [ ] **Step 2: approvals 删包装**

删除第 11 行 `interface RequestError` 与第 15-16 行局部 `errorMessage`：

```ts
interface RequestError { response?: { data?: { error?: string } } }

const errorMessage = (err: unknown, fallback: string): string =>
  (err as RequestError).response?.data?.error || fallback;
```

文件内 6 个 `errorMessage(err, '<F>')` 调用点替换为 `extractErrorMessage(err, '<F>')`，
`<F>` 依次为：'加载待审批列表失败'、'加载审批历史失败'、'加载可指派成员失败'、
'加载审批详情失败'、'操作失败'（runAction）、'指派失败'。补 import。

- [ ] **Step 3: 验证 + Commit**
Run: `cd web && npx vitest run src/modules/operation-gate src/modules/approvals` → PASS。
Run: `cd web && npx tsc --noEmit` → 无错误。

```bash
git add web/src/modules/operation-gate web/src/modules/approvals
git commit -m "refactor(fe): operation-gate/approvals 错误抽取统一走 shared extractErrorMessage"
```

---

### Task 4: audit + agent（含包装）+ evaluation

**Files:**

- Modify: `web/src/modules/audit/hooks/useAuditListPage.ts`, `web/src/modules/agent/hooks/useResourceChangeProposal.ts`, `web/src/modules/evaluation/components/ReviewPoolPanel.tsx`
- Test: `src/modules/audit`、`src/modules/agent`、`src/modules/evaluation` 既有 vitest

- [ ] **Step 1: audit 两单点**

`useAuditListPage.ts`：'加载审计记录失败'、'加载审计详情失败' 两处按操作 A 替换；删除文件内 `interface RequestError`，补 import。

- [ ] **Step 2: agent 删包装**

`useResourceChangeProposal.ts`：删除第 11-14 行局部 `errorContent`：

```ts
const errorContent = (error: unknown) => {
  const value = error as { response?: { data?: { error?: string } } };
  return value.response?.data?.error || '操作失败';
};
```

5 个 `message.error({ content: errorContent(error), duration: 3 })` 调用点（load 的 catch、
useEffect 的 catch、saveDraft/confirm/cancel 的 catch）替换为
`message.error({ content: extractErrorMessage(error, '操作失败'), duration: 3 })`。
补 import。

- [ ] **Step 3: evaluation ReviewPoolPanel 3 单点**

按操作 A 替换 3 处；fallback 以文件实际字面量为准并保留。文件无 `interface RequestError`（如替换后确认无手写类型则无需删）。补 import。

- [ ] **Step 4: 验证 + Commit**
Run: `cd web && npx vitest run src/modules/audit src/modules/agent src/modules/evaluation` → PASS。
Run: `cd web && npx tsc --noEmit` → 无错误。

```bash
git add web/src/modules/audit web/src/modules/agent web/src/modules/evaluation
git commit -m "refactor(fe): audit/agent/evaluation 错误抽取统一走 shared extractErrorMessage"
```

---

### Task 5: shared 自举 + 仓库守卫 + 全量校验

**Files:**

- Modify: `web/src/shared/hooks/useRequestEditorAccess.ts`
- Test: 全量 `make fe-lint fe-typecheck fe-test fe-build`

- [ ] **Step 1: shared/hooks 自举**

`useRequestEditorAccess.ts:21` 现有 `message.error({ content: error?.response?.data?.error || '操作失败', duration: 3 })`
替换为 `message.error({ content: extractErrorMessage(error, '操作失败'), duration: 3 })`。补 import。

- [ ] **Step 2: 仓库级守卫（硬门槛，全库 0 残留）**
Run（在分支根目录）：

```bash
echo "A) 残留 response?.data?.error（应仅 shared/lib/errorMessage.ts 自身）:"
grep -rn "response?\.data?\.error" web/src --include="*.ts" --include="*.tsx" | grep -v "\.test\." | grep -v "shared/lib/errorMessage.ts" || echo "NONE"
echo "B) 残留 interface RequestError（应为空）:"
grep -rn "interface RequestError" web/src --include="*.ts" --include="*.tsx" | grep -v "\.test\." || echo "NONE"
```

Expected: A) 输出 `NONE`；B) 输出 `NONE`。若仍有残留，回到对应文件补齐替换（不允许跳过）。

- [ ] **Step 3: 全量校验**
Run: `make fe-lint && make fe-typecheck && make fe-test && make fe-build`
Expected: 全部 PASS。

- [ ] **Step 4: Commit**

```bash
git add web/src/shared/hooks/useRequestEditorAccess.ts
git commit -m "refactor(fe): shared/hooks 错误抽取自举统一走 extractErrorMessage"
```

若前序任务已把改动分 commit 提交，本任务仅为收尾；若守卫暴露遗漏，随收尾 commit 一并修复。

---

## Self-Review 结论

- **Spec 覆盖**：Spec「A/B 类映射 + C 类核对」全部落进 Task 1-5；收尾清理（删接口、补 import、
  守卫 0 残留）覆盖 Spec「收尾清理」；验证分级（R0/R1 最小档 + 对照 verification.yaml 复核）由 Task 5 全量校验承担。
- **占位符**：无 TODO/TBD；所有 B 类文件给出完整代码；A 类给出来自审计 grep 的逐站 fallback 表，
  未在 grep 中捕获的 3 处（entries/runactions/reviewpool）标注"以文件实际为准"，属确定性保留而非占位。
- **类型一致**：仅涉及 `extractErrorMessage` 单函数（签名 `(err: unknown, fallback?: string) => string`）与
  `message.content: string`，前后一致。
