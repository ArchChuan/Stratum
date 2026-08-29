# Skill 草稿 → 发布与撤销设计

## 目标

将 skill 从「保存即生效」改为「草稿 → 发布」流程（对齐工作流的编辑体验）：

- 编辑内容保存为草稿，不立即生效；显式「发布」后才成为生效版本。
- 支持「撤销草稿」：删除已保存的草稿，前端自动用最新生效版本数据回填表单。
- 版本历史与回滚语义保持不变（`published` / `deprecated` 依旧）。

## 非目标

- 不改动候选（candidate）与评测优化流程。
- 不引入审批流（草稿 → 发布由作者直接完成）。
- 不改变工作流 / 平台参数等其他草稿机制。

## 现状：DB 基础设施已预留

skill 的草稿基础设施已就绪，本设计是补齐 application 层流转，不是新建：

- `skills` 表已有 `draft_revision_id TEXT` 列（`tenant_schema.sql:327, 335`），当前未使用。
- `skill_revisions` 表 `status` 含 `draft`；部分唯一索引 `idx_skill_revisions_one_draft ON skill_revisions(skill_id) WHERE status='draft'`（每 skill 至多一个草稿）。
- `skill_revisions.revision_no` 可空 + `idx_skill_revisions_published_no WHERE revision_no IS NOT NULL`：草稿不占正式版本号，发布时才分配。
- `SkillRevision.ValidatePublishable()` 已存在（发布前置校验：名称 / 描述 / 指令非空）。
- domain `VersionStatusDraft` 已定义。

## 草稿模型

- 草稿 = `skill_revisions` 中 `status='draft'` 的行，`revision_no` 为 NULL（不占版本号），`published_at` 为 NULL。
- `skills.draft_revision_id` 指向草稿行；`skills.active_revision_id` 保持不变（继续指向当前生效版）。
- 每 skill 至多一个草稿（唯一索引保证）：再次保存草稿 = 覆盖更新草稿行，`draft_revision_id` 不变。

## 草稿保存

改造 `SaveRevision` 为保存草稿（或新增 `SaveDraft`，保留既有入口由前端切换）：

1. 复用现有 `loadOwnedActive` 校验所有权；`expectedContentHash` 基线取当前生效版本 `content_hash`，失配返回 `ErrSkillDraftStale`（409）。
2. 构造草稿 `SkillRevision`：`Status=draft`、`RevisionNo=0`、`Source=manual`，payload 为 trim 后的名称 / 描述 / 指令。
3. 事务内：INSERT 新草稿（或覆盖既有草稿，`revision_no` 保持 NULL）→ 更新 `skills.draft_revision_id` → **不动** `active_revision_id` 与既有 `published/deprecated` → audit。
4. 返回最新 workspace 视图（含「存在未发布草稿」标记）。

## 发布

新增 `PublishDraft`：

1. 加载草稿；无草稿 → `ErrSkillNoDraft`（409）。
2. `ValidatePublishable()`；校验不通过 → `ErrSkillNotPublishable`（409），不破坏既有草稿。
3. 事务内：草稿行 → `status='published'` + 分配 `revision_no`（`NextRevisionNo`）+ `published_at=NOW()` + `parent_revision_id` 自链到旧 active；`skills.active_revision_id` 重指草稿；旧 active → `deprecated`；`skills.draft_revision_id` 清空；audit。
4. 返回新 workspace 视图（`active` 指向发布版）。

发布后草稿行转正，不留悬空 draft；版本历史中出现该新版本。

## 撤销草稿

新增 `DiscardDraft`：

1. 事务内：删除 `status='draft'` 的行 + 清空 `skills.draft_revision_id`；幂等（无草稿也返回成功）。
2. 返回当前生效版本数据（供前端回填；无生效版本时返回空草稿表单）。

## 前端（`SkillWorkspacePage`）

- 表单按钮：「保存草稿」（`saveDraft`）+「发布」（`publishDraft`，有草稿且通过校验时可用）。
- 编辑区提示条：存在未发布草稿时显示「有未发布的草稿」。
- **撤销草稿**按钮（`Modal.confirm`）：调用 `discard` → `applyWorkspace(返回数据)` 回填表单（有生效版回填生效版；无生效版清空表单）。
- 版本历史 tab 保留：只展示 `published` / `deprecated`（draft 行不出现在版本历史），回滚逻辑不变。
- 兼容：存量 skill 无草稿 → 表单回填 `active`；首存 = 草稿，需发布才生效（产品提示）。

## 错误处理

- 并发保存草稿（`expectedContentHash` 失配）→ 409。
- 无草稿发布 → 409。
- 草稿校验不通过 → 409，不破坏既有草稿。
- 撤销草稿幂等。

## 测试

- `SaveDraft`：覆盖更新草稿、乐观锁冲突、不改变 `active_revision_id` 与既有 published/deprecated。
- `PublishDraft`：转正 + 版本号分配 + 旧版降级 + 指针重指原子性；无草稿 409；校验失败 409。
- `DiscardDraft`：删除草稿 + 幂等。
- 前端：保存草稿 / 发布 / 撤销回填。

## 风险与边界

- 存量 skill 无草稿，直接兼容；历史草稿数据不存在。
- `skills.status` 列（产品行状态）与 `skill_revisions.status`（版本状态）语义不同：产品行状态不被草稿保存改变，仅发布时刷新展示。
- 并发发布与保存草稿以 `expectedContentHash` 乐观锁串行化，冲突返回 409 由前端重载重试。
