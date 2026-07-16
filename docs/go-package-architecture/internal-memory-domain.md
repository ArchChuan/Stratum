# internal/memory/domain

该包承载记忆领域实体、事实状态机、作用域规则、frecency 算法、通用记忆模型、ID/时间生成和领域错误。

完整导入路径：`github.com/byteBuilderX/stratum/internal/memory/domain`

```mermaid
flowchart LR
  fact["fact.go<br/>MemoryFact<br/>NewFact · MarkSuperseded · MarkArchived"]
  entity["entity.go<br/>MemoryEntity<br/>NewEntity · profile rebuild 规则"]
  scope["scope.go<br/>Scope · ScopeFilter<br/>ValidateScope · BuildScopeFilter"]
  general["types.go<br/>MemoryEntry · MemorySearchRequest/Result<br/>MemoryStats · MemoryFilter"]
  score["frecency.go<br/>CalculateFrecency"]
  support["id.go · errors.go<br/>newID · now · Err*"]
  constants["pkg/constants"]
  tests["测试<br/>entity · errors · fact · frecency · scope"]
  fact --> support
  entity --> support
  fact --> scope
  entity --> scope
  general --> scope
  score --> constants
  tests -.验证实体不变量、状态迁移和评分.-> fact
```

## 说明

`MemoryFact` 通过显式状态迁移维护 active/superseded/archived 生命周期，`MemoryEntity` 管理事实计数与画像重建条件。`ScopeFilter` 描述 tenant/user/agent 的可见范围，`CalculateFrecency` 为召回排序提供领域评分。
