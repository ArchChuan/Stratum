# internal/iam/application

该包编排 IAM 用例：平台租户管理、RS256 令牌、用户入驻与租户成员/设置管理，并通过领域端口隔离持久化和清理设施。

完整导入路径：`github.com/byteBuilderX/stratum/internal/iam/application`

```mermaid
flowchart LR
  subgraph pkg["iam/application"]
    admin["admin_service.go<br/>AdminService / AdminListResult<br/>List·Create·Update·DeleteTenant"]
    jwt["jwt_service.go<br/>JWTService / TokenClaims / OnboardingClaims<br/>Sign·Verify"]
    onboard["onboard_service.go<br/>OnboardService / GuestAccount<br/>租户入驻、访客与成员查询"]
    tenant["tenant_service.go<br/>TenantService / TenantGatewayCache<br/>成员权限、设置加密、模型锁定"]
  end
  ports["internal/iam/domain/port<br/>AdminTenantRepo / OnboardRepo / TenantRepo<br/>清理与缓存端口"]
  domain["internal/iam/domain<br/>Tenant / Member / SystemRole / errors"]
  constants["pkg/constants"]
  crypto["pkg/crypto"]
  ext["外部：golang-jwt/jwt v5 · google/uuid · zap"]
  tests["测试汇总<br/>jwt_service_test.go<br/>onboard_service_test.go<br/>tenant_service_test.go"]
  admin --> ports
  admin --> domain
  onboard --> ports
  onboard --> domain
  tenant --> ports
  tenant --> domain
  tenant --> crypto
  admin --> constants
  onboard --> constants
  jwt --> domain
  jwt --> ext
  pkg -.-> tests
```

`New*Service` 构造函数注入各领域端口。`AdminService` 在删除租户主记录后依次调用向量、Schema 与缓存清理端口；`TenantService` 实施角色约束、加密/掩码 API key，并通过最小缓存接口失效租户网关。JWT 服务直接使用 RSA 密钥和 `golang-jwt/jwt` 完成签发与校验。
