# Skill 草稿 → 发布与撤销 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 skill 从「保存即生效」改为「草稿 → 显式发布」流程：保存草稿不生效、发布后转正为新生效版本、撤销草稿幂等删除并回填当前生效版本。

**Architecture:** 复用已就绪的草稿基础设施（`skills.draft_revision_id` 列、`skill_revisions.status='draft'` 行、唯一索引 `idx_skill_revisions_one_draft`）。在 `VersionRepo` port 上新增 `SaveDraft`/`PublishDraft`/`DiscardDraft` 三方法，repo 事务内实现「覆盖更新草稿 / demote 旧 active 后转正草稿 / 删除草稿」；`VersionService` 复用 `loadOwnedActive` 所有权校验与 `expectedContentHash` 乐观并发；handler 新增三条 member 级路由；前端 `SkillWorkspacePage` 表单改三按钮并显示草稿提示条。

**Tech Stack:** Go 1.25（pgx v5、Gin v1.9）、protoc-gen-ginstruct 生成 DTO、React 18 + antd 5 + zod。

## Global Constraints

- **发布事务顺序（spec §发布 #3 修正）**：`PublishDraft` 事务内必须先 demote 旧 active（`UPDATE skill_revisions SET status='deprecated' ... WHERE skill_id=$1 AND status='published'`）再转正草稿（`UPDATE ... SET status='published' ... WHERE id=$2 AND skill_id=$1 AND status='draft'`）。此时草稿仍是 `'draft'`，demote 的 WHERE 不会命中它；反向顺序会让 demote 降级刚转正的草稿。镜像 agent/versioning 的 Demote→Insert 顺序。
- **错误名修正（spec §发布 #1）**：spec 引用 `ErrSkillNoDraft`（不存在）。实际 domain sentinel 是 `ErrSkillDraftNotFound`（`internal/skill/domain/errors.go`，经 `api/middleware/error_mapping.go` 映射 409）。
- **校验失败状态码（spec §发布 #2 修正）**：spec 写「`ErrSkillNotPublishable` → 409」。实际 `error_mapping.go` 将其映射为 **400**（既定决策）。保持 400。`ValidatePublishable()` 在 service 层事务前调用，校验失败不破坏既有草稿。
- **乐观并发串行化**：`SaveDraft` 与 `PublishDraft` 都接收 `expectedContentHash` 基线（前端取当前 `active.contentHash`），失配 → `ErrSkillDraftStale`(409)，由前端重载重试。空 baseline 接受任意状态（存量未发布 skill 首存）。
- **草稿覆盖语义**：再次保存草稿 = 覆盖既有 draft 行（service 复用 `skill.DraftRevisionID` 作为草稿 id，不重新生成）；仅首次保存才 INSERT + 指向。`active_revision_id` 与既有 published/deprecated 历史**不**被草稿保存改动。
- **草稿不占版本号**：draft 行 `revision_no` NULL、`published_at` NULL（`insertSkillRevision` 的 `NULLIF($4,0)` + `CASE WHEN $5='published'` 已支持）；发布时才分配 `NextRevisionNo` 并写 `published_at`。
- **版本历史不含草稿**：后端 `listRevisionsSQL` 无 status 过滤（返回含 draft 行），**前端**版本历史 tab 过滤 `r.status !== 'draft'`；后端不改。
- **路由权限**：三条草稿路由（`POST /:id/draft`、`POST /:id/publish`、`DELETE /:id/draft`）均 member 级 `requireActive`（对齐 `PATCH /:id UpdateSkill`），编辑人经 service `resolveUpdateActor` 白名单校验，不叠加 admin 门槛。
- **契约覆盖**：skill 端点无 contract golden（buildSkill nil-db early return），新增端点只做 handler unit tests；**不**跑 `make record-contracts`。
- **无请求体端点**：`DiscardSkillDraft` 无请求体，handler 不 bind、proto 不定义空 request message（YAGNI）。
- 提交标题 `[type](scope): description`，type 用 `feat|chore`；每个 task commit 以 `feat: ...` 开头并带 `Co-Authored-By: Claude <noreply@anthropic.com>`。

---

### Task 1: proto 契约 + 生成物 + 前端类型

**Files:**

- Modify: `proto/skill/skill.proto`
- Test: 无新增测试文件（纯契约/生成，验证 = 生成成功 + 编译通过）
- Modify: `web/src/modules/skill/model/skill.ts:38-44`

**Interfaces:**

- Produces: `gen.SkillWorkspaceResponse.HasDraft bool`（proto `has_draft = 4`）、`gen.SaveSkillDraftRequest{Name,Description,Instructions,ExpectedContentHash}`、`gen.PublishSkillDraftRequest{ExpectedContentHash}`、前端 `skillWorkspaceSchema` 增 `hasDraft: z.boolean().optional().default(false)`。

- [ ] **Step 1: 在 `proto/skill/skill.proto` 的 `SkillWorkspaceResponse` 增加 `has_draft` 字段**

在 `SkillWorkspaceResponse` 的 `SkillRevisionResponse active = 3;` 之后追加：

```proto
message SkillWorkspaceResponse {
  repeated string editors = 1;
  SkillProductResponse skill = 2;
  SkillRevisionResponse active = 3;
  // hasDraft 标记存在未发布草稿(skills.draft_revision_id 非空)。前端据此
  // 展示草稿提示条并开启发布入口。
  bool has_draft = 4;
}
```

- [ ] **Step 2: 在 `proto/skill/skill.proto` 文件末尾追加两个 request message**

```proto
// SaveSkillDraftRequest 保存或覆盖 skill 的草稿,不立即生效。
// expectedContentHash 为乐观并发基线,取当前生效版本 content_hash,失配返回 409。
message SaveSkillDraftRequest {
  string name = 1;
  string description = 2;
  string instructions = 3;
  // @omitempty
  string expectedContentHash = 4;
}

// PublishSkillDraftRequest 将草稿提升为新的生效版本(版本号分配、旧版降级、指针重指)。
message PublishSkillDraftRequest {
  // @omitempty
  string expectedContentHash = 1;
}
```

- [ ] **Step 3: 重新生成前后端契约类型**

Run:

```bash
cd /home/yang/go-projects/stratum-kb-version-skill-draft && make proto-gen
```

Expected: 成功无报错；`api/http/dto/gen/skill.pb.go` 出现 `HasDraft bool`、`SaveSkillDraftRequest`、`PublishSkillDraftRequest`；`web/src/services/gen/skill.ts` 出现 `hasDraft`、`SaveSkillDraftRequest`、`PublishSkillDraftRequest`。

- [ ] **Step 4: 前端 schema 增加 `hasDraft`**

在 `web/src/modules/skill/model/skill.ts` 将 `skillWorkspaceSchema`（38-44 行）改为：

```ts
export const skillWorkspaceSchema = z.object({
  skill: skillProductSchema,
  // active 是当前生效版本;存量未发布 skill 首次保存前为空。
  active: skillRevisionSchema,
  editors: z.array(z.string()).default([]),
  // hasDraft 标记存在未发布草稿(skills.draft_revision_id 非空);前端据此
  // 展示草稿提示条并开启发布入口。
  hasDraft: z.boolean().optional().default(false),
}).passthrough();
```

- [ ] **Step 5: 验证契约生成**

Run:

```bash
cd /home/yang/go-projects/stratum-kb-version-skill-draft && grep -c "HasDraft" api/http/dto/gen/skill.pb.go && grep -c "hasDraft" web/src/services/gen/skill.ts
```

Expected: 两个命令各输出 ≥1（字段已生成）。

- [ ] **Step 6: Commit**

```bash
cd /home/yang/go-projects/stratum-kb-version-skill-draft && git add proto/skill/skill.proto web/src/modules/skill/model/skill.ts && git commit -m "feat(skill): add draft workspace contract fields

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 2: VersionRepo port 扩展 + mock 同步

**Files:**

- Modify: `internal/skill/domain/port/version_repository.go:21-49`（`VersionRepo` interface）
- Test: `internal/skill/application/version_service_test.go`（`fakeVersionRepo` 加三方法，追加在 `NextRevisionNo` 之后）

**Interfaces:**

- Consumes: `domain.SkillRevision`、`auditdomain.ResourceChangeAuditEvent`（已定义）。
- Produces: 三个 port 方法签名（Task 3 repo 实现、Task 4 service 调用的精确契约）：
  - `SaveDraft(ctx context.Context, skillID, expectedContentHash string, draft domain.SkillRevision, editorActor string, audit *auditdomain.ResourceChangeAuditEvent) error`
  - `PublishDraft(ctx context.Context, skillID, draftID, parentRevisionID string, nextRevisionNo int, expectedContentHash, editorActor string, audit *auditdomain.ResourceChangeAuditEvent) error`
  - `DiscardDraft(ctx context.Context, skillID, editorActor string, audit *auditdomain.ResourceChangeAuditEvent) error`

- [ ] **Step 1: 写失败测试——扩展接口（编译失败是预期的失败信号）**

在 `internal/skill/domain/port/version_repository.go` 的 `NextRevisionNo` 方法（第 48 行）之后追加：

```go
 // SaveDraft persists the skill's single draft revision, overwriting any
 // existing draft row (id kept) or inserting a fresh one, and repoints
 // skills.draft_revision_id. The active revision and all published/deprecated
 // revisions are untouched — saved content is NOT effective until PublishDraft.
 // expectedContentHash is the optimistic-concurrency baseline against the
 // current active revision (409 on mismatch). editorActor, when non-empty,
 // re-validates the actor's editor qualification inside the write transaction.
 SaveDraft(ctx context.Context, skillID, expectedContentHash string, draft domain.SkillRevision, editorActor string, audit *auditdomain.ResourceChangeAuditEvent) error
 // PublishDraft promotes the skill's draft to the new active published version
 // in one transaction: the old active is demoted to deprecated, revision_no /
 // published_at / parent_revision_id are assigned, active_revision_id repoints
 // and draft_revision_id clears. nextRevisionNo is taken by the caller;
 // parentRevisionID is the old active's id ("" when the skill had none).
 // expectedContentHash guards concurrent edits (409). ErrSkillDraftNotFound is
 // returned when no draft exists.
 PublishDraft(ctx context.Context, skillID, draftID, parentRevisionID string, nextRevisionNo int, expectedContentHash, editorActor string, audit *auditdomain.ResourceChangeAuditEvent) error
 // DiscardDraft deletes the skill's draft row and clears skills.draft_revision_id
 // — idempotent (a skill without a draft succeeds). editorActor, when non-empty,
 // re-validates editor qualification inside the transaction.
 DiscardDraft(ctx context.Context, skillID, editorActor string, audit *auditdomain.ResourceChangeAuditEvent) error
```

- [ ] **Step 2: 运行以确认编译失败（mock 未同步）**

Run:

```bash
cd /home/yang/go-projects/stratum-kb-version-skill-draft && go build ./internal/skill/... 2>&1 | head -20
```

Expected: 编译失败，错误指向 `fakeVersionRepo` 缺 `SaveDraft`/`PublishDraft`/`DiscardDraft` 方法（未满足 `port.VersionRepo`）。

- [ ] **Step 3: 写最小实现——同步 `fakeVersionRepo`**

在 `internal/skill/application/version_service_test.go` 的 `NextRevisionNo` 方法（第 352 行）之后追加：

```go
func (r *fakeVersionRepo) SaveDraft(_ context.Context, skillID, expected string, draft domain.SkillRevision, _ string, audit *auditdomain.ResourceChangeAuditEvent) error {
 r.recordAudit(audit)
 skill, ok := r.skills[skillID]
 if !ok {
  return domain.ErrSkillNotFound
 }
 if expected != "" {
  active, ok := r.revisions[skill.ActiveRevisionID]
  if !ok || active.ContentHash != expected {
   return domain.ErrSkillDraftStale
  }
 }
 // 覆盖既有草稿(保持 id 不变)或首次插入。
 if old, ok := r.revisions[skill.DraftRevisionID]; ok && old.SkillID == skillID && old.Status == domain.VersionStatusDraft {
  old.Name, old.Description, old.Instructions = draft.Name, draft.Description, draft.Instructions
  old.ContentHash = draft.ContentHash
  r.revisions[old.ID] = old
 } else {
  r.revisions[draft.ID] = draft
  skill.DraftRevisionID = draft.ID
  r.skills[skillID] = skill
 }
 return nil
}
func (r *fakeVersionRepo) PublishDraft(_ context.Context, skillID, draftID, parentRevisionID string, next int, expected, _ string, audit *auditdomain.ResourceChangeAuditEvent) error {
 r.recordAudit(audit)
 skill, ok := r.skills[skillID]
 if !ok {
  return domain.ErrSkillNotFound
 }
 if expected != "" {
  active, ok := r.revisions[skill.ActiveRevisionID]
  if !ok || active.ContentHash != expected {
   return domain.ErrSkillDraftStale
  }
 }
 draft, ok := r.revisions[draftID]
 if !ok || draft.SkillID != skillID || draft.Status != domain.VersionStatusDraft {
  return domain.ErrSkillDraftNotFound
 }
 // 顺序必须 demote→promote:旧生效版本先降级,再转正草稿。
 if cur, ok := r.revisions[skill.ActiveRevisionID]; ok && cur.ID != draftID {
  cur.Status = domain.VersionStatusDeprecated
  r.revisions[cur.ID] = cur
 }
 draft.Status = domain.VersionStatusPublished
 draft.RevisionNo = next
 draft.ParentRevisionID = parentRevisionID
 r.revisions[draftID] = draft
 skill.ActiveRevisionID = draftID
 skill.DraftRevisionID = ""
 r.skills[skillID] = skill
 return nil
}
func (r *fakeVersionRepo) DiscardDraft(_ context.Context, skillID, _ string, audit *auditdomain.ResourceChangeAuditEvent) error {
 r.recordAudit(audit)
 skill, ok := r.skills[skillID]
 if !ok {
  return domain.ErrSkillNotFound
 }
 if draft, ok := r.revisions[skill.DraftRevisionID]; ok && draft.SkillID == skillID && draft.Status == domain.VersionStatusDraft {
  delete(r.revisions, draft.ID)
 }
 skill.DraftRevisionID = ""
 r.skills[skillID] = skill
 return nil
}
```

- [ ] **Step 4: 运行以确认编译通过且既有测试仍绿**

Run:

```bash
cd /home/yang/go-projects/stratum-kb-version-skill-draft && go build ./internal/skill/... && go test -short ./internal/skill/application/... -run 'TestVersionService' 2>&1 | tail -5
```

Expected: 编译通过；`TestVersionService...` 全 PASS。（`failingVersionRepo` 在 `ownership_matrix_test.go` embed `fakeVersionRepo`，新方法自动继承，无需同步。）

- [ ] **Step 5: Commit**

```bash
cd /home/yang/go-projects/stratum-kb-version-skill-draft && git add internal/skill/domain/port/version_repository.go internal/skill/application/version_service_test.go && git commit -m "feat(skill): extend version repo port with draft methods

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 3: skill repo 草稿持久化 + pgxmock 测试

**Files:**

- Modify: `internal/skill/infrastructure/persistence/skill_version_repo.go`
- Test: `internal/skill/infrastructure/persistence/skill_version_repo_mock_test.go`

**Interfaces:**

- Consumes: port 三方法签名（Task 2）、`insertSkillRevision`（375-400 行，已支持 draft）、`revalidateEditorIfActor`（66-75 行）、`insertChangeAudit`（80-100 行）、`resourceEditorKind = "skill"`（43 行）。
- Produces: `PgSkillRevisionRepo` 三方法的数据库行为（Task 4 依赖的语义）。

- [ ] **Step 1: 写失败测试——`SaveDraft` 覆盖既有草稿**

在 `internal/skill/infrastructure/persistence/skill_version_repo_mock_test.go` 文件末尾（`TestPgSkillRevisionRepo_ListRevisions_success` 之后）追加：

```go
func TestPgSkillRevisionRepo_SaveDraft_overwritesExisting(t *testing.T) {
 mock := newSkillMock(t)
 repo := newSkillRepo(mock)
 beginTenantTx(t, mock)

 mock.ExpectQuery("SELECT EXISTS").
  WithArgs("s-1").
  WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
 // UPDATE 命中既有草稿行(覆盖更新,id 不变)。
 mock.ExpectExec("UPDATE skill_revisions SET name=\\$2, description=\\$3, instructions=\\$4").
  WithArgs("s-1", "draft-name", "draft-desc", "draft-ins", "h-draft", "u1").
  WillReturnResult(pgxmock.NewResult("UPDATE", 1))
 mock.ExpectCommit()

 draft := domain.SkillRevision{ID: "dr-1", SkillID: "s-1", Status: domain.VersionStatusDraft, Source: "manual",
  Name: "draft-name", Description: "draft-desc", Instructions: "draft-ins", ContentHash: "h-draft", CreatedBy: "u1"}
 require.NoError(t, repo.SaveDraft(skillTenantCtx(), "s-1", "", draft, "", nil))
 require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgSkillRevisionRepo_SaveDraft_insertsNew(t *testing.T) {
 mock := newSkillMock(t)
 repo := newSkillRepo(mock)
 beginTenantTx(t, mock)

 mock.ExpectQuery("SELECT EXISTS").
  WithArgs("s-1").
  WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
 mock.ExpectExec("UPDATE skill_revisions SET name=\\$2, description=\\$3, instructions=\\$4").
  WithArgs("s-1", "draft-name", "draft-desc", "draft-ins", "h-draft", "u1").
  WillReturnResult(pgxmock.NewResult("UPDATE", 0))
 expectInsertSkillRevision(mock, "dr-1", "draft", "manual")
 mock.ExpectExec("UPDATE skills SET draft_revision_id=\\$2").
  WithArgs("s-1", "dr-1").
  WillReturnResult(pgxmock.NewResult("UPDATE", 1))
 mock.ExpectCommit()

 draft := domain.SkillRevision{ID: "dr-1", SkillID: "s-1", Status: domain.VersionStatusDraft, Source: "manual",
  Name: "draft-name", Description: "draft-desc", Instructions: "draft-ins", ContentHash: "h-draft", CreatedBy: "u1"}
 require.NoError(t, repo.SaveDraft(skillTenantCtx(), "s-1", "", draft, "", nil))
 require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgSkillRevisionRepo_SaveDraft_stale(t *testing.T) {
 mock := newSkillMock(t)
 repo := newSkillRepo(mock)
 beginTenantTx(t, mock)

 mock.ExpectQuery("SELECT EXISTS").
  WithArgs("s-1").
  WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
 mock.ExpectQuery("COALESCE\\(r.content_hash").
  WithArgs("s-1").
  WillReturnRows(pgxmock.NewRows([]string{"hash"}).AddRow("other-hash"))
 mock.ExpectRollback()

 draft := domain.SkillRevision{ID: "dr-1", SkillID: "s-1", Status: domain.VersionStatusDraft}
 require.ErrorIs(t, repo.SaveDraft(skillTenantCtx(), "s-1", "h-1", draft, "", nil), domain.ErrSkillDraftStale)
 require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgSkillRevisionRepo_PublishDraft_success(t *testing.T) {
 mock := newSkillMock(t)
 repo := newSkillRepo(mock)
 beginTenantTx(t, mock)

 mock.ExpectQuery("SELECT EXISTS").
  WithArgs("s-1").
  WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
 // demote 旧 active → promote 草稿 → 重指指针。顺序必须 demote 在前。
 mock.ExpectExec("UPDATE skill_revisions SET status='deprecated'").
  WithArgs("s-1").
  WillReturnResult(pgxmock.NewResult("UPDATE", 1))
 mock.ExpectExec("UPDATE skill_revisions SET status='published', revision_no=\\$3, published_at=NOW").
  WithArgs("s-1", "dr-1", 2, "r-1").
  WillReturnResult(pgxmock.NewResult("UPDATE", 1))
 mock.ExpectExec("UPDATE skills SET active_revision_id=\\$2, draft_revision_id=NULL").
  WithArgs("s-1", "dr-1").
  WillReturnResult(pgxmock.NewResult("UPDATE", 1))
 mock.ExpectCommit()

 require.NoError(t, repo.PublishDraft(skillTenantCtx(), "s-1", "dr-1", "r-1", 2, "", "", nil))
 require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgSkillRevisionRepo_PublishDraft_noDraft(t *testing.T) {
 mock := newSkillMock(t)
 repo := newSkillRepo(mock)
 beginTenantTx(t, mock)

 mock.ExpectQuery("SELECT EXISTS").
  WithArgs("s-1").
  WillReturnRows(pgxmock.NewRows([]string{"exists"}).AddRow(true))
 mock.ExpectExec("UPDATE skill_revisions SET status='deprecated'").
  WithArgs("s-1").
  WillReturnResult(pgxmock.NewResult("UPDATE", 0))
 mock.ExpectExec("UPDATE skill_revisions SET status='published', revision_no=\\$3, published_at=NOW").
  WithArgs("s-1", "dr-1", 2, "r-1").
  WillReturnResult(pgxmock.NewResult("UPDATE", 0))
 mock.ExpectRollback()

 require.ErrorIs(t, repo.PublishDraft(skillTenantCtx(), "s-1", "dr-1", "r-1", 2, "", "", nil), domain.ErrSkillDraftNotFound)
 require.NoError(t, mock.ExpectationsWereMet())
}

func TestPgSkillRevisionRepo_DiscardDraft_success(t *testing.T) {
 mock := newSkillMock(t)
 repo := newSkillRepo(mock)
 beginTenantTx(t, mock)

 mock.ExpectExec("DELETE FROM skill_revisions WHERE skill_id=\\$1 AND status='draft'").
  WithArgs("s-1").
  WillReturnResult(pgxmock.NewResult("DELETE", 1))
 mock.ExpectExec("UPDATE skills SET draft_revision_id=NULL").
  WithArgs("s-1").
  WillReturnResult(pgxmock.NewResult("UPDATE", 1))
 mock.ExpectCommit()

 require.NoError(t, repo.DiscardDraft(skillTenantCtx(), "s-1", "", nil))
 require.NoError(t, mock.ExpectationsWereMet())
}
```

- [ ] **Step 2: 运行以确认失败**

Run:

```bash
cd /home/yang/go-projects/stratum-kb-version-skill-draft && go test ./internal/skill/infrastructure/persistence/ -run 'TestPgSkillRevisionRepo_(SaveDraft|PublishDraft|DiscardDraft)' 2>&1 | tail -20
```

Expected: 编译失败（`repo.SaveDraft` 等方法未定义）——这是预期的失败信号。

- [ ] **Step 3: 写最小实现——repo 三方法**

在 `internal/skill/infrastructure/persistence/skill_version_repo.go` 的 `NextRevisionNo`（293 行）之后追加：

```go
// SaveDraft persists the skill's single draft revision, overwriting an
// existing draft row (id kept) or inserting a fresh one, and repoints
// skills.draft_revision_id. The active revision and published/deprecated
// history are untouched. expectedContentHash guards concurrent edits (409 on
// mismatch). editorActor, when non-empty, re-validates editor qualification
// inside the transaction (TOCTOU closure).
func (r *PgSkillRevisionRepo) SaveDraft(
 ctx context.Context,
 skillID, expectedContentHash string,
 draft domain.SkillRevision,
 editorActor string,
 audit *auditdomain.ResourceChangeAuditEvent,
) error {
 return r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
  if err := revalidateEditorIfActor(ctx, tx, resourceEditorKind, skillID, editorActor); err != nil {
   return err
  }
  var exists bool
  if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM skills WHERE id=$1)`, skillID).Scan(&exists); err != nil {
   return err
  }
  if !exists {
   return domain.ErrSkillNotFound
  }
  if expectedContentHash != "" {
   var activeHash string
   if err := tx.QueryRow(ctx,
    `SELECT COALESCE(r.content_hash, '') FROM skills s
     LEFT JOIN skill_revisions r ON r.id=s.active_revision_id WHERE s.id=$1`, skillID,
   ).Scan(&activeHash); err != nil {
    return err
   }
   if activeHash != expectedContentHash {
    return domain.ErrSkillDraftStale
   }
  }
  // 覆盖既有草稿(部分唯一索引保证至多一个);未命中则首次插入并指向。
  tag, err := tx.Exec(ctx,
   `UPDATE skill_revisions SET name=$2, description=$3, instructions=$4, source='manual',
           content_hash=$5, updated_at=NOW(), created_by=$6
    WHERE skill_id=$1 AND status='draft'`,
   skillID, draft.Name, draft.Description, draft.Instructions, draft.ContentHash, draft.CreatedBy,
  )
  if err != nil {
   return err
  }
  if tag.RowsAffected() == 0 {
   if err := insertSkillRevision(ctx, tx, draft); err != nil {
    return err
   }
   if _, err := tx.Exec(ctx,
    `UPDATE skills SET draft_revision_id=$2, updated_at=NOW() WHERE id=$1`,
    skillID, draft.ID,
   ); err != nil {
    return err
   }
  }
  return insertChangeAudit(ctx, tx, audit)
 })
}

// PublishDraft promotes the skill's draft to the new active published version
// in one transaction: the old active is demoted to deprecated first (a draft
// still holds status='draft', so the demote WHERE clause cannot touch it),
// then the draft is promoted with revision_no/published_at/parent assigned,
// active_revision_id repoints and draft_revision_id clears. nextRevisionNo is
// taken by the caller; parentRevisionID is the old active's id ("" when none).
// expectedContentHash guards concurrent edits (409); no draft → ErrSkillDraftNotFound.
func (r *PgSkillRevisionRepo) PublishDraft(
 ctx context.Context,
 skillID, draftID, parentRevisionID string,
 nextRevisionNo int,
 expectedContentHash, editorActor string,
 audit *auditdomain.ResourceChangeAuditEvent,
) error {
 return r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
  if err := revalidateEditorIfActor(ctx, tx, resourceEditorKind, skillID, editorActor); err != nil {
   return err
  }
  var exists bool
  if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM skills WHERE id=$1)`, skillID).Scan(&exists); err != nil {
   return err
  }
  if !exists {
   return domain.ErrSkillNotFound
  }
  if expectedContentHash != "" {
   var activeHash string
   if err := tx.QueryRow(ctx,
    `SELECT COALESCE(r.content_hash, '') FROM skills s
     LEFT JOIN skill_revisions r ON r.id=s.active_revision_id WHERE s.id=$1`, skillID,
   ).Scan(&activeHash); err != nil {
    return err
   }
   if activeHash != expectedContentHash {
    return domain.ErrSkillDraftStale
   }
  }
  // 顺序必须 demote→promote:先转正草稿再按 status='published' 降级会把
  // 刚转正的草稿自己降级。此时草稿仍是 'draft',demote 的 WHERE 不会命中它。
  if _, err := tx.Exec(ctx,
   `UPDATE skill_revisions SET status='deprecated', updated_at=NOW() WHERE skill_id=$1 AND status='published'`, skillID,
  ); err != nil {
   return err
  }
  tag, err := tx.Exec(ctx,
   `UPDATE skill_revisions SET status='published', revision_no=$3, published_at=NOW(),
           parent_revision_id=COALESCE($4, ''), updated_at=NOW()
    WHERE id=$2 AND skill_id=$1 AND status='draft'`,
   skillID, draftID, nextRevisionNo, parentRevisionID,
  )
  if err != nil {
   return err
  }
  if tag.RowsAffected() != 1 {
   return domain.ErrSkillDraftNotFound
  }
  if _, err := tx.Exec(ctx,
   `UPDATE skills SET active_revision_id=$2, draft_revision_id=NULL, status='published', updated_at=NOW() WHERE id=$1`,
   skillID, draftID,
  ); err != nil {
   return err
  }
  return insertChangeAudit(ctx, tx, audit)
 })
}

// DiscardDraft deletes the skill's draft row and clears skills.draft_revision_id.
// Idempotent: a skill without a draft returns success (the service layer has
// already loaded and owned the skill). editorActor, when non-empty,
// re-validates editor qualification inside the transaction.
func (r *PgSkillRevisionRepo) DiscardDraft(
 ctx context.Context,
 skillID, editorActor string,
 audit *auditdomain.ResourceChangeAuditEvent,
) error {
 return r.execTenant(ctx, func(ctx context.Context, tx pgx.Tx) error {
  if err := revalidateEditorIfActor(ctx, tx, resourceEditorKind, skillID, editorActor); err != nil {
   return err
  }
  if _, err := tx.Exec(ctx, `DELETE FROM skill_revisions WHERE skill_id=$1 AND status='draft'`, skillID); err != nil {
   return err
  }
  if _, err := tx.Exec(ctx,
   `UPDATE skills SET draft_revision_id=NULL, updated_at=NOW() WHERE id=$1`, skillID,
  ); err != nil {
   return err
  }
  return insertChangeAudit(ctx, tx, audit)
 })
}
```

> 注：`expectInsertSkillRevision(mock, id, status, source)`（helper 70-76 行）pin INSERT 参数；draft 插入时 `revision_no=0` 经 `NULLIF($4,0)` 落 NULL。若该 helper 内部对 revision_no 的 pin 与 draft 场景不符，先读其源码（70-76 行）按 `insertSkillRevision`（375-400 行）的参数顺序修正 helper——实现细节，非契约变更。

- [ ] **Step 4: 运行以确认通过**

Run:

```bash
cd /home/yang/go-projects/stratum-kb-version-skill-draft && go test ./internal/skill/infrastructure/persistence/ -run 'TestPgSkillRevisionRepo_(SaveDraft|PublishDraft|DiscardDraft)' -v 2>&1 | tail -30
```

Expected: 7 个用例全部 PASS（3 SaveDraft + 2 PublishDraft + 1 DiscardDraft + stale）。

- [ ] **Step 5: Commit**

```bash
cd /home/yang/go-projects/stratum-kb-version-skill-draft && git add internal/skill/infrastructure/persistence/skill_version_repo.go internal/skill/infrastructure/persistence/skill_version_repo_mock_test.go && git commit -m "feat(skill): implement draft persistence in skill repo

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 4: service 草稿流转 + handler + 路由

**Files:**

- Modify: `internal/skill/application/version_service.go`
- Test: `internal/skill/application/version_service_test.go`（末尾追加用例）
- Modify: `api/http/handler/skill_handler.go`
- Test: `api/http/handler/skill_handler_test.go`
- Modify: `api/http/router.go:429-451`（`registerSkills` 内）

**Interfaces:**

- Consumes: port 三方法（Task 2）、`loadOwnedActive`（442-462）、`contentHashMatches`（547-549）、`newChangeAudit`、`skillSafeProjection`、`resolveNames`、`recordFailure`、`workspaceView`（本任务新增）、`GetRevision`、`NextRevisionNo`、`ValidatePublishable`。
- Produces: `SkillWorkspaceView.HasDraft bool`、`SaveDraftInput{Name,Description,Instructions,ActorID}`、`VersionService.SaveDraft/PublishDraft/DiscardDraft`、handler `SaveSkillDraft/PublishSkillDraft/DiscardSkillDraft`、三条路由。

- [ ] **Step 1: 写失败测试——service 草稿流转**

在 `internal/skill/application/version_service_test.go` 文件末尾追加：

```go
func TestVersionServiceSaveDraft(t *testing.T) {
 svc := NewVersionService(newFakeVersionRepo(), zap.NewNop())
 svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})
 view := mustCreateSkill(t, svc)

 out, err := svc.SaveDraft(context.Background(), view.Skill.ID, view.Active.ContentHash,
  SaveDraftInput{Name: "draft-name", Description: "draft-desc", Instructions: "draft-ins", ActorID: "user-1"})
 require.NoError(t, err)
 require.True(t, out.HasDraft)
 // 保存草稿不改变当前生效版本。
 require.Equal(t, view.Active.ID, out.Active.ID)
 require.Equal(t, view.Active.ContentHash, out.Active.ContentHash)

 // 再次保存 = 覆盖更新,草稿 id 复用(service 复用 skill.DraftRevisionID)。
 out2, err := svc.SaveDraft(context.Background(), view.Skill.ID, view.Active.ContentHash,
  SaveDraftInput{Name: "draft-2", Description: "draft-desc", Instructions: "draft-ins", ActorID: "user-1"})
 require.NoError(t, err)
 require.True(t, out2.HasDraft)
 require.Equal(t, out.Skill.DraftRevisionID, out2.Skill.DraftRevisionID)
}

func TestVersionServiceSaveDraftStale(t *testing.T) {
 svc := NewVersionService(newFakeVersionRepo(), zap.NewNop())
 svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})
 view := mustCreateSkill(t, svc)

 _, err := svc.SaveDraft(context.Background(), view.Skill.ID, "stale-hash",
  SaveDraftInput{Name: "n", Description: "d", Instructions: "i", ActorID: "user-1"})
 require.ErrorIs(t, err, domain.ErrSkillDraftStale)
}

func TestVersionServicePublishDraft(t *testing.T) {
 svc := NewVersionService(newFakeVersionRepo(), zap.NewNop())
 svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})
 view := mustCreateSkill(t, svc)
 _, err := svc.SaveDraft(context.Background(), view.Skill.ID, view.Active.ContentHash,
  SaveDraftInput{Name: "draft-name", Description: "draft-desc", Instructions: "draft-ins", ActorID: "user-1"})
 require.NoError(t, err)

 out, err := svc.PublishDraft(context.Background(), view.Skill.ID, view.Active.ContentHash, "user-1")
 require.NoError(t, err)
 require.False(t, out.HasDraft)
 require.Equal(t, "draft-name", out.Active.Name)
 require.Equal(t, view.Active.RevisionNo+1, out.Active.RevisionNo)
 // 旧生效版本降级。
 revisions, err := svc.ListRevisions(context.Background(), view.Skill.ID)
 require.NoError(t, err)
 require.Len(t, revisions, 2)
 require.Equal(t, domain.VersionStatusDeprecated, revisions[1].Status)
}

func TestVersionServicePublishDraftNoDraft(t *testing.T) {
 svc := NewVersionService(newFakeVersionRepo(), zap.NewNop())
 svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})
 view := mustCreateSkill(t, svc)

 _, err := svc.PublishDraft(context.Background(), view.Skill.ID, view.Active.ContentHash, "user-1")
 require.ErrorIs(t, err, domain.ErrSkillDraftNotFound)
}

func TestVersionServicePublishDraftNotPublishable(t *testing.T) {
 svc := NewVersionService(newFakeVersionRepo(), zap.NewNop())
 svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})
 view := mustCreateSkill(t, svc)
 // SaveDraft 允许空指令(草稿可半成品),发布时 ValidatePublishable 拒绝。
 _, err := svc.SaveDraft(context.Background(), view.Skill.ID, view.Active.ContentHash,
  SaveDraftInput{Name: "draft-name", Description: "draft-desc", Instructions: "", ActorID: "user-1"})
 require.NoError(t, err)

 _, err = svc.PublishDraft(context.Background(), view.Skill.ID, view.Active.ContentHash, "user-1")
 require.ErrorIs(t, err, domain.ErrSkillNotPublishable)
 // 校验失败不破坏既有草稿:再次发布仍报 NotPublishable 而非 NotFound。
 _, err = svc.PublishDraft(context.Background(), view.Skill.ID, view.Active.ContentHash, "user-1")
 require.ErrorIs(t, err, domain.ErrSkillNotPublishable)
}

func TestVersionServiceDiscardDraft(t *testing.T) {
 svc := NewVersionService(newFakeVersionRepo(), zap.NewNop())
 svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})
 view := mustCreateSkill(t, svc)
 _, err := svc.SaveDraft(context.Background(), view.Skill.ID, view.Active.ContentHash,
  SaveDraftInput{Name: "draft-name", Description: "draft-desc", Instructions: "draft-ins", ActorID: "user-1"})
 require.NoError(t, err)

 out, err := svc.DiscardDraft(context.Background(), view.Skill.ID, "user-1")
 require.NoError(t, err)
 require.False(t, out.HasDraft)
 // 表单回填当前生效版本。
 require.Equal(t, view.Active.Name, out.Active.Name)
 require.Equal(t, view.Active.ContentHash, out.Active.ContentHash)

 // 幂等:无草稿再次撤销成功。
 _, err = svc.DiscardDraft(context.Background(), view.Skill.ID, "user-1")
 require.NoError(t, err)
}
```

- [ ] **Step 2: 运行以确认失败**

Run:

```bash
cd /home/yang/go-projects/stratum-kb-version-skill-draft && go test -short ./internal/skill/application/... -run 'TestVersionService(SaveDraft|PublishDraft|DiscardDraft)' 2>&1 | tail -20
```

Expected: 编译失败（`svc.SaveDraft` 等方法未定义）——预期的失败信号。

- [ ] **Step 3: 写最小实现——`SkillWorkspaceView.HasDraft` + `workspaceView` helper**

在 `internal/skill/application/version_service.go`：

**(a)** `SkillWorkspaceView`（32-38 行）加 `HasDraft` 字段：

```go
type SkillWorkspaceView struct {
 Skill SkillProduct
 // Active is the currently effective revision; empty when a legacy
 // unpublished skill (no active_revision_id) is still to produce its first version.
 Active domain.SkillRevision
 Editors []string
 // HasDraft marks an unpublished draft revision existing for the skill
 // (skills.draft_revision_id set). The frontend shows a draft banner and
 // gates the publish entry.
 HasDraft bool
}
```

**(b)** 在 `SaveRevisionInput`（40-46 行）之后加 `SaveDraftInput`：

```go
type SaveDraftInput struct {
 Name         string
 Description  string
 Instructions string
 // ActorID is the caller; ownership is checked against the skill's created_by.
 ActorID string
}
```

**(c)** `GetWorkspace`（194 行 `view := SkillWorkspaceView{Skill: skill, Editors: editors}`）填充 `HasDraft`：

```go
 view := SkillWorkspaceView{Skill: skill, Editors: editors, HasDraft: skill.DraftRevisionID != ""}
```

**(d)** 新增 `workspaceView` helper（放在 `GetWorkspace` 之后）：

```go
// workspaceView 组装写后视图:Skill 行 + active 版本(填充昵称)+ 空 editors。
// HasDraft 由 skill 行 draft_revision_id 推导。供 SaveDraft/PublishDraft/DiscardDraft 复用。
func (s *VersionService) workspaceView(ctx context.Context, skill port.SkillProductRow, active *domain.SkillRevision) (SkillWorkspaceView, error) {
 view := SkillWorkspaceView{Skill: skill, Editors: []string{}, HasDraft: skill.DraftRevisionID != ""}
 if active != nil {
  view.Active = *active
  if err := s.resolveNames(ctx, &view.Active); err != nil {
   return SkillWorkspaceView{}, err
  }
 }
 return view, nil
}
```

- [ ] **Step 4: 写最小实现——service 三方法**

在 `internal/skill/application/version_service.go` 的 `SaveRevision`（541 行）之后、`contentHashMatches`（543 行）之前追加：

```go
// SaveDraft persists (or overwrites) the skill's single draft revision without
// touching the active revision — the saved content is NOT effective until an
// explicit PublishDraft. expectedContentHash is the optimistic-concurrency
// baseline taken from the current active revision (409 on mismatch). A legacy
// unpublished skill (nil active) accepts any baseline and keeps an empty active.
// Overwriting reuses skill.DraftRevisionID as the draft id (row id kept); a
// fresh draft gets a new id and repoints skills.draft_revision_id.
func (s *VersionService) SaveDraft(
 ctx context.Context,
 skillID, expectedContentHash string,
 in SaveDraftInput,
) (SkillWorkspaceView, error) {
 skill, active, editorActor, err := s.loadOwnedActive(ctx, skillID, in.ActorID)
 if err != nil {
  return SkillWorkspaceView{}, err
 }
 if !contentHashMatches(expectedContentHash, active) {
  return SkillWorkspaceView{}, domain.ErrSkillDraftStale
 }
 // before 投影在构造草稿前定格当前生效内容。
 var beforeRev *domain.SkillRevision
 if active != nil {
  rev := *active
  beforeRev = &rev
 }
 before := skillSafeProjection(skill, beforeRev)
 draftID := skill.DraftRevisionID
 if draftID == "" {
  draftID = uuid.Must(uuid.NewV7()).String()
 }
 draft := domain.SkillRevision{
  ID:                 draftID,
  SkillID:            skillID,
  Status:             domain.VersionStatusDraft,
  Source:             "manual",
  RevisionNo:         0, // 草稿不占版本号(insertSkillRevision NULLIF(0))
  GenerationMetadata: map[string]any{},
  Name:               strings.TrimSpace(in.Name),
  Description:        strings.TrimSpace(in.Description),
  Instructions:       strings.TrimSpace(in.Instructions),
  CreatedBy:          in.ActorID,
 }
 hash, err := draft.ComputeContentHash()
 if err != nil {
  return SkillWorkspaceView{}, err
 }
 draft.ContentHash = hash
 audit, err := newChangeAudit(ctx, auditdomain.ResourceKindSkill, skillID, auditdomain.ChangeOpUpdate, in.ActorID,
  before, skillSafeProjection(skill, &draft))
 if err != nil {
  return SkillWorkspaceView{}, err
 }
 if err := s.repo.SaveDraft(ctx, skillID, expectedContentHash, draft, editorActor, audit); err != nil {
  s.recordFailure(ctx, skillID, "update", err)
  return SkillWorkspaceView{}, err
 }
 skill.DraftRevisionID = draftID
 return s.workspaceView(ctx, skill, active)
}

// PublishDraft promotes the skill's draft to the new active published version:
// revision_no assigned, the old active deprecated, active_revision_id repointed
// and draft_revision_id cleared — all inside the repository transaction.
// expectedContentHash guards concurrent edits against the current active
// revision (409 on mismatch). No draft → ErrSkillDraftNotFound. A draft failing
// publish validation leaves the draft intact (ErrSkillNotPublishable, 400).
func (s *VersionService) PublishDraft(
 ctx context.Context,
 skillID, expectedContentHash, actorID string,
) (SkillWorkspaceView, error) {
 skill, active, editorActor, err := s.loadOwnedActive(ctx, skillID, actorID)
 if err != nil {
  return SkillWorkspaceView{}, err
 }
 if !contentHashMatches(expectedContentHash, active) {
  return SkillWorkspaceView{}, domain.ErrSkillDraftStale
 }
 if skill.DraftRevisionID == "" {
  return SkillWorkspaceView{}, domain.ErrSkillDraftNotFound
 }
 draft, ok, err := s.repo.GetRevision(ctx, skillID, skill.DraftRevisionID)
 if err != nil {
  return SkillWorkspaceView{}, err
 }
 if !ok || draft.Status != domain.VersionStatusDraft {
  return SkillWorkspaceView{}, domain.ErrSkillDraftNotFound
 }
 if err := draft.ValidatePublishable(); err != nil {
  return SkillWorkspaceView{}, err // ErrSkillNotPublishable → 400,不破坏草稿
 }
 next, err := s.repo.NextRevisionNo(ctx, skillID)
 if err != nil {
  return SkillWorkspaceView{}, err
 }
 var parentID string
 if active != nil {
  parentID = active.ID
 }
 // before 定格当前 active + 草稿内容;after 定格转正后的草稿。
 after := draft
 after.Status = domain.VersionStatusPublished
 after.RevisionNo = next
 audit, err := newChangeAudit(ctx, auditdomain.ResourceKindSkill, skillID, auditdomain.ChangeOpUpdate, actorID,
  skillSafeProjection(skill, active), skillSafeProjection(skill, &after))
 if err != nil {
  return SkillWorkspaceView{}, err
 }
 if err := s.repo.PublishDraft(ctx, skillID, draft.ID, parentID, next, expectedContentHash, editorActor, audit); err != nil {
  s.recordFailure(ctx, skillID, "publish", err)
  return SkillWorkspaceView{}, err
 }
 skill.ActiveRevisionID = draft.ID
 skill.DraftRevisionID = ""
 after.IsCurrent = true
 return s.workspaceView(ctx, skill, &after)
}

// DiscardDraft deletes the skill's draft revision and clears
// skills.draft_revision_id (idempotent), then returns the current effective
// workspace so the client can refill the form from the active version.
func (s *VersionService) DiscardDraft(ctx context.Context, skillID, actorID string) (SkillWorkspaceView, error) {
 skill, active, editorActor, err := s.loadOwnedActive(ctx, skillID, actorID)
 if err != nil {
  return SkillWorkspaceView{}, err
 }
 beforeRev := active
 skillAfter := skill
 skillAfter.DraftRevisionID = ""
 audit, err := newChangeAudit(ctx, auditdomain.ResourceKindSkill, skillID, auditdomain.ChangeOpUpdate, actorID,
  skillSafeProjection(skill, beforeRev), skillSafeProjection(skillAfter, beforeRev))
 if err != nil {
  return SkillWorkspaceView{}, err
 }
 if err := s.repo.DiscardDraft(ctx, skillID, editorActor, audit); err != nil {
  s.recordFailure(ctx, skillID, "discard", err)
  return SkillWorkspaceView{}, err
 }
 return s.workspaceView(ctx, skillAfter, active)
}
```

- [ ] **Step 5: 运行 service 测试以确认通过**

Run:

```bash
cd /home/yang/go-projects/stratum-kb-version-skill-draft && go test -short ./internal/skill/application/... -run 'TestVersionService' 2>&1 | tail -5
```

Expected: 全部 PASS（新增 6 个用例 + 既有用例）。

- [ ] **Step 6: 写失败测试——handler 三方法**

在 `api/http/handler/skill_handler_test.go`：

**(a)** `fakeSkillRevisionService`（21-70 行）struct 加字段 `savedDraft skillapp.SaveDraftInput`，并追加三方法：

```go
func (f *fakeSkillRevisionService) SaveDraft(_ context.Context, _, hash string, input skillapp.SaveDraftInput) (skillapp.SkillWorkspaceView, error) {
 f.savedDraft = input
 f.gotHash = hash
 return skillapp.SkillWorkspaceView{
  Skill: skillapp.SkillProduct{ID: "skill-1", Name: input.Name, Description: input.Description,
   Status: "published", ActiveRevisionID: "revision-1", DraftRevisionID: "draft-1"},
  Active: domain.SkillRevision{
   ID: "revision-1", SkillID: "skill-1", RevisionNo: 1, Status: domain.VersionStatusPublished,
   Name: "complaint", Description: "分类", Instructions: "分类投诉",
  },
  HasDraft: true,
 }, nil
}
func (f *fakeSkillRevisionService) PublishDraft(_ context.Context, _, hash, _ string) (skillapp.SkillWorkspaceView, error) {
 f.gotHash = hash
 return skillapp.SkillWorkspaceView{
  Skill: skillapp.SkillProduct{ID: "skill-1", Name: "draft-name", Description: "draft-desc",
   Status: "published", ActiveRevisionID: "draft-1"},
  Active: domain.SkillRevision{
   ID: "draft-1", SkillID: "skill-1", RevisionNo: 2, Status: domain.VersionStatusPublished,
   Name: "draft-name", Description: "draft-desc", Instructions: "draft-ins",
  },
 }, nil
}
func (f *fakeSkillRevisionService) DiscardDraft(_ context.Context, _, _ string) (skillapp.SkillWorkspaceView, error) {
 return skillapp.SkillWorkspaceView{
  Skill: skillapp.SkillProduct{ID: "skill-1", Name: "complaint", Description: "分类",
   Status: "published", ActiveRevisionID: "revision-1"},
  Active: domain.SkillRevision{
   ID: "revision-1", SkillID: "skill-1", RevisionNo: 1, Status: domain.VersionStatusPublished,
   Name: "complaint", Description: "分类", Instructions: "分类投诉",
  },
 }, nil
}
```

**(b)** `newSkillTestRouter`（72-93 行）的 switch 加 DELETE case：

```go
 case http.MethodDelete:
  router.DELETE(path, handler)
```

**(c)** 文件末尾追加三个用例：

```go
func TestSkillHandlerSaveSkillDraft(t *testing.T) {
 service := &fakeSkillRevisionService{}
 handler := NewSkillHandler(service, zap.NewNop())
 router := newSkillTestRouter(http.MethodPost, "/skills/:id/draft", handler.SaveSkillDraft)
 body, _ := json.Marshal(gen.SaveSkillDraftRequest{
  Name: "draft-name", Description: "draft-desc", Instructions: "draft-ins",
  ExpectedContentHash: "h-1",
 })
 w := httptest.NewRecorder()
 req := httptest.NewRequest(http.MethodPost, "/skills/skill-1/draft", bytes.NewReader(body))
 req.Header.Set("Content-Type", "application/json")
 router.ServeHTTP(w, req)
 if w.Code != http.StatusOK {
  t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
 }
 if service.savedDraft.Instructions != "draft-ins" || service.savedDraft.Name != "draft-name" {
  t.Fatalf("draft input not forwarded: %#v", service.savedDraft)
 }
 if service.gotHash != "h-1" {
  t.Fatalf("expectedContentHash not forwarded: %q", service.gotHash)
 }
 var resp gen.SkillWorkspaceResponse
 if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
  t.Fatalf("decode workspace: %v", err)
 }
 if !resp.HasDraft {
  t.Fatalf("expected HasDraft=true, got false")
 }
}

func TestSkillHandlerPublishSkillDraft(t *testing.T) {
 service := &fakeSkillRevisionService{}
 handler := NewSkillHandler(service, zap.NewNop())
 router := newSkillTestRouter(http.MethodPost, "/skills/:id/publish", handler.PublishSkillDraft)
 body, _ := json.Marshal(gen.PublishSkillDraftRequest{ExpectedContentHash: "h-1"})
 w := httptest.NewRecorder()
 req := httptest.NewRequest(http.MethodPost, "/skills/skill-1/publish", bytes.NewReader(body))
 req.Header.Set("Content-Type", "application/json")
 router.ServeHTTP(w, req)
 if w.Code != http.StatusOK {
  t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
 }
 if service.gotHash != "h-1" {
  t.Fatalf("expectedContentHash not forwarded: %q", service.gotHash)
 }
}

func TestSkillHandlerDiscardSkillDraft(t *testing.T) {
 service := &fakeSkillRevisionService{}
 handler := NewSkillHandler(service, zap.NewNop())
 router := newSkillTestRouter(http.MethodDelete, "/skills/:id/draft", handler.DiscardSkillDraft)
 w := httptest.NewRecorder()
 req := httptest.NewRequest(http.MethodDelete, "/skills/skill-1/draft", nil)
 router.ServeHTTP(w, req)
 if w.Code != http.StatusOK {
  t.Fatalf("expected 200, got %d body=%s", w.Code, w.Body.String())
 }
}
```

- [ ] **Step 7: 运行以确认失败**

Run:

```bash
cd /home/yang/go-projects/stratum-kb-version-skill-draft && go test ./api/http/handler/ -run 'TestSkillHandler' 2>&1 | tail -15
```

Expected: 编译失败（`skillRevisionService` interface 新增方法未在 fake 实现；`gen.SaveSkillDraftRequest` 未在 handler 使用前已由 Task 1 生成）。——预期的失败信号。

- [ ] **Step 8: 写最小实现——handler interface + 三方法 + `workspaceToResponse`**

在 `api/http/handler/skill_handler.go`：

**(a)** `skillRevisionService` interface（21-30 行）追加：

```go
 SaveDraft(context.Context, string, string, skillapp.SaveDraftInput) (skillapp.SkillWorkspaceView, error)
 PublishDraft(context.Context, string, string, string) (skillapp.SkillWorkspaceView, error)
 DiscardDraft(context.Context, string, string) (skillapp.SkillWorkspaceView, error)
```

**(b)** `workspaceToResponse`（190-192 行）加 `HasDraft`：

```go
func workspaceToResponse(value skillapp.SkillWorkspaceView) gen.SkillWorkspaceResponse {
 return gen.SkillWorkspaceResponse{
  Skill: productToResponse(value.Skill), Active: revisionToResponse(value.Active),
  Editors: value.Editors, HasDraft: value.HasDraft,
 }
}
```

**(c)** 在 `UpdateSkill`（109 行）之后追加三个 handler：

```go
// SaveSkillDraft persists the skill's draft without making it effective.
// expectedContentHash is the optimistic-concurrency baseline (current active
// content hash); the saved draft becomes effective only after PublishSkillDraft.
func (h *SkillHandler) SaveSkillDraft(c *gin.Context) {
 var req gen.SaveSkillDraftRequest
 if err := c.ShouldBindJSON(&req); err != nil {
  _ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
  return
 }
 actorID, ok := userIDFromCtx(c)
 if !ok {
  respondMissingUser(c)
  return
 }
 view, err := h.service.SaveDraft(c.Request.Context(), c.Param("id"), req.ExpectedContentHash,
  skillapp.SaveDraftInput{
   Name: req.Name, Description: req.Description, Instructions: req.Instructions,
   ActorID: actorID,
  })
 if err != nil {
  _ = c.Error(err)
  return
 }
 c.JSON(http.StatusOK, workspaceToResponse(view))
}

// PublishSkillDraft promotes the skill's draft to the new active published
// version (immediately effective).
func (h *SkillHandler) PublishSkillDraft(c *gin.Context) {
 var req gen.PublishSkillDraftRequest
 if err := c.ShouldBindJSON(&req); err != nil {
  _ = c.Error(middleware.NewHTTPError(http.StatusBadRequest, err))
  return
 }
 actorID, ok := userIDFromCtx(c)
 if !ok {
  respondMissingUser(c)
  return
 }
 view, err := h.service.PublishDraft(c.Request.Context(), c.Param("id"), req.ExpectedContentHash, actorID)
 if err != nil {
  _ = c.Error(err)
  return
 }
 c.JSON(http.StatusOK, workspaceToResponse(view))
}

// DiscardSkillDraft deletes the skill's draft and returns the workspace so the
// client refills the form from the active version. No request body.
func (h *SkillHandler) DiscardSkillDraft(c *gin.Context) {
 actorID, ok := userIDFromCtx(c)
 if !ok {
  respondMissingUser(c)
  return
 }
 view, err := h.service.DiscardDraft(c.Request.Context(), c.Param("id"), actorID)
 if err != nil {
  _ = c.Error(err)
  return
 }
 c.JSON(http.StatusOK, workspaceToResponse(view))
}
```

- [ ] **Step 9: 写最小实现——路由**

在 `api/http/router.go` 的 `registerSkills` 内 `skills.PATCH("/:id", requireActive, skillHandler.UpdateSkill)` 之后追加：

```go
  // 草稿流转：保存/发布/撤销草稿与编辑同级(member 级 requireActive)，
  // 编辑人经 service 白名单校验；发布/保存共用乐观并发基线。
  skills.POST("/:id/draft", requireActive, skillHandler.SaveSkillDraft)
  skills.POST("/:id/publish", requireActive, skillHandler.PublishSkillDraft)
  skills.DELETE("/:id/draft", requireActive, skillHandler.DiscardSkillDraft)
```

- [ ] **Step 10: 运行全部相关测试确认通过**

Run:

```bash
cd /home/yang/go-projects/stratum-kb-version-skill-draft && go build ./... && go test -short ./internal/skill/... ./api/http/handler/... 2>&1 | tail -8
```

Expected: 编译通过；`internal/skill/...` 与 `api/http/handler/...` 全部 PASS。

- [ ] **Step 11: Commit**

```bash
cd /home/yang/go-projects/stratum-kb-version-skill-draft && git add internal/skill/application/version_service.go internal/skill/application/version_service_test.go api/http/handler/skill_handler.go api/http/handler/skill_handler_test.go api/http/router.go && git commit -m "feat(skill): wire draft save/publish/discard service and api

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 5: 前端 SkillWorkspacePage 草稿编辑流

**Files:**

- Modify: `web/src/modules/skill/api/skill.api.ts`
- Modify: `web/src/modules/skill/pages/SkillWorkspacePage.tsx`

**Interfaces:**

- Consumes: `skillWorkspaceSchema`（Task 1 加 `hasDraft`）、`gen.SaveSkillDraftRequest`/`gen.PublishSkillDraftRequest`（Task 1 生成）、`workspaceToResponse.HasDraft`（Task 4）。
- Produces: `skillApi.saveDraft/publishDraft/discardDraft`；页面三按钮 + 草稿提示条 + 版本历史过滤。

- [ ] **Step 1: `skill.api.ts` 增加三个草稿方法**

在 `web/src/modules/skill/api/skill.api.ts` 的 `updateSkill`（20-21 行）之后追加：

```ts
  // saveDraft: 保存草稿,不立即生效;发布后才成为当前生效版本。
  // expectedContentHash 取当前 active.contentHash,并发编辑时后端返回 409。
  saveDraft: async (id: string, data: SaveSkillDraftRequest): Promise<SkillWorkspace> =>
    skillWorkspaceSchema.parse((await api.post(`/skills/${id}/draft`, data)).data),
  // publishDraft: 将草稿提升为新的生效版本(版本号分配、旧版降级、指针重指)。
  publishDraft: async (id: string, data: PublishSkillDraftRequest): Promise<SkillWorkspace> =>
    skillWorkspaceSchema.parse((await api.post(`/skills/${id}/publish`, data)).data),
  // discardDraft: 删除草稿,返回当前生效版本供表单回填;幂等。
  discardDraft: async (id: string): Promise<SkillWorkspace> =>
    skillWorkspaceSchema.parse((await api.delete(`/skills/${id}/draft`)).data),
```

并在 import 行（第 9 行）扩展类型：

```ts
import type { PublishSkillDraftRequest, SaveSkillDraftRequest, SkillRevisionsResponse, UpdateSkillRequest } from '@/services/gen/skill';
```

- [ ] **Step 2: `SkillWorkspacePage.tsx` 改造表单按钮区**

在 `web/src/modules/skill/pages/SkillWorkspacePage.tsx`：

**(a)** 第 65 行解构加 `hasDraft`：

```tsx
  const { skill, active, hasDraft } = workspace;
```

**(b)** 将 `saveRevision`（76-91 行）替换为三个草稿动作：

```tsx
  // saveDraft: 保存草稿不生效;expectedContentHash 取当前生效版本,并发编辑 409。
  const saveDraft = async (values: DraftValues) => {
    setSaving('draft');
    try {
      const next = await skillApi.saveDraft(skill.id, {
        name: values.name, description: values.description, instructions: values.instructions,
        expectedContentHash: active.contentHash,
      });
      applyWorkspace(next);
      setRefreshTick((t) => t + 1);
      message.success({ content: '已保存草稿，发布后生效', duration: 2 });
    } catch (err) {
      message.error({ content: extractErrorMessage(err) || '保存草稿失败', duration: 3 });
    } finally {
      setSaving('');
    }
  };
  // publishDraft: 将草稿转正为新生效版本,立即生效。
  const publishDraft = async () => {
    setSaving('publish');
    try {
      const next = await skillApi.publishDraft(skill.id, { expectedContentHash: active.contentHash });
      applyWorkspace(next);
      setRefreshTick((t) => t + 1);
      message.success({ content: '已发布，立即生效', duration: 2 });
    } catch (err) {
      message.error({ content: extractErrorMessage(err) || '发布失败', duration: 3 });
    } finally {
      setSaving('');
    }
  };
  // discardDraft: 撤销草稿,删除后用当前生效版本回填表单;幂等。
  const discardDraft = () => {
    Modal.confirm({
      title: '撤销草稿？',
      content: '草稿将被删除，表单回填为当前生效版本。',
      okText: '撤销', okButtonProps: { danger: true }, cancelText: '取消',
      onOk: async () => {
        try {
          const next = await skillApi.discardDraft(skill.id);
          applyWorkspace(next);
          setRefreshTick((t) => t + 1);
          message.success({ content: '草稿已撤销', duration: 2 });
        } catch (err) {
          message.error({ content: extractErrorMessage(err) || '撤销失败', duration: 3 });
        }
      },
    });
  };
```

**(c)** import 增加 `Modal`（第 2 行 antd import）：

```tsx
import { Alert, Button, Form, Input, Modal, Select, Skeleton, Tabs, Typography, message } from 'antd';
```

**(d)** 指令 tab（128-136 行）：`onFinish={saveDraft}`，表单上方加草稿提示条，按钮区替换为三按钮：

```tsx
      { key: 'instructions', label: '指令', children: <div>
        {hasDraft && <Alert type="warning" showIcon style={{ marginBottom: 12 }}
          message="有未发布的草稿：保存的内容尚未生效，点击「发布」后成为当前生效版本。" />}
        <Form disabled={!canEdit} form={draftForm} layout="vertical" onFinish={saveDraft}>
          <Form.Item label="名称" name="name" rules={[{ required: true, message: '请输入技能名称' }]}><Input /></Form.Item>
          <Form.Item label="描述" name="description"><TextArea rows={3} /></Form.Item>
          <Form.Item label="执行指令" name="instructions" rules={[{ required: true, message: '请输入执行指令' }]}><TextArea rows={10} /></Form.Item>
          {canEdit && <ActionRow>
            <Button danger disabled={!hasDraft} loading={saving === 'discard'} onClick={discardDraft} style={{ marginInlineEnd: 8 }}>撤销草稿</Button>
            <Button htmlType="submit" loading={saving === 'draft'}>保存草稿</Button>
            <Button type="primary" disabled={!hasDraft} loading={saving === 'publish'} onClick={() => void publishDraft()} style={{ marginInlineStart: 8 }}>发布</Button>
          </ActionRow>}
        </Form>
        {!canEdit && <ActionRow><Button type="primary" icon={<LockOutlined />} loading={requesting} onClick={() => void handleRequestEditor()}>申请编辑权限</Button></ActionRow>}
      </div> },
```

- [ ] **Step 3: 版本历史过滤 draft 行**

将 `SkillWorkspacePage.tsx` 版本历史 tab（179-183 行）的 rows 加过滤：

```tsx
          rows={(revisions ?? []).filter((r) => r.status !== 'draft').map((r) => ({
            id: r.id, versionNo: r.revisionNo, status: r.status, isCurrent: r.isCurrent,
            createdByName: r.createdByName, createdBy: r.createdBy, createdAt: r.createdAt,
            canRollback: r.status === 'deprecated' && canEdit,
          }))}
```

- [ ] **Step 4: 运行前端 lint 与构建验证**

Run:

```bash
cd /home/yang/go-projects/stratum-kb-version-skill-draft/web && npx tsc --noEmit && npx eslint src/modules/skill/pages/SkillWorkspacePage.tsx src/modules/skill/api/skill.api.ts
```

Expected: tsc 与 eslint 均无错误（`Modal`、`hasDraft`、三方法全部可解析；`hasDraft` 已由 Task 1 schema 提供类型）。

- [ ] **Step 5: Commit**

```bash
cd /home/yang/go-projects/stratum-kb-version-skill-draft && git add web/src/modules/skill/api/skill.api.ts web/src/modules/skill/pages/SkillWorkspacePage.tsx && git commit -m "feat(skill): add draft editing flow to skill workspace page

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 6: 全量回归

**Files:** 无新增（回归验证）。

- [ ] **Step 1: 后端快速验证**

Run:

```bash
cd /home/yang/go-projects/stratum-kb-version-skill-draft && go vet ./... && go test -short ./... 2>&1 | tail -15
```

Expected: `go vet` 无输出；`go test -short ./...` 全部 PASS（含新 contract 相关——skill 无 contract golden，契约测试不受影响）。

- [ ] **Step 2: 代码质量门禁**

Run:

```bash
cd /home/yang/go-projects/stratum-kb-version-skill-draft && make code-quality 2>&1 | tail -15
```

Expected: 通过（新增方法圈复杂度 ≤10、认知 ≤15、行数 ≤120、嵌套 ≤4；未触碰基线）。

- [ ] **Step 3: 前端构建**

Run:

```bash
cd /home/yang/go-projects/stratum-kb-version-skill-draft && make fe-lint && make fe-build
```

Expected: 通过。

- [ ] **Step 4: 确认迁移与租户 schema 无改动**

Run:

```bash
cd /home/yang/go-projects/stratum-kb-version-skill-draft && git diff --stat pkg/migration/sql/ pkg/storage/postgres/tenant_schema.sql
```

Expected: 无输出（本计划零 DDL 改动——`draft_revision_id` 列与 `idx_skill_revisions_one_draft` 已存在）。

- [ ] **Step 5: Commit（如有残留）**

Run:

```bash
cd /home/yang/go-projects/stratum-kb-version-skill-draft && git status --short
```

若全绿无残留则跳过本步；若有意外改动，审查后单独提交 `chore(skill): regression for draft flow`（带 Co-Authored-By）。

---

## Self-Review

**1. Spec 覆盖：**

| Spec 要求 | 落点 |
|---|---|
| 草稿保存不生效 | Task 4 `SaveDraft`（不动 active）+ Task 3 repo UPDATE/INSERT 语义 |
| 再次保存 = 覆盖草稿 | Task 4 service 复用 `skill.DraftRevisionID` + Task 3 UPDATE 命中 |
| 发布 = 转正 + 版本号 + 旧版降级 + 指针重指 + 清空草稿指针 | Task 3 `PublishDraft` + Task 4 `PublishDraft` |
| 无草稿发布 409 | `ErrSkillDraftNotFound`（Task 4） |
| 校验失败不破坏草稿 | Task 4 `ValidatePublishable` 事务前调用（400，Global Constraints） |
| 撤销草稿幂等 + 回填生效版 | Task 3 `DiscardDraft` 幂等 + Task 4 返回 workspaceView + Task 5 `applyWorkspace` |
| 前端三按钮 + 提示条 + 版本历史过滤 draft | Task 5 |
| 存量无草稿兼容（首存 = 草稿） | Task 5 `hasDraft` default false + Task 4 legacy nil active 兜底 |
| 乐观并发 409 | `expectedContentHash`（Task 4 + Task 5 传 `active.contentHash`） |
| 并发发布/保存串行化 | Global Constraints #4（同一基线） |
| `skills.status` 产品行不被草稿保存改变 | Task 3 `SaveDraft` 不 UPDATE skills.status；`PublishDraft` 才置 'published' |

**2. Placeholder 扫描：** 无 TBD/占位符；所有任务含完整代码、精确路径、验证命令与期望输出。

**3. 类型一致性：**

- `SkillWorkspaceView.HasDraft`（Task 4 定义）→ `workspaceToResponse.HasDraft`（Task 4）→ proto `has_draft=4`（Task 1）→ `skillWorkspaceSchema.hasDraft`（Task 1）→ 页面解构（Task 5）。
- port `SaveDraft/PublishDraft/DiscardDraft` 签名（Task 2）→ repo 实现（Task 3）→ service 调用（Task 4）签名逐字段一致（含 `nextRevisionNo int`、`editorActor string`）。
- `SaveDraftInput{Name,Description,Instructions,ActorID}`（Task 4）→ handler `SaveSkillDraft` bind `gen.SaveSkillDraftRequest` 字段映射一致。
- 路由 `POST /:id/draft`、`POST /:id/publish`、`DELETE /:id/draft`（Task 4）→ `skillApi` 对应方法与路径（Task 5）一致。
- handler 测试 `fakeSkillRevisionService` 补全三方法（Task 4）使 interface 满足；`newSkillTestRouter` 加 DELETE case 供 DiscardSkillDraft。
