# internal/iam/domain/port

该包声明 IAM 消费方所需的持久化、OAuth、会话、令牌、租户 Schema/向量清理与缓存失效契约。

完整导入路径：`github.com/byteBuilderX/stratum/internal/iam/domain/port`

```mermaid
flowchart TB
  subgraph pkg["iam/domain/port"]
    admin["admin_tenant_repo.go<br/>AdminTenantRepo"]
    oauth["oauth_provider.go<br/>GitHubProfile / GitHubOAuthClient"]
    onboard["onboard_repo.go<br/>OnboardRepo"]
    schema["schema_provisioner.go<br/>TenantSchemaProvisioner"]
    session["session_store.go<br/>SessionStore"]
    cleaner["tenant_cleaner.go<br/>TenantSchemaCleaner / TenantVectorCleaner<br/>TenantCacheInvalidator"]
    tenant["tenant_repo.go<br/>TenantRepo"]
    token["token_store.go<br/>TokenStore / RefreshTokenStore"]
    user["user_repo.go<br/>UserRepo"]
  end
  domain["internal/iam/domain<br/>Tenant、Member、StoredSession 与输入模型"]
  admin --> domain
  onboard --> domain
  session --> domain
  tenant --> domain
  token --> domain
  user --> domain
```

所有文件均为接口或接口使用的数据形状；应用层依赖这些契约，基础设施在外层实现它们。该包没有测试文件，也没有关键第三方依赖。
