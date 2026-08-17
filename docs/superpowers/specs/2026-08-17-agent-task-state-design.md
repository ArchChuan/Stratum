# Agent 持久化 Task 状态设计（跨会话推进同一目标）

日期：2026-08-17
状态：已确认

## 1. 背景与问题

存在一个已确认的产品需求：**单个 agent 的目标需要多轮对话、多次 execution 持续推进**。用户中途被打断（关浏览器、换会话、隔天再来）后，agent 应能从上次进度继续推进，而不是从零重来。

当前能力矩阵：

| 层级 | 机制 | 作用域 | 现状 |
|---|---|---|---|
| 执行级 | `agent_execution_checkpoints` + `resumeFromCheckpoint` | 同 execution 断线续接 | ✅ 已存在 |
| 编排级 | `workflow_runs` + claim/lease | 同 workflow 多 worker | ✅ 已存在 |
| **目标级** | **`agent_tasks`（本设计）** | **跨 execution、跨会话推进同一目标** | 🆕 缺失 |

缺的是目标级：用户在会话 A 说"推进订单服务迁移"，agent 建 plan 干了一半；用户会话 B 再来，agent 不知道有个未完成目标。本设计补这一层。

## 2. 目标与非目标

### 目标

1. 持久化"跨会话推进同一目标"的 task 状态：goal、current_phase、completed_steps、next_action。
2. 新会话（同 agent + 同 user）可恢复活跃 task 并从上次进度继续，无需用户重新描述目标。
3. 并发防护：同一 task 不被多个会话同时推进（沿用仓库已验证模式）。
4. 生命周期解耦：单个会话删除不丢失 task。

### 非目标

- 不做 task 的 UI 列表/管理页面（本期仅对话内提示）。
- 不自动加载旧会话完整历史（跨会话恢复靠 task 摘要，完整历史属 checkpoint 语义）。
- 不为纯对话（无 plan）建 task。
- 不引入 plan 触发的硬编码轮数主逻辑。轮数至多作为可配置兜底信号（权重低，见 §7.3），绝不作为主触发。

## 3. 方案对比与选定

| 方案 | 说明 | 结论 |
|---|---|---|
| 复用 `collab.task_steps` | 已有 claim/generation/lease 先例，但绑定 collaboration 语义，与 agent 目标语义不符 | ❌ |
| 扩展 checkpoint 字段 | checkpoint 是执行级（execution_id 唯一），跨 execution 的目标状态混入会破坏执行级语义 | ❌ |
| conversation 元数据 | conversation 是会话级，task 跨会话，绑会话即断 | ❌ |
| **独立 `agent_tasks` 新表** | 目标级独立实体，owner = (tenant, agent, user)，与 conversation 解耦 | ✅ 选定 |

**选定理由**：task 是 (tenant, agent, user) 三元组的独立实体，与 `chat_conversations` 同构但生命周期独立；满足全部澄清约束（跨对话推进、应用层自动提取、对话内提示、自然语言+声明绑定继续、从 Plan 映射、执行结束自动建/更新）。

## 4. 数据模型

新表落 `pkg/storage/postgres/tenant_schema.sql`（tenant-only DDL 唯一基线，禁止复制到 `pkg/migration/sql/`）。历史租户兼容：`CREATE TABLE IF NOT EXISTS`。

```sql
CREATE TABLE IF NOT EXISTS agent_tasks (
    id                   TEXT        PRIMARY KEY,
    agent_id             TEXT        NOT NULL,
    user_id              TEXT        NOT NULL,
    goal                 TEXT        NOT NULL DEFAULT '',
    current_phase        TEXT        NOT NULL DEFAULT '',
    completed_steps      JSONB       NOT NULL DEFAULT '[]',
    next_action          TEXT        NOT NULL DEFAULT '',
    status               TEXT        NOT NULL CHECK (status IN ('active','completed','abandoned')),
    -- 并发防护三字段（复用 workflow_runs / task_steps 先例）
    claimed_by           TEXT        NOT NULL DEFAULT '',
    lease_expires_at     TIMESTAMPTZ,
    generation           BIGINT      NOT NULL DEFAULT 0,
    -- 会话引用：软引用，无 FK 级联，仅用于"最近推进会话"定位
    last_conversation_id UUID,
    last_execution_id    TEXT        NOT NULL DEFAULT '',
    fail_count           INT         NOT NULL DEFAULT 0,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at           TIMESTAMPTZ NOT NULL DEFAULT NOW() + INTERVAL '30 days'
);
CREATE INDEX IF NOT EXISTS idx_agent_tasks_owner
    ON agent_tasks (agent_id, user_id, updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_agent_tasks_status
    ON agent_tasks (status, expires_at);
```

字段语义：

- `owner` 维度：`(agent_id, user_id)` **不设唯一约束**——同一 agent+user 允许多个活跃 task（用户决策）。`id` 由提取时生成（UUID 派生，一个 plan 一个稳定 id）。
- 恢复入口：`GetLatestActiveForOwner(tenantID, agentID, userID)` 按 `updated_at DESC` 返回最新活跃 task（多个活跃 task 并存，恢复指向最近推进的那个）。
- `last_conversation_id` 是**软引用**（UUID 可空，与 checkpoint 的 conversation_id 同风格）。会话删除只使其悬空，不级联删 task。不做 FK `ON DELETE CASCADE`。
- `status` 三态：`active`（可推进）、`completed`（用户明确完成，停更）、`abandoned`（保留进度但不再推荐恢复）。
- `expires_at`：task 自身保留策略（默认 30 天），独立于 chat 的 deleted_at+30 天策略；活跃 task 每次更新顺带续期。

## 5. 并发防护（三层）

复用仓库已验证的 `workflow_runs` / `task_steps` 模式（`claimed_by + lease_expires_at + generation`）。

1. **原子 Claim**：推进前条件更新，抢占或续约：

```sql
UPDATE agent_tasks
   SET claimed_by = $conversationID,
       lease_expires_at = NOW() + INTERVAL '30 minutes',
       generation = generation + 1,          -- bump fence：接管后旧会话 stale 写被拒
       updated_at = NOW()
 WHERE id = $taskID
   AND status = 'active' AND expires_at > NOW()  -- 只 claim 可推进且未回收的 task
   AND (claimed_by = $conversationID        -- 本会话续约
        OR claimed_by = ''                  -- 无主抢占
        OR lease_expires_at < NOW());       -- lease 过期接管
```

1. **generation 乐观锁**：claim 时 bump（见上）；写回 task 内容时携带 claim 后读取到的 `generation`，`WHERE id=$1 AND generation=$old`，冲突则 `ErrGenerationConflict` 重试/放弃。fence 依赖 claim 已 bump——lease 过期被接管后，旧会话 stale 写命中旧 generation 被拒。
2. **lease 自动失效**：无 heartbeat。task 推进是离散的（每 execution 写一次），lease 仅用于防悬挂 + 允许接管；会话崩溃/被杀后 30 分钟自动释放，新会话可接管。

**失败语义**：claim 失败（另一会话正在推进）→ 本会话降级为只读（不写 task 内容），WARN 记录，不打断当前 execution。

## 6. 生命周期

task 生存依赖是 (tenant, agent, user)，**不是 conversation**。会话删除只解除引用。

| 事件 | task 处置 |
|---|---|
| execution 结束（有 plan） | 建/更新 task，claim 刷新，`expires_at` 顺延 |
| conversation 删除 | **解除引用，task 保留 active**——`claimed_by = ''`、`lease_expires_at = NULL`（复用 `tool_approval_store.CascadeByConversation` 的级联处理模式，在 `DeleteConversation` 事务内执行） |
| lease 过期 | 其他会话可接管 |
| 30 天未推进 | `CleanupExpired` 定时回收（模式同 `agent_execution_checkpoints.DeleteExpired`） |
| 目标达成 | LLM 调保留 tool `stratum_complete_task`（或用户对话中显式要求）→ status=completed，停更 |
| 多次失败（`fail_count` 超阈值） | 恢复时提示"上次多次失败，是否继续"，**不自动改状态**（避免误伤）；`abandoned` 仅由用户显式操作进入 |

核心语义：**task 故意独立于会话，会话删除只解除关联，目标始终可恢复**。若某天需要"全部关联会话删除即回收 task"，那需要级联/孤儿回收——但这与"跨对话推进"语义冲突，默认不启用。

## 7. 数据流

### 7.1 提取挂点 —— 只服务有 Plan 的执行

task 内容从 Plan 映射（Plan 是目标内容的事实源）。**挂点不能放 `Execute` 末尾读 checkpoint**：执行结束时 checkpoint 的 `runtime_state_json` 只编码 ActiveSkills、不编码 Plan（`buildReActRuntimeState`），且被最后一步 AfterStep 覆盖。挂点移到 `executeReAct` / `executePlanning` 内部、`cg.Invoke` 返回之后，直接读**内存** `finalState.ActivePlan`：

```
cg.Invoke 返回后，若 finalState.ActivePlan 非空 →
  TaskSnapshot:
    goal            ← 首个/合并 PlanNode.Goal（Plan 无顶层 Goal）
    current_phase   ← 节点状态分布推导（"N/M 完成，status=…"）
    completed_steps ← PlanNodeStatus=Succeeded 的节点
    next_action     ← 首个 pending 且依赖满足的节点
    status          ← 全部节点达成 → completed；失败 → fail_count+1；否则 active
  → 若本次 execution 恢复了某活跃 task → 更新该 task；
    否则（新 plan）→ 新建 task（新 id，UUID 派生）
  → 原子 Claim
```

**不依赖 checkpoint**：`checkpoint_enabled=false`（agents 表列）时 plan 不落盘，但 plan 内存态仍在——task 提取照常工作。纯对话（无 plan）不建 task——task 是"推进目标"的载体，目标需要 plan。完成信号（`stratum_complete_task`）由 LLM 调用时记录到 `AgentResult` metadata，随提取一并写入。

### 7.2 恢复链路 —— 两级互补，不重叠

| 级 | 触发 | 机制 | 现状 |
|---|---|---|---|
| checkpoint（已有） | 同 execution 断线/中断 | `resumeFromCheckpoint` 恢复消息+plan | ✅ 已存在 |
| **task（新增）** | **新会话、跨 execution** | `GetLatestActiveForOwner` → 语义相关才注入 task 摘要 + continue 指令 | 🆕 本设计 |

新会话推进流程（注入点与 `resumeFromCheckpoint` 同一位置：`BuildContextMessagesWithCompaction` 的 initMessages 组装；`BuildInitMessages` 非生产路径无调用者，不采用）：

```
用户在新会话发消息 →
  task := GetLatestActiveForOwner(tenantID, agentID, userID)   -- 最新活跃 task
  if task.status=='active' 且 next_action 非空 且 新消息与 task.goal 语义相关:
    注入 task 摘要（goal / current_phase / completed_steps / next_action）
    + continue 指令（进入 plan continue 模式，非从零重新 plan）
    → agent 继续推进 → execution 结束更新该 task + 刷新 lease
  若同一 execution 已通过 resumeFromCheckpoint 恢复完整 plan:
    两级同命中时以 checkpoint plan 为准，task 不注入（避免重复）
```

**语义相关判断**：命中活跃 task 时先判断新消息与 task.goal 语义相关才注入——避免同一 agent+user 的新会话起不相关新目标时被旧 task 摘要劫持。**不自动加载旧会话完整历史**：task 摘要是进度的压缩表示；完整历史属 checkpoint 语义（同会话断线）。

### 7.3 plan 触发策略

现状：plan 完全由 LLM 自主触发（`stratum_create_plan` 是 ReAct 保留 tool）。**不引入硬编码轮数主逻辑**，理由：轮数不是复杂度的可靠代理（1 轮复杂迁移 vs 20 轮闲聊，轮数无法区分），且随模型/输入漂移。

触发信号按优先级：

| 信号 | 机制 |
|---|---|
| 用户显式要求 | "做个计划""分步骤来" → LLM 识别并调 `stratum_create_plan` |
| LLM 自主判断 | 现有 `stratum_create_plan` tool（主通道，保留） |
| **新会话恢复发现活跃 task** | task.next_action 非空 → 语义相关时注入 continue 指令（本设计闭环） |
| **目标达成** | LLM 调 `stratum_complete_task` → task 转 completed |
| 历史预算触达 | `Budget.HistoryCap` 快耗尽 → 上下文要挤出时建议固化目标（挂在 `BuildContextMessagesWithCompaction` 侧） |

关键闭环：**task 持久化本身就是最强的"该走 plan"信号**。LLM 激活过 plan → task 持久化 → 新会话命中活跃 task 自动继续，不再依赖 LLM 重新判断。轮数阈值仅作可配置兜底（`DefaultPlanRoundThreshold`），定位纯兜底、权重低，防止 LLM 漏判时空转。

## 8. 错误处理

| 场景 | 处置 |
|---|---|
| task upsert/claim 失败（写路径） | **旁路降级**：不阻断已产生的 execution 响应（仿 MemoryBuffer 模式，`agent_service.go` 缓冲失败不阻断响应）——ERROR 日志 + `fail_count` 累积，恢复时提示；下次 execution 补偿更新 |
| claim 冲突（另一会话在推进） | 不打断当前 execution；本会话降级为只读（不写 task 内容），WARN 记录 |
| 恢复时 task 读取失败 | **fail closed**，拒绝继续——不能假装有进度（risk-guardrail #1） |
| conversation 删除时 detach 失败 | 由 `DeleteConversation` 事务回滚，向上传播 |

写路径降级、读路径 fail closed 的理由：写是副作用（不丢用户已收到的回复），读是恢复依据（错了不能假装有进度）。

## 9. 测试

- **domain**：Plan → TaskSnapshot 映射纯函数（goal 从 PlanNode 推导、current_phase 从节点状态分布推导），表驱动。
- **repository**：Upsert / ClaimAtomic / GetLatestActiveForOwner / CleanupExpired / DetachConversation——覆盖 tenantID 强制、claim 冲突、lease 过期接管、conversation 删除 detach、**generation 乐观锁（claim bump 后 stale 写被拒）**、多活跃 task 并存。所有方法经 `execTenant(ctx, tenantID, fn)`，port 签名显式含 `tenantID string`。
- **application**：提取挂点（mock plan/task repo，读内存 ActivePlan 而非 checkpoint）；恢复链路（命中/未命中、**语义相关判断**、**checkpoint plan 与 task 同命中时以 plan 为准**、写路径旁路降级、读路径 fail closed）。
- **E2E**：无头浏览器跨会话推进同一目标（`stratum-e2e-development` skill 主导验收）。

## 10. 落地位置与改动清单

| 文件 | 改动 |
|---|---|
| `pkg/storage/postgres/tenant_schema.sql` | 新增 `agent_tasks` 表 + 2 索引（§4 DDL，IF NOT EXISTS） |
| `internal/agent/domain/` | `Task` / `TaskSnapshot` 实体 + `TaskRepo` port（方法含 tenantID） |
| `internal/agent/infrastructure/persistence/` | `PgTaskRepo`（execTenant 封装）+ `task_store.go`；`chat_store.DeleteConversation` 内挂 detach |
| `internal/agent/application/` | `BaseAgent.Execute` 结束路径挂提取点；恢复链路注入 |
| `internal/agent/application/graph/` | TaskSnapshot 映射（读内存 `finalState.ActivePlan`，非 checkpoint）；新增保留 tool `stratum_complete_task` |
| `pkg/constants/` | `DefaultTaskLeaseDuration` / `DefaultTaskExpiresAt` / `DefaultPlanRoundThreshold` |
| `api/wiring/` | 装配 PgTaskRepo 到 AgentService |
| 前端 | 仅对话内提示（任务摘要条），`web/src/modules/agent/` |

## 11. 成功标准

1. 会话 A 激活 plan 干一半，会话 B（同 agent+user）发消息且语义相关，agent 从 next_action 继续，无需用户重述目标。
2. 会话 A 删除后，会话 B 仍能恢复同一 task（detach 不级联）。
3. 两个会话同时推进同一 task，第二个会话降级只读，不互相覆盖（claim bump generation + lease 生效）。
4. 同一 agent+user 可在不同会话建多个活跃 task（不互斥）。
5. 无 plan 的纯对话不产生 task 行；`checkpoint_enabled=false` 时 task 提取照常。
6. 目标达成后 LLM 调 `stratum_complete_task`，task 转 completed。
7. 新会话消息与旧 task 语义无关时不注入摘要。
8. `go vet && go test -short ./...` 通过；E2E 覆盖跨会话场景。
