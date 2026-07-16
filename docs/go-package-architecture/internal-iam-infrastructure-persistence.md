# internal/iam/infrastructure/persistence

该包以 PostgreSQL（以及令牌撤销所需的 Redis）实现 IAM 租户、入驻、成员设置、Schema 清理和刷新令牌存储。

完整导入路径：`github.com/byteBuilderX/stratum/internal/iam/infrastructure/persistence`

```mermaid
flowchart TB
  subgraph pkg["iam/infrastructure/persistence"]
    admin["admin_tenant_repo.go<br/>AdminTenantRepo<br/>租户分页、CRUD、ProvisionSchema"]
    onboard["onboard_repo.go<br/>OnboardRepo<br/>用户/租户事务、默认租户、访客"]
    tenant["tenant_repo.go<br/>TenantRepo<br/>成员与 settings JSONB"]
    cleaner["tenant_schema_cleaner.go<br/>TenantSchemaCleaner<br/>校验 UUID 后 DROP SCHEMA"]
    token["token_store.go<br/>TokenStore<br/>SHA-256、轮换、撤销、黑名单"]
  end
  domain["internal/iam/domain"]
  tenantdb["pkg/tenantdb"]
  pg["外部：pgx/v5 + PostgreSQL"]
  redis["外部：go-redis/v9 + Redis"]
  tests["测试汇总<br/>token_store_test.go"]
  admin --> domain
  onboard --> domain
  tenant --> domain
  token --> domain
  admin --> tenantdb
  admin --> pg
  onboard --> pg
  tenant --> pg
  cleaner --> pg
  token --> pg
  token --> redis
  pkg -.-> tests
```

三个 Repo 将 SQL 结果映射为领域类型；入驻和租户创建使用事务并创建租户 Schema。`TokenStore` 只持久化原始令牌的 SHA-256，Redis 保存带剩余 TTL 的撤销标记；`TenantSchemaCleaner` 对动态 Schema 名先做 UUID 格式校验。
