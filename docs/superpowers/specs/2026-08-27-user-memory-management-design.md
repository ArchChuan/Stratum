# 用户记忆管理：可视化 / 删除 / 修改 / 向量同步

> 状态：已获设计确认（brainstorming 流程）
> 日期：2026-08-27
> 范围：`internal/memory` 后端 + `web/src/modules/memory` 前端

## 背景与目标

用户需求：**用户所有的记忆，都需要可视化、可删除、可修改，并同步向量数据。**

现状盘点（代码核验）：

- `internal/memory` 已有完整记忆子系统。`memory_facts` 存在 PG，向量存在 Milvus
  `memory_facts_<tenant>[_<model>]` 集合；`FactRepo` 已有 `GetByID/Update/Delete`，
  `VectorStore` 已有 `Upsert/DeleteFactVectors/DeleteEntryVectors`。
- 已暴露 API：`GET /memory`（列表）、`GET /memory/entities`、`GET /memory/stats`、
  `DELETE /memory/clear`、`GET /memory/summary/:session_id`。
- 前端 `MyMemoriesPage` 已有记忆条目 + 实体两个表格 + 「清空全部」，**缺行级编辑/删除**。
- **缺口**：单条记忆的删除/修改端点缺失；向量同步（编辑重嵌入 / 删除清向量）缺失；
  前端可视化停留在基础表格。

经澄清确认的需求：

1. **可视化**：增强列表 —— 搜索 + 重要度/分类筛选 + 详情 Drawer。
2. **管理范围**：全部 5 类可进入上下文的记忆（事实 / 实体 / 历史摘要 / 活跃快照 / 原始条目）。
3. **可编辑字段**（事实）：内容 + 重要度 + 分类；内容改动**不重抽实体**。
4. **向量同步**：同步重嵌入（请求内完成）。
5. **删除语义**：硬删除（PG 物理删除 + 清向量）。
6. **各类操作组合**：facts 可编辑+删除；快照可编辑+清空；实体/摘要/条目仅删除。

### 为什么原始条目应纳入管理（业界视角）

`memory_entries`（原始条目）确实会进入上下文，有两条路径（代码核验）：

- **路径一（直接）**：`memory.recall` 工具（`recall_tool.go`）查询 `memory_entries`，
  `tryVectorSearch` 检索 raw 集合（`memory_<tenant>[_<model>]`）∪ facts 集合，
  向量+文本混合、RRF 融合后返回给 LLM。
- **路径二（间接）**：history worker 把 `memory_entries` 压缩为 `memory_summaries`
  （recent/earlier/long-term 分层），`MemoryInjector.BuildContext` 每次构建上下文注入。

业界实践（Obsidian 知识库 + 公开资料）：

- 召回原文是成熟模式：MemGPT/Letta 的 recall memory 与 archival 原文块、Zep 的
  episodes、通用 RAG 的 chunk 召回，都是"原文进上下文"。
- 前提：**按需召回（agent 工具调用）、top-k 有界、过滤过期/非活跃**，绝不常驻注入。
- Stratum 当前实现正是按需召回模式（top-k 1–20、`filterRecallResults` 过滤、
  PG 二次校验），不是反模式；提取式 `memory_facts` 负责常驻注入的稳定长期记忆。

## 设计总览

方案 A（已选）：统一记忆管理服务 + 5 类 REST 子资源；每类独立 handler/service，
复用现有 repo 与向量同步模式；前端 5 Tab 增强列表。

## 第 1 节：后端 API

沿用现有 `/memory` 组（member 角色 + requireActive），扩展为 5 类子资源。
所有按 ID 操作先 `GetByID` 并校验归属（`user_id == 当前用户`），不匹配一律 404
（不泄露存在性）。

### 事实 Facts

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/memory/facts?page&page_size&q&importance_min&importance_max&category` | `q` 走 trigram 内容匹配；重要度区间/分类筛选；分页 |
| GET | `/memory/facts/:id` | 详情（编辑预填） |
| PATCH | `/memory/facts/:id` | body `{content?, importance?, category?}`，至少一项；校验 content 非空、importance∈[0,1]、category∈白名单 |
| DELETE | `/memory/facts/:id` | 硬删 + 向量清理 |

前端记忆条目 Tab 改用 `/memory/facts`（带筛选/详情）；现有 `GET /memory`
保留为兼容的简单列表，不再被新页面消费。

### 实体 Entities

- `DELETE /memory/entities/:id`（列表已有，新增单删）

### 历史摘要 Summaries

- `GET /memory/summaries?page&page_size` → id/summary/tier/importance/conversation_id/period_end/created_at
- `DELETE /memory/summaries/:id`

### 活跃快照 Snapshot

- `GET /memory/snapshots`（每 (user,agent) 一条：work_context/personal_context/top_of_mind/过期/状态）
- `PATCH /memory/snapshots/:agent_id`（改三段数组，长度校验按 `constants.ActiveSnapshot*MaxRunes`）
- `DELETE /memory/snapshots/:agent_id`

### 原始条目 Entries

- `GET /memory/entries?page&page_size&q` → id/role/content/type/importance/scope/created_at/expires_at
- `DELETE /memory/entries/:id`（硬删 + 向量清理）

### DTO 变更

扩展 `proto/memory/memory.proto` 的 `MemoryFactResponse`（补 category/confidence/source/status），
并新增各资源响应类型。随后 `make proto-gen` 重新生成 Go DTO（`api/http/dto/gen/`）与前端 TS
（`web/src/services/gen/`），生成物 gitignored。

## 第 2 节：服务层与数据访问

### 服务层（扩展 `internal/memory/application.MemoryService`）

| 新方法 | 职责 | 复用 repo |
|---|---|---|
| `UpdateUserFact(ctx, tenantID, userID, factID, patch)` | 校验归属 → 应用变更 → 向量同步 → `factRepo.Update` | `FactRepo.GetByID/Update` |
| `DeleteUserFact(ctx, tenantID, userID, factID)` | 校验归属 → `factRepo.Delete` → `DeleteFactVectors` | `FactRepo.Delete` |
| `ListUserMemories` 扩展过滤 | `q`/`importance_min/max`/`category` | `FactRepo.ListUserFactsFiltered`（新） |
| `ListUserSummaries(ctx, tenantID, userID, limit, offset)` | 分页列出活跃摘要 | `HistoryRepo.ListUserSummaries`（新） |
| `DeleteUserSummary(ctx, tenantID, userID, id)` | 校验归属 → 删除 | `HistoryRepo.Delete`（新） |
| `ListUserSnapshots(ctx, tenantID, userID)` | 列出该用户全部 (user,agent) 快照 | `ActiveSnapshotRepo.ListUser`（新） |
| `UpdateUserSnapshot(ctx, tenantID, userID, agentID, patch)` | 校验归属 → `UpdatedAt=now` → Upsert | `ActiveSnapshotRepo.Upsert` |
| `DeleteUserSnapshot(ctx, tenantID, userID, agentID)` | 校验归属 → 删除 | `ActiveSnapshotRepo.Delete` |
| `ListUserEntries(ctx, tenantID, userID, limit, offset, q?)` | 分页列出条目 | `MemoryRepo.ListUserEntries`（新） |
| `DeleteUserEntry(ctx, tenantID, userID, id)` | 校验归属 → `memoryRepo.Delete` → `DeleteEntryVectors` | `MemoryRepo.Delete` |

### 仓库层新增（port + persistence 各一个方法）

| 仓库 | 新增方法 | 说明 |
|---|---|---|
| `FactRepo` | `ListUserFactsFiltered(ctx, tenantID, userID, filter, limit, offset)` | `q` 走 trigram 内容匹配（复用既有 `SearchByContent` 机制）；importance 区间 + category 拼进 WHERE |
| `EntityRepo` | `Delete(ctx, tenantID, id)` | 目前只有批量删，补单删 |
| `HistoryRepo` | `ListUserSummaries(...)`、`Delete(ctx, tenantID, id)` | 列表仅 `scope='user' AND status='active'` |
| `ActiveSnapshotRepo` | `ListUser(ctx, tenantID, userID)` | 快照 `UNIQUE(user_id, agent_id)`，需跨 agent 列出 |
| `MemoryRepo` | `ListUserEntries(ctx, tenantID, userID, limit, offset, q?)` | 目前只有 `Search`，补分页列表 |

### 归属校验统一规则

所有 `:id` 操作先读实体校验 `UserID == 当前用户`（snapshot 校验 user_id），不匹配 404。
快照编辑用 `UpdatedAt=now()` 绕过 `EXCLUDED.updated_at` 覆盖守卫（用户显式操作优先）。

## 第 3 节：向量同步与一致性

### 事实编辑（PATCH）——顺序刻意设计

1. 校验 + `GetByID` + 归属校验。
2. **先用新内容嵌入** → 失败（无模型/调用失败）返回 502，**不写任何数据**
   （fail-closed，与 `embedAndStoreFactVector` 一致）。
3. `DeleteFactVectors(tenant, [id])` **先删旧向量**（所有 `memory_facts_*` 集合）→
   陈旧内容立即停止被召回。
4. `factRepo.Update`（PG 新内容）→ 失败返回 500（此时 PG 未变、无新向量，一致）。
5. `Upsert` 新向量到 `factsCollectionName(tenant, 当前模型)` → 失败返回错误但 PG 已提交。

顺序要点：**旧向量先删、新向量后写**，避免同 ID（fact.ID 是 Milvus 主键）互相覆盖。

### 事实/条目删除 ——「PG 删除成功 = 操作成功」

- `factRepo.Delete` / `memoryRepo.Delete` 成功后，向量清理
  （`DeleteFactVectors` / `DeleteEntryVectors`）为 **best-effort**，失败记 ERROR + 指标。
- 依据：recall 侧有 PG 二次校验（`collectRecallableFacts` 用 `GetByID` 找不到即跳过；
  `keepLiveEntryResults` 回 PG 校验未过期），**PG 删除成功已保证召回正确**，向量清理
  纯属数据卫生，由既有 GC reconcile pass 兜底。

### 无向量类型

实体/摘要/快照不涉及向量同步。

### 原子性边界（明确不做）

PG 与 Milvus 不跨库事务。沿用系统既定模式——PG 为唯一真相源 + 向量 best-effort +
GC reconcile 兜底。**不为本功能引入 outbox/JetStream 事件**（YAGNI，不动流水线）。

## 第 4 节：前端

### 页面结构

`MyMemoriesPage` 扩为 5 个 Tab（事实/实体/摘要/快照/条目），顶部保留统计卡片。
每个 Tab 独立组件 + 独立 hook，避免单文件膨胀。

| 组件 | 能力 |
|---|---|
| `FactTable.tsx` | 搜索框（q）+ 重要度/分类下拉筛选 + 分页；行点击 → 详情 Drawer（完整内容/重要度/分类/置信度/来源/时间戳）；行内编辑 → Modal（content 文本域 + 重要度 + 分类）；删除 → `DangerPopconfirm` |
| `EntityTable.tsx` | 现有实体表 + 每行删除 |
| `SummaryTable.tsx` | 摘要表（summary/tier/importance/period/时间）+ 每行删除 |
| `SnapshotPanel.tsx` | 各 agent 快照卡片（三段可编辑列表）+ 编辑 Modal + 清空 |
| `EntryTable.tsx` | 条目表（content/role/type/importance/scope/时间/过期）+ 搜索 + 每行删除 |

### 状态与数据层

- `model/memory.ts`：扩展 `MemoryFact`（category/confidence/source/status），
  新增 `MemorySummary`、`MemorySnapshot`、`MemoryEntry` 类型。
- `api/memory-user.api.ts`：新增各资源请求函数（对齐 `web/src/services/gen/memory.ts` TS 类型）。
- `hooks/`：每 Tab 一个 hook（`useFactsTab`/`useEntitiesTab`/`useSummariesTab`/
  `useSnapshotsTab`/`useEntriesTab`），各自管理分页、loading、错误。

### 交互约束

- 删除一律 `DangerPopconfirm` + 「不可恢复」提示。
- 编辑成功后原地刷新当前页 + 刷新顶部统计卡片。
- 事实编辑保存时若向量同步失败，`message.error` 提示
  「内容已保存，但向量同步失败，将在后台补偿」；顶部 `embed_model_configured` 健康提示保留。

## 第 5 节：错误处理、边界与测试

### 错误处理（复用 `api/middleware/error_mapping.go`）

| 场景 | 状态码 |
|---|---|
| 归属不匹配 / 不存在 | 404 |
| 无效输入（content 空、importance 越界、category 非法、空 patch） | 400 |
| 嵌入模型未配置 / 嵌入失败 | 502 |
| 向量清理失败（编辑/删除） | 记 ERROR + 指标，不阻塞主操作 |
| PG 错误 | `translatePgError` 归一（not found → 404，其余 500） |

### 边界与竞态

- **仅 `status='active'` 的事实可编辑**：superseded/archived 返回 409（避免与提取管线
  supersede 链冲突）；删除任意状态均可。
- 快照编辑用 `UpdatedAt=now` 绕过反射覆盖守卫。
- 条目只删不编辑，无并发问题。

### 测试

- 单元（沿用 `fact_repo_mock_test.go` 等既有 mock 模式）：service 层 `UpdateUserFact`
  （成功/校验失败/归属不匹配/嵌入失败/向量失败仍返回）、`DeleteUserFact`、各列表过滤分页；
  handler 层参数校验与错误映射。
- 契约：`api/http/dto/gen/parity_test.go` 模式覆盖新 DTO 字段。
- e2e：按 `test/e2e/memory_lifecycle_test.go` 模式补「编辑事实 → 向量同步 → 召回命中新内容；
  删除事实 → 召回不再命中」链路。
- 前端：各 tab hook 测试（沿用 `useMyMemoriesPage.test.ts` 模式）+ 关键组件交互
  （删除 confirm、编辑保存）。
- 流程：`make proto-gen` 先行（生成物 gitignored），再跑后端/前端测试。

## 关联

- Stratum 分层记忆：Facts + Active Snapshot + Dynamic History（`docs/agent/tiered-memory.md`）
- 现有记忆用户侧 handler：`api/http/handler/user_memory_handler.go`
- 记忆服务：`internal/memory/application/memory_service_v2.go`
- 向量存储 port：`internal/memory/domain/port/vector_store.go`
- 前端页面：`web/src/modules/memory/pages/MyMemoriesPage.tsx`
