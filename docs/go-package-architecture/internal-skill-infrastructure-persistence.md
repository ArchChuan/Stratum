# internal/skill/infrastructure/persistence

该包以 tenant PostgreSQL 事务实现版本化 Skill 仓储。

完整导入路径：`github.com/byteBuilderX/stratum/internal/skill/infrastructure/persistence`

```mermaid
flowchart LR
  repo["skill_version_repo.go<br/>PgSkillRevisionRepo<br/>skill/revision CRUD · candidate · publish"]
  ports["internal/skill/domain/port<br/>VersionRepo"]
  domain["internal/skill/domain<br/>SkillRevision"]
  tenantdb["pkg/tenantdb<br/>ExecTenant"]
  pg["pgx/v5 · pgxpool"]
  repo -.实现.-> ports
  repo --> domain
  repo --> tenantdb
  repo --> pg
```

`PgSkillRevisionRepo` 对 capability、activation contract、requirements、generation metadata 和 publish checks 做显式 JSON 编解码。发布在单个 tenant 事务中弃用旧 published revision、发布 draft，并更新 `skills.active_revision_id`。
