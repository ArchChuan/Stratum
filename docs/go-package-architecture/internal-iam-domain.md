# internal/iam/domain

该包定义 IAM 的领域数据、系统角色推导规则、租户管理输入模型和领域错误，不承担外部 IO。

完整导入路径：`github.com/byteBuilderX/stratum/internal/iam/domain`

```mermaid
flowchart LR
  subgraph pkg["iam/domain"]
    errors["errors.go<br/>ErrTenantNotFound / ErrMemberNotFound 等"]
    iam["iam.go<br/>User / Token / Session"]
    onboard["onboard.go<br/>TenantInfo / CreateTenantInput<br/>CreateTenantResult / AutoJoinInput"]
    role["system_role.go<br/>SystemRole / TenantMembership<br/>DeriveSystemRole / AtLeast"]
    tenant["tenant.go<br/>Member / Tenant / TenantFilter<br/>TenantPatch / StoredSession"]
  end
  constants["pkg/constants<br/>角色/默认值常量"]
  tests["测试汇总<br/>system_role_test.go"]
  role --> constants
  role --> tenant
  onboard --> tenant
  pkg -.-> tests
```

`DeriveSystemRole` 根据租户成员关系统一推导系统角色，`AtLeast` 比较权限等级；其余文件提供应用层与端口共享的稳定领域形状和哨兵错误。
