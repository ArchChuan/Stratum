# 模型管理可编辑参数 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 模型管理成为模型权威数据唯一编辑入口——采样默认注入 + max_tokens 硬 clamp + 上下文窗口预检 + 采样越界拦截 + 能力不匹配拦截 + extra_headers，网关单一执行点（Gateway.invoke per-link），CD 部署成功。

**Architecture:** 038 迁移加 4 列（models.sampling_params/max_temperature、providers.extra_headers/default_sampling）→ domain 实体 + 写时校验纯函数 → repo SQL 同步 + 审计同事务（SET LOCAL search_path）→ registry policy 预计算（吸收 ResolveReasoning N+1）→ gateway `enforceModelPolicy`（clamp → 注入 → 预检 → 校验，per-link，纯函数返回副本）→ agent 账本链插 DB 权威 → 前端高级折叠区 → contract golden/e2e/测试迁移。

**Tech Stack:** Go 1.25.12、Gin v1.9.1、pgx v5.9.2（pgxmock）、Zap、React 18.3 + AntD 5.20、Vite 6.4。

## Global Constraints

- 采纳原则：有请求失败风险的参数用模型权威数据硬拦截兜底（fail-closed 最高优先级）；无失败风险参数默认值注入（低于显式配置）。
- 执行顺序：clamp → 注入 → 预检 → 校验；注入值必须回灌硬拦截层。
- 拦截错误 = permanent（不重试不降级）+ 语义化 4xx；L2 错误必须实现 `ContextLengthExceeded() bool`（agent D4 最小请求重试闭环）。
- 无 policy（模型记录不存在）= 权威数据不存在 → L1-L3 跳过 + WARN + `llmgateway.policy_missing` 指标。
- max_tokens=0 兜底注入仅限 openai_compat/anthropic 协议族；ollama 0=infinite 原生语义不注入。
- extra_headers 写时 `textproto.CanonicalMIMEHeaderKey(strings.TrimSpace(k))` 归一化 + 固定黑名单（Authorization/Content-Type/User-Agent/X-API-Key/api-key/Host/Cookie/Proxy-Authorization/Referer/Transfer-Encoding/Content-Length/Trailer/Accept-Encoding/X-Forwarded-For/Forwarded）；控制字符拒收；write-only（`json:"-"`）不回显。
- 合并顺序：extra_headers 先应用 → 客户端硬编码鉴权头最后覆盖。
- 空值语义：空 map/空 struct = 保留既有值，显式 null = 清空（指针字段区分）。
- 审计：Update 写路径同事务写 `ResourceChangeAuditEvent`（Before/After 脱敏，extra_headers 值/api_key 不进投影）。
- 写时校验：max_temperature ∈ [0,1] 或 NULL；temperature ≤ max_temperature；max_temperature=0 禁 temperature；sampling 键与 L3 同一套边界。
- 账本链（Goal 3 唯一例外）：显式 > DB 模型 max_tokens > vendor 表 maxOut > 4096。
- Go 门禁：圈复杂度 ≤10、认知 ≤15、行数 ≤120、嵌套 ≤4；错误逐层 wrap；日志只用 Zap，不打印 extra_headers 值/API key。
- 禁止 main 分支直推；E2E 用无头浏览器；远端写入需许可（/goal 已授权部署推进）。

---

### Task 1: 038 迁移 + 内容断言

**Files:**

- Create: `pkg/migration/sql/038_model_editable_params.up.sql`
- Create: `pkg/migration/sql/038_model_editable_params.down.sql`
- Modify: `pkg/migration/migration_test.go`（追加测试函数）

**Interfaces:**

- Produces: 4 列 DDL（public schema，`IF NOT EXISTS`）；`TestModelEditableParamsMigration` 内容断言（仿 `TestRemoveTenantLLMAPIKeysMigration`：os.ReadFile + strings.Contains）。

- [ ] **Step 1: 写失败测试（038 内容断言）**

在 `pkg/migration/migration_test.go` 追加（先读该文件确认包名/既有 helper 风格，仿 `TestRemoveTenantLLMAPIKeysMigration`）：

```go
func TestModelEditableParamsMigration(t *testing.T) {
 for _, tc := range []struct {
  file    string
  queries []string
 }{
  {
   file: "038_model_editable_params.up.sql",
   queries: []string{
    "ALTER TABLE public.models ADD COLUMN IF NOT EXISTS",
    "sampling_params",
    "max_temperature",
    "ALTER TABLE public.providers ADD COLUMN IF NOT EXISTS",
    "extra_headers",
    "default_sampling",
   },
  },
  {
   file:    "038_model_editable_params.down.sql",
   queries: []string{"ALTER TABLE public.models DROP COLUMN IF EXISTS", "ALTER TABLE public.providers DROP COLUMN IF EXISTS"},
  },
 } {
  t.Run(tc.file, func(t *testing.T) {
   content, err := os.ReadFile(filepath.Join("sql", tc.file))
   require.NoError(t, err)
   for _, q := range tc.queries {
    require.Contains(t, string(content), q, "missing %q in %s", q, tc.file)
   }
  })
 }
}
```

- [ ] **Step 2: 运行验证失败**

Run: `go test ./pkg/migration/ -run TestModelEditableParamsMigration -v`
Expected: FAIL（sql 文件不存在，ReadFile 报错）

- [ ] **Step 3: 最小实现（迁移文件）**

`pkg/migration/sql/038_model_editable_params.up.sql`：

```sql
-- 模型管理可编辑参数：采样默认值、采样上限、provider 级追加请求头与默认采样。
-- public schema 权威数据；运行时由 Gateway.enforceModelPolicy 消费。

ALTER TABLE public.models ADD COLUMN IF NOT EXISTS
    sampling_params  JSONB NOT NULL DEFAULT '{}';
ALTER TABLE public.models ADD COLUMN IF NOT EXISTS
    max_temperature  DOUBLE PRECISION;

ALTER TABLE public.providers ADD COLUMN IF NOT EXISTS
    extra_headers    JSONB NOT NULL DEFAULT '{}';
ALTER TABLE public.providers ADD COLUMN IF NOT EXISTS
    default_sampling JSONB NOT NULL DEFAULT '{}';
```

`pkg/migration/sql/038_model_editable_params.down.sql`：

```sql
ALTER TABLE public.models DROP COLUMN IF EXISTS sampling_params;
ALTER TABLE public.models DROP COLUMN IF EXISTS max_temperature;
ALTER TABLE public.providers DROP COLUMN IF EXISTS extra_headers;
ALTER TABLE public.providers DROP COLUMN IF EXISTS default_sampling;
```

- [ ] **Step 4: 运行验证通过**

Run: `go test ./pkg/migration/ -run TestModelEditableParamsMigration -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add pkg/migration/sql/038_model_editable_params.up.sql pkg/migration/sql/038_model_editable_params.down.sql pkg/migration/migration_test.go
git commit -m "feat(llmgateway): 038 migration for model editable params"
```

---

### Task 2: domain 实体扩展 + 写时校验纯函数

**Files:**

- Modify: `internal/llmgateway/domain/model.go`（Model 加 SamplingParams/MaxTemperature；校验纯函数）
- Modify: `internal/llmgateway/domain/provider.go`（Provider 加 ExtraHeaders/DefaultSampling）
- Create: `internal/llmgateway/domain/headers.go`（黑名单 + 归一化校验）
- Create: `internal/llmgateway/domain/model_validation_test.go`
- Create: `internal/llmgateway/domain/headers_test.go`

**Interfaces:**

- Consumes: 无（stdlib textproto）
- Produces:
  - `type SamplingParams struct { Temperature *float64`json:"temperature,omitempty"`; TopP *float64`json:"top_p,omitempty"`; FrequencyPenalty *float64`json:"frequency_penalty,omitempty"`; PresencePenalty *float64`json:"presence_penalty,omitempty"`; Seed *int64`json:"seed,omitempty"`}`
  - `Model.SamplingParams *SamplingParams`、`Model.MaxTemperature *float64`（db 列 sampling_params/max_temperature）
  - `Provider.ExtraHeaders map[string]string`json:"-"``、`Provider.DefaultSampling map[string]any `json:"-"``
  - `func ValidateSamplingWrite(p *SamplingParams, maxTemp *float64) error`（范围 + 跨字段）
  - `func ValidateMaxTemperature(v *float64) error`
  - `func ValidateExtraHeaders(h map[string]string) error`（黑名单 + 控制字符）
  - `func CanonicalizeHeaderKey(k string) string`

- [ ] **Step 1: 写失败测试**

`internal/llmgateway/domain/model_validation_test.go`：

```go
package domain

import (
 "testing"

 "github.com/stretchr/testify/require"
)

func floatPtr(v float64) *float64 { return &v }
func intPtr(v int64) *int64       { return &v }

func TestValidateMaxTemperature(t *testing.T) {
 cases := []struct {
  name string
  v    *float64
  want string // 期望错误子串；空=通过
 }{
  {name: "nil allowed", v: nil, want: ""},
  {name: "zero allowed", v: floatPtr(0), want: ""},
  {name: "one allowed", v: floatPtr(1), want: ""},
  {name: "mid range", v: floatPtr(0.7), want: ""},
  {name: "negative rejected", v: floatPtr(-0.1), want: "out of range"},
  {name: "above one rejected", v: floatPtr(1.5), want: "out of range"},
 }
 for _, tc := range cases {
  t.Run(tc.name, func(t *testing.T) {
   err := ValidateMaxTemperature(tc.v)
   if tc.want == "" {
    require.NoError(t, err)
    return
   }
   require.ErrorContains(t, err, tc.want)
  })
 }
}

func TestValidateSamplingWrite(t *testing.T) {
 cases := []struct {
  name    string
  p       *SamplingParams
  maxTemp *float64
  want    string
 }{
  {name: "nil params ok", p: nil, maxTemp: nil, want: ""},
  {name: "temperature in range", p: &SamplingParams{Temperature: floatPtr(0.7)}, maxTemp: nil, want: ""},
  {name: "temperature too high", p: &SamplingParams{Temperature: floatPtr(1.5)}, maxTemp: nil, want: "temperature"},
  {name: "top_p too high", p: &SamplingParams{TopP: floatPtr(1.1)}, maxTemp: nil, want: "top_p"},
  {name: "frequency penalty out of range", p: &SamplingParams{FrequencyPenalty: floatPtr(2.5)}, maxTemp: nil, want: "frequency_penalty"},
  {name: "presence penalty out of range", p: &SamplingParams{PresencePenalty: floatPtr(-2.5)}, maxTemp: nil, want: "presence_penalty"},
  {name: "temperature exceeds max_temperature", p: &SamplingParams{Temperature: floatPtr(0.8)}, maxTemp: floatPtr(0.5), want: "max_temperature"},
  {name: "temperature equals max_temperature ok", p: &SamplingParams{Temperature: floatPtr(0.5)}, maxTemp: floatPtr(0.5), want: ""},
  {name: "max_temperature zero forbids temperature", p: &SamplingParams{Temperature: floatPtr(0.1)}, maxTemp: floatPtr(0), want: "not support"},
  {name: "max_temperature zero without temperature ok", p: &SamplingParams{TopP: floatPtr(0.9)}, maxTemp: floatPtr(0), want: ""},
  {name: "seed negative ok", p: &SamplingParams{Seed: intPtr(-1)}, maxTemp: nil, want: ""},
 }
 for _, tc := range cases {
  t.Run(tc.name, func(t *testing.T) {
   err := ValidateSamplingWrite(tc.p, tc.maxTemp)
   if tc.want == "" {
    require.NoError(t, err)
    return
   }
   require.ErrorContains(t, err, tc.want)
  })
 }
}
```

`internal/llmgateway/domain/headers_test.go`：

```go
package domain

import (
 "strings"
 "testing"

 "github.com/stretchr/testify/require"
)

func TestCanonicalizeHeaderKey(t *testing.T) {
 cases := []struct{ in, want string }{
  {"x-api-key", "X-Api-Key"},
  {"  Authorization  ", "Authorization"},
  {"content-type", "Content-Type"},
  {"x-forwarded-for", "X-Forwarded-For"},
 }
 for _, tc := range cases {
  require.Equal(t, tc.want, CanonicalizeHeaderKey(tc.in), "input %q", tc.in)
 }
}

func TestValidateExtraHeaders(t *testing.T) {
 cases := []struct {
  name    string
  headers map[string]string
  want    string
 }{
  {name: "nil ok", headers: nil, want: ""},
  {name: "empty ok", headers: map[string]string{}, want: ""},
  {name: "custom header ok", headers: map[string]string{"X-Tenant": "a"}, want: ""},
  {name: "authorization blocked", headers: map[string]string{"Authorization": "Bearer x"}, want: "Authorization"},
  {name: "authorization case variant blocked", headers: map[string]string{"authorization": "x"}, want: "Authorization"},
  {name: "content-type blocked", headers: map[string]string{"Content-Type": "x"}, want: "Content-Type"},
  {name: "x-api-key blocked", headers: map[string]string{"x-api-key": "x"}, want: "X-Api-Key"},
  {name: "host blocked", headers: map[string]string{"Host": "x"}, want: "Host"},
  {name: "cookie blocked", headers: map[string]string{"Cookie": "x"}, want: "Cookie"},
  {name: "x-forwarded-for blocked", headers: map[string]string{"x-forwarded-for": "1.2.3.4"}, want: "X-Forwarded-For"},
  {name: "trailing space variant blocked", headers: map[string]string{"Content-Type ": "x"}, want: "Content-Type"},
  {name: "crlf in value rejected", headers: map[string]string{"X-Custom": "a\r\nX-Evil: b"}, want: "control"},
  {name: "empty key rejected", headers: map[string]string{"": "x"}, want: "empty"},
 }
 for _, tc := range cases {
  t.Run(tc.name, func(t *testing.T) {
   err := ValidateExtraHeaders(tc.headers)
   if tc.want == "" {
    require.NoError(t, err)
    return
   }
   require.ErrorContains(t, err, tc.want)
  })
 }
}
```

- [ ] **Step 2: 运行验证失败**

Run: `go test ./internal/llmgateway/domain/ -run 'TestValidate|TestCanonicalize' -v`
Expected: FAIL（编译错误：未定义）

- [ ] **Step 3: 最小实现**

`internal/llmgateway/domain/model.go` 追加（读文件确认 Model 结构位置后插入字段）：

```go
// SamplingParams 是模型级默认采样参数；nil 表示未配置（回退 provider 层）。
// 0=unset 语义与 agent 侧一致：请求未显式设置时注入。
type SamplingParams struct {
 Temperature      *float64 `json:"temperature,omitempty"`
 TopP             *float64 `json:"top_p,omitempty"`
 FrequencyPenalty *float64 `json:"frequency_penalty,omitempty"`
 PresencePenalty  *float64 `json:"presence_penalty,omitempty"`
 Seed             *int64   `json:"seed,omitempty"`
}

// MaxTemperature 是采样上限（NULL=全局契约 [0,1]；0=不支持 temperature）。
// 见 ValidateMaxTemperature 与 ValidateSamplingWrite。
```

Model 结构加字段（与 ContextWindow/MaxTokens 同级）：

```go
 SamplingParams *SamplingParams `json:"samplingParams,omitempty" db:"sampling_params"`
 MaxTemperature *float64        `json:"maxTemperature,omitempty" db:"max_temperature"`
```

`internal/llmgateway/domain/model.go` 文件尾部追加校验纯函数：

```go
// samplingRange 校验采样键值域；与网关 L3 拦截共用同一套边界。
func validateInUnitRange(name string, v *float64) error {
 if v == nil {
  return nil
 }
 if *v < 0 || *v > 1 {
  return fmt.Errorf("%s %v out of range [0,1]", name, *v)
 }
 return nil
}

// ValidateMaxTemperature 校验采样上限：nil 或 [0,1]。
func ValidateMaxTemperature(v *float64) error {
 if v == nil {
  return nil
 }
 if *v < 0 || *v > 1 {
  return fmt.Errorf("max_temperature %v out of range [0,1]", *v)
 }
 return nil
}

// ValidateSamplingWrite 校验模型级采样参数写入门禁（防注入值被运行时 L3 拒绝）：
//   - temperature/top_p/frequency_penalty/presence_penalty ∈ [0,1]；
//   - temperature ≤ max_temperature（max_temperature>0 时）；
//   - max_temperature=0 时禁止 temperature（不支持）。
func ValidateSamplingWrite(p *SamplingParams, maxTemp *float64) error {
 if p == nil {
  return ValidateMaxTemperature(maxTemp)
 }
 if err := validateInUnitRange("temperature", p.Temperature); err != nil {
  return err
 }
 if err := validateInUnitRange("top_p", p.TopP); err != nil {
  return err
 }
 if err := validateInUnitRange("frequency_penalty", p.FrequencyPenalty); err != nil {
  return err
 }
 if err := validateInUnitRange("presence_penalty", p.PresencePenalty); err != nil {
  return err
 }
 if err := ValidateMaxTemperature(maxTemp); err != nil {
  return err
 }
 if maxTemp != nil {
  if *maxTemp == 0 && p.Temperature != nil {
   return fmt.Errorf("temperature not supported (max_temperature=0)")
  }
  if *maxTemp > 0 && p.Temperature != nil && *p.Temperature > *maxTemp {
   return fmt.Errorf("temperature %v exceeds max_temperature %v", *p.Temperature, *maxTemp)
  }
 }
 return nil
}
```

`internal/llmgateway/domain/provider.go` 的 Provider 结构加字段（APIKey 旁，json:"-"）：

```go
 // ExtraHeaders 是 provider 级追加请求头（write-only，永不回显）。
 ExtraHeaders map[string]string `json:"-"`
 // DefaultSampling 是 provider 级默认采样参数（write-only，永不回显）。
 DefaultSampling map[string]any `json:"-"`
```

`internal/llmgateway/domain/headers.go`：

```go
package domain

import (
 "fmt"
 "net/textproto"
 "strings"
)

// blockedHeaderKeys 是 extra_headers 写时固定黑名单：鉴权/传输/客户端 IP 伪造
// 头一律拒收。比较前必须经 CanonicalizeHeaderKey 归一化（大小写变体/尾空格
// 穿透是真实覆盖风险）。
var blockedHeaderKeys = map[string]struct{}{
 "Authorization":      {},
 "Content-Type":       {},
 "User-Agent":         {},
 "X-Api-Key":          {},
 "Host":               {},
 "Cookie":             {},
 "Proxy-Authorization": {},
 "Referer":            {},
 "Transfer-Encoding":  {},
 "Content-Length":     {},
 "Trailer":            {},
 "Accept-Encoding":    {},
 "X-Forwarded-For":    {},
 "Forwarded":          {},
}

// CanonicalizeHeaderKey 归一化头名：TrimSpace + MIME 规范形式
// （x-api-key → X-Api-Key，authorization → Authorization）。
func CanonicalizeHeaderKey(k string) string {
 return textproto.CanonicalMIMEHeaderKey(strings.TrimSpace(k))
}

// ValidateExtraHeaders 校验 provider extra_headers 写入门禁：
// 空 key/黑名单头（含大小写变体与尾空格）/值含控制字符一律拒绝。
func ValidateExtraHeaders(h map[string]string) error {
 for k, v := range h {
  canonical := CanonicalizeHeaderKey(k)
  if canonical == "" {
   return fmt.Errorf("extra_headers: empty header key")
  }
  if _, blocked := blockedHeaderKeys[canonical]; blocked {
   return fmt.Errorf("extra_headers: header %q is blocked", canonical)
  }
  if strings.IndexFunc(v, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
   return fmt.Errorf("extra_headers: header %q value contains control characters", canonical)
  }
 }
 return nil
}
```

检查 model.go 头部 import 是否含 `fmt`（无则加）。

- [ ] **Step 4: 运行验证通过**

Run: `go test ./internal/llmgateway/domain/ -v`
Expected: PASS（含既有测试）

- [ ] **Step 5: Commit**

```bash
git add internal/llmgateway/domain/
git commit -m "feat(llmgateway): domain entities and write-time validation for editable params"
```

---

### Task 3: port + repo SQL 同步 + 审计同事务

**Files:**

- Modify: `internal/llmgateway/domain/port/model_repo.go`（Update 签名）
- Modify: `internal/llmgateway/domain/port/provider_repo.go`（Update 签名）
- Modify: `internal/llmgateway/infrastructure/model_repo.go`（SELECT 17 列、Update 扩展、审计事务）
- Modify: `internal/llmgateway/infrastructure/provider_repo.go`（SELECT 11 列、Update 扩展、审计事务）
- Modify: `internal/llmgateway/infrastructure/model_repo_internal_test.go`、`provider_repo_internal_test.go`（pgxmock 同步）
- Modify: `api/http/contract_test.go`（contractModelRepo/contractProviderRepo Update stub 签名同步）

**Interfaces:**

- Consumes: `domain.SamplingParams`、`domain.ValidateExtraHeaders`（Task 2）；`auditdomain.ResourceChangeAuditEvent`、`auditdomain.ChangeAuditInsertSQL`、`auditdomain.ChangeOpUpdate`
- Produces: port 签名 `Update(ctx context.Context, m *domain.Model, tenantID string, audit *auditdomain.ResourceChangeAuditEvent) error`（provider 同形）；repo 内 Begin → `SET LOCAL search_path = "tenant_"+tenantID, public` → UPDATE（全限定 public.*）→ INSERT resource_change_audits（非全限定，走 search_path）→ Commit；出错 Rollback。

- [ ] **Step 1: 先读现文件**

Read `internal/llmgateway/infrastructure/model_repo.go`（15 列 SELECT/12 列 INSERT/7 列 UPDATE）与 `provider_repo.go`（GetMeta 不解密、Update CASE WHEN $4=''）——按下列改动逐行替换。

- [ ] **Step 2: 写失败测试（pgxmock 同步 + 审计事务）**

`model_repo_internal_test.go`：`modelColumns` 加 2 列 `sampling_params`、`max_temperature`（17 列），`modelRow` 加对应值（`[]byte("{}")`、`nil`）；`anyArgs(12)` → `anyArgs(14)`（Create 14 列）。Update 测试改为事务断言：

```go
func TestModelRepo_Update_withAudit(t *testing.T) {
 mock, repo := newFactMock() // 读现有 helper，复用
 model := &domain.Model{ID: "m1", Name: "qwen-turbo", Enabled: true, ContextWindow: 131072,
  MaxTokens: 8192, SamplingParams: &domain.SamplingParams{Temperature: floatPtr(0.7)}, MaxTemperature: floatPtr(0.9)}
 audit := &auditdomain.ResourceChangeAuditEvent{
  ResourceKind: auditdomain.ResourceKindModel, ResourceID: "m1",
  Operation: auditdomain.ChangeOpUpdate, ActorID: "u1",
  Before: json.RawMessage(`{"name":"qwen-turbo"}`), After: json.RawMessage(`{"name":"qwen-turbo","max_tokens":8192}`),
 }
 mock.ExpectBegin()
 mock.ExpectExec(`SET LOCAL search_path`).WillReturnResult(pgxmock.NewResult("SET", 0))
 mock.ExpectExec(`UPDATE public.models`).WithArgs(anyArgs(9)...).WillReturnResult(pgxmock.NewResult("UPDATE", 1))
 mock.ExpectExec(`INSERT INTO resource_change_audits`).WithArgs(anyArgs(11)...).WillReturnResult(pgxmock.NewResult("INSERT", 1))
 mock.ExpectCommit()
 err := repo.Update(context.Background(), model, "tenant-1", audit)
 require.NoError(t, err)
 mock.ExpectationsWereMet()
}
```

（floatPtr helper 放测试文件内；若既有测试无 `anyArgs` 则读现有风格复制。）

- [ ] **Step 3: 运行验证失败**

Run: `go test ./internal/llmgateway/infrastructure/ -run 'TestModelRepo|TestProviderRepo' -v`
Expected: FAIL（签名不匹配编译失败）

- [ ] **Step 4: 最小实现**

port 两处（model_repo.go / provider_repo.go）：

```go
 // Update 更新模型记录并在同一事务内写入资源变更审计
 // （resource_change_audits 位于租户 schema，事务内 SET LOCAL search_path 切换）。
 Update(ctx context.Context, m *domain.Model, tenantID string, audit *auditdomain.ResourceChangeAuditEvent) error
```

`model_repo.go`：

- `modelColumns`（15 列）追加 `sampling_params`、`max_temperature` → 17 列；scan 目标加 `m.SamplingParams`（`*domain.SamplingParams`，pgx 可解 JSONB 到 struct 指针——传 `&m.SamplingParams` 用 `[]byte` 再 json.Unmarshal 更稳，读现有 scan 风格，若现有列用 `string` 则新增两列同样用 `[]byte` 解包：`case "sampling_params": json.Unmarshal(...)`；若 scan 直接绑 struct 字段则保持一致）。
- Create INSERT 12 列加 2 列（sampling_params/max_temperature 参数化传 `nil` 或 `{}`）。
- Update 重写：

```go
func (r *PgModelRepository) Update(ctx context.Context, m *domain.Model, tenantID string, audit *auditdomain.ResourceChangeAuditEvent) error {
 tx, err := r.pool.Begin(ctx)
 if err != nil {
  return fmt.Errorf("model repo update: begin: %w", err)
 }
 defer func() { _ = tx.Rollback(ctx) }()
 if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path = %q, public", "tenant_"+tenantID)); err != nil {
  return fmt.Errorf("model repo update: set search_path: %w", err)
 }
 sampling, err := json.Marshal(m.SamplingParams)
 if err != nil {
  return fmt.Errorf("model repo update: marshal sampling_params: %w", err)
 }
 if _, err := tx.Exec(ctx, `UPDATE public.models SET
   display_name=$1, capabilities=$2, context_window=$3, max_tokens=$4,
   input_price=$5, output_price=$6, recommended=$7, enabled=$8,
   sampling_params=$9, max_temperature=$10
  WHERE id=$11`,
  m.DisplayName, m.Capabilities, m.ContextWindow, m.MaxTokens,
  m.InputPrice, m.OutputPrice, m.Recommended, m.Enabled,
  string(sampling), m.MaxTemperature, m.ID); err != nil {
  return fmt.Errorf("model repo update: %w", err)
 }
 if err := r.insertAudit(ctx, tx, tenantID, audit); err != nil {
  return fmt.Errorf("model repo update: audit: %w", err)
 }
 if err := tx.Commit(ctx); err != nil {
  return fmt.Errorf("model repo update: commit: %w", err)
 }
 return nil
}

// insertAudit 在业务事务内写资源变更审计（表在租户 schema，依赖当前事务 search_path）。
func (r *PgModelRepository) insertAudit(ctx context.Context, tx pgx.Tx, tenantID string, ev *auditdomain.ResourceChangeAuditEvent) error {
 if ev == nil {
  return nil
 }
 ev = ev.Normalized()
 _, err := tx.Exec(ctx, auditdomain.ChangeAuditInsertSQL,
  ev.ResourceID+"-"+ev.Operation+"-"+tenantID, tenantID, ev.ResourceKind, ev.ResourceID,
  ev.Operation, ev.ActorID, ev.ActorType, ev.Source, ev.ProposalID,
  ev.Before, ev.After)
 return err
}
```

（`id` 列若既有审计行 id 是 UUID 字符串则保持既有生成方式；`SET LOCAL` 用 `fmt.Sprintf("%q", ...)` 转义租户名——租户 ID 由系统生成，无注入面；事务结束 SET LOCAL 自动失效，无连接残留。）

`provider_repo.go`：

- 9 列 SELECT 加 `extra_headers`、`default_sampling` → 11 列；GetMeta 同步加列（不解密）。
- Create 加 2 列；Update 重写为同形事务 + 审计（UPDATE public.providers SET ... extra_headers=$N, default_sampling=$M + WHERE id=$K；extra_headers 存 `json.Marshal(p.ExtraHeaders)`，default_sampling 存 `json.Marshal(p.DefaultSampling)`；审计同 insertAudit——provider_repo 内写同名私有方法或抽公共 helper 至基础设施包内新文件 `audit_tx.go`（推荐，两 repo 共用）：

`internal/llmgateway/infrastructure/audit_tx.go`：

```go
package infrastructure

import (
 "context"
 "fmt"

 "github.com/jackc/pgx/v5"

 auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
)

// beginTenantTx 开启事务并切换租户 schema（resource_change_audits 位于
// tenant_<id>）；SET LOCAL 随事务结束自动失效，无连接残留。
func beginTenantTx(ctx context.Context, pool pgxBeginner, tenantID string) (pgx.Tx, error) {
 tx, err := pool.Begin(ctx)
 if err != nil {
  return nil, fmt.Errorf("begin: %w", err)
 }
 if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path = %q, public", "tenant_"+tenantID)); err != nil {
  _ = tx.Rollback(ctx)
  return nil, fmt.Errorf("set search_path: %w", err)
 }
 return tx, nil
}

// insertAuditTx 在业务事务内写资源变更审计；nil 事件跳过（无审计的调用点不强制）。
func insertAuditTx(ctx context.Context, tx pgx.Tx, tenantID string, ev *auditdomain.ResourceChangeAuditEvent) error {
 if ev == nil {
  return nil
 }
 ev = ev.Normalized()
 _, err := tx.Exec(ctx, auditdomain.ChangeAuditInsertSQL,
  ev.ResourceID+"-"+ev.Operation+"-"+tenantID, tenantID, ev.ResourceKind, ev.ResourceID,
  ev.Operation, ev.ActorID, ev.ActorType, ev.Source, ev.ProposalID,
  ev.Before, ev.After)
 return err
}

// pgxBeginner 抽象 pool 与 tx 的 Begin（测试可 mock）。
type pgxBeginner interface {
 Begin(ctx context.Context) (pgx.Tx, error)
}
```

`contract_test.go`：contractModelRepo.Update / contractProviderRepo.Update stub 改签名并返回 `errStubNotFound` 或成功（读现有 stub 后同步）。

- [ ] **Step 5: 运行验证通过**

Run: `go test ./internal/llmgateway/... ./api/http/ -run 'TestModelRepo|TestProviderRepo|TestContract|TestAdmin' -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/llmgateway/ api/http/contract_test.go
git commit -m "feat(llmgateway): repo SQL columns and audit-in-transaction for editable params"
```

---

### Task 4: application 写时校验 + 审计事件构造

**Files:**

- Modify: `internal/llmgateway/application/model_mgmt_service.go`
- Modify: `internal/llmgateway/application/provider_service.go`
- Modify: 对应 service 测试（`model_mgmt_service_test.go`/`provider_service_test.go`，若存在）

**Interfaces:**

- Consumes: `ValidateSamplingWrite`、`ValidateMaxTemperature`、`ValidateExtraHeaders`（Task 2）；port `Update(ctx, m, tenantID, audit)`（Task 3）
- Produces: `UpdateModelInput.SamplingParams *domain.SamplingParams`、`UpdateModelInput.MaxTemperature *float64`；`UpdateProviderInput.ExtraHeaders map[string]string`、`UpdateProviderInput.DefaultSampling map[string]any`；审计事件 Before/After 脱敏投影（`auditProjection(m)` / `auditProjection(p)` 私有函数，不含 extra_headers 值/api_key）。

- [ ] **Step 1: 写失败测试（先读现有测试文件风格）**

`model_mgmt_service_test.go` 追加：

```go
func TestModelMgmtService_Update_rejectsSamplingViolations(t *testing.T) {
 svc, _ := newModelMgmtServiceFixture(t) // 读现有 fixture helper
 over := 1.5
 _, err := svc.Update(context.Background(), "tenant-1", UpdateModelInput{
  ID: "m1", SamplingParams: &domain.SamplingParams{Temperature: &over},
 })
 require.ErrorContains(t, err, "out of range")
}

func TestModelMgmtService_Update_writesAuditWithSanitizedProjection(t *testing.T) {
 repo := &stubModelRepo{} // 读现有 stub，扩展断言捕获 audit 参数
 svc := NewModelMgmtService(repo, nil)
 _, err := svc.Update(context.Background(), "tenant-1", UpdateModelInput{
  ID: "m1", DisplayName: "qwen", MaxTokens: 8192,
 })
 require.NoError(t, err)
 require.NotNil(t, repo.lastAudit)
 require.Equal(t, auditdomain.ResourceKindModel, repo.lastAudit.ResourceKind)
 require.Equal(t, auditdomain.ChangeOpUpdate, repo.lastAudit.Operation)
}
```

`provider_service_test.go` 追加（headers 黑名单 + 审计投影不含 extra_headers）：

```go
func TestProviderService_Update_rejectsBlockedHeader(t *testing.T) {
 svc, _ := newProviderServiceFixture(t)
 _, err := svc.Update(context.Background(), "tenant-1", UpdateProviderInput{
  ID: "p1", ExtraHeaders: map[string]string{"authorization": "Bearer x"},
 })
 require.ErrorContains(t, err, "blocked")
}

func TestProviderService_Update_auditProjectionHidesHeaderValues(t *testing.T) {
 repo := &stubProviderRepo{meta: &domain.Provider{ID: "p1", Name: "p1"}}
 svc := NewProviderService(repo, nil, nil, nil)
 _, err := svc.Update(context.Background(), "tenant-1", UpdateProviderInput{
  ID: "p1", ExtraHeaders: map[string]string{"X-Tenant": "secret"},
 })
 require.NoError(t, err)
 require.NotContains(t, string(repo.lastAudit.After), "secret")
}
```

- [ ] **Step 2: 运行验证失败**

Run: `go test ./internal/llmgateway/application/ -run 'TestModelMgmtService_Update|TestProviderService_Update' -v`
Expected: FAIL（编译/断言失败）

- [ ] **Step 3: 最小实现**

`model_mgmt_service.go` Update 改造（读现文件 57-74 后替换）：

```go
func (s *ModelMgmtService) Update(ctx context.Context, tenantID string, input UpdateModelInput) (*domain.Model, error) {
 existing, err := s.repo.Get(ctx, input.ID)
 if err != nil {
  return nil, fmt.Errorf("model management: get model: %w", err)
 }
 if err := validateModelSamplingWrite(input); err != nil {
  return nil, err
 }
 updated := *existing
 if input.DisplayName != nil { updated.DisplayName = *input.DisplayName }
 // ... 既有逐字段拷贝保持 ...
 if input.SamplingParams != nil {
  updated.SamplingParams = input.SamplingParams
 }
 if input.MaxTemperature != nil {
  updated.MaxTemperature = input.MaxTemperature
 }
 before, _ := json.Marshal(auditProjection(existing))
 after, _ := json.Marshal(auditProjection(&updated))
 event := &auditdomain.ResourceChangeAuditEvent{
  ResourceKind: auditdomain.ResourceKindModel,
  ResourceID:   input.ID,
  Operation:    auditdomain.ChangeOpUpdate,
  Before:       before,
  After:        after,
 }
 if err := s.repo.Update(ctx, &updated, tenantID, event); err != nil {
  return nil, fmt.Errorf("model management: update model: %w", err)
 }
 s.invalidate()
 return &updated, nil
}
```

（`UpdateModelInput` 现有字段用值类型——读现文件确认是值类型还是指针，保持既有风格；`DisplayName: "x"` 空值=保留语义按现文件实现。`SamplingParams *domain.SamplingParams` nil=保留；`MaxTemperature *float64` nil=保留。）

`model_mgmt_service.go` 追加：

```go
// validateModelSamplingWrite 把 domain 校验规则接到写入口（幂等，repo 保持纯 IO）。
func validateModelSamplingWrite(input UpdateModelInput) error {
 if err := domain.ValidateSamplingWrite(input.SamplingParams, input.MaxTemperature); err != nil {
  return fmt.Errorf("model management: sampling_params: %w", err)
 }
 return nil
}

// auditProjection 构造审计投影（脱敏：不含凭据；数值字段全量）。
func auditProjection(m *domain.Model) map[string]any {
 return map[string]any{
  "name":            m.Name,
  "display_name":    m.DisplayName,
  "capabilities":    m.Capabilities,
  "context_window":  m.ContextWindow,
  "max_tokens":      m.MaxTokens,
  "input_price":     m.InputPrice,
  "output_price":    m.OutputPrice,
  "recommended":     m.Recommended,
  "enabled":         m.Enabled,
  "provider_managed": m.ProviderManaged,
  "sampling_params": m.SamplingParams,
  "max_temperature": m.MaxTemperature,
 }
}
```

`provider_service.go` Update 改造（读现文件 99-116 后替换）：`ExtraHeaders`/`DefaultSampling` 拷贝 + 校验：

```go
 if input.ExtraHeaders != nil {
  if err := domain.ValidateExtraHeaders(input.ExtraHeaders); err != nil {
   return nil, fmt.Errorf("provider management: extra_headers: %w", err)
  }
  updated.ExtraHeaders = input.ExtraHeaders
 }
 if input.DefaultSampling != nil {
  updated.DefaultSampling = input.DefaultSampling
 }
```

（既有 `apiKey == ""` 保留逻辑不动；audit 构造同 model 侧，投影用私有函数 `providerAuditProjection(p)`——只含 name/kind/base_url/default_model/extra_headers 键名列表（不含值）或直接省略 extra_headers 键。**投影绝不含 extra_headers 值、api_key。** 用键名列表可审计性更好：`"extra_headers_keys": keys(p.ExtraHeaders)`。）

- [ ] **Step 4: 运行验证通过**

Run: `go test ./internal/llmgateway/application/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/llmgateway/application/
git commit -m "feat(llmgateway): write-time validation and audit events on model/provider update"
```

---

### Task 5: registry policy 预计算（吸收 ResolveReasoning N+1）

**Files:**

- Modify: `internal/llmgateway/infrastructure/model_registry.go`
- Modify: `internal/llmgateway/infrastructure/gateway.go`（chainLink.Policy 字段）
- Modify: `internal/llmgateway/infrastructure/model_registry_test.go`（如有）

**Interfaces:**

- Consumes: `domain.Model` 新字段（Task 2）
- Produces:
  - `type ModelPolicy struct { MaxTokens int; ContextWindow int; MaxTemperature *float64; SamplingDefaults *domain.SamplingParams; Capabilities []domain.ModelCapability; Reasoning bool }`（infrastructure 包）
  - `resolvedEntry.policy *ModelPolicy`
  - `func policyFromModel(m *domain.Model) *ModelPolicy`（nil 当 m==nil）
  - `chainLink.Policy *ModelPolicy`（gateway.go）
  - `ModelRegistry.ResolveReasoning(ctx, model)` 改造：先查 cache `policy.Reasoning`，miss 回退旧 catalog 逻辑
  - cacheSet 各解析级按模型名携带 policy（resolveExact/ResolveFallbackCandidates 有模型记录；②③④ 级用已解析出的模型记录或 nil）

- [ ] **Step 1: 先读现文件**

Read `internal/llmgateway/infrastructure/model_registry.go`（cacheSet 5 调用点 191/222/270/292/372、Warm 566/568、resolveModel 5 级、ResolveReasoning 638-657、ResolveFallbackCandidates 348-380）——按下列改动逐点替换。

- [ ] **Step 2: 写失败测试（policy 预计算 + 吸收 N+1）**

`model_registry_test.go` 追加（读现有 fixture 风格——mockModelRepo 已有 qwen-turbo）：

```go
func TestResolve_reasoningFromCachedPolicy_noSecondListCall(t *testing.T) {
 repo := &stubModelRepo{models: []*domain.Model{
  {Name: "qwen-turbo", Capabilities: []domain.ModelCapability{domain.CapReasoning}},
 }}
 reg := NewModelRegistry(repo, nil, nil)
 ctx := context.Background()
 _, _, err := reg.Resolve(ctx, "qwen-turbo") // 预热 cache（policy 预计算）
 require.NoError(t, err)
 repo.listCalls = 0
 got := reg.ResolveReasoning(ctx, "qwen-turbo")
 require.True(t, got)
 require.Zero(t, repo.listCalls, "ResolveReasoning must not re-query repo")
}
```

（stub 需有 listCalls 计数——读现有 stub 后扩展；`CapReasoning` 常量名读 domain/model.go 确认。）

- [ ] **Step 3: 运行验证失败**

Run: `go test ./internal/llmgateway/infrastructure/ -run TestResolve -v`
Expected: FAIL（listCalls > 0 或编译失败）

- [ ] **Step 4: 最小实现**

`model_registry.go`：

```go
// ModelPolicy 是模型权威数据的运行时快照（缓存预计算，避免每请求 N+1）。
// nil 表示模型记录不存在（权威数据缺失 → L1-L3 跳过 + WARN + 指标）。
type ModelPolicy struct {
 MaxTokens        int
 ContextWindow    int
 MaxTemperature   *float64
 SamplingDefaults *domain.SamplingParams
 Capabilities     []domain.ModelCapability
 Reasoning        bool
}

// policyFromModel 从模型记录构造 policy；nil 输入返回 nil。
func policyFromModel(m *domain.Model) *ModelPolicy {
 if m == nil {
  return nil
 }
 reasoning := false
 for _, c := range m.Capabilities {
  if c == domain.CapReasoning {
   reasoning = true
   break
  }
 }
 return &ModelPolicy{
  MaxTokens:        m.MaxTokens,
  ContextWindow:    m.ContextWindow,
  MaxTemperature:   m.MaxTemperature,
  SamplingDefaults: m.SamplingParams,
  Capabilities:     m.Capabilities,
  Reasoning:        reasoning,
 }
}
```

`resolvedEntry` 加 `policy *ModelPolicy`；cacheSet 签名扩展携带 policy（读现签名，改为 `cacheSet(ctx, kind, name string, cfg ProviderConfig, p *ModelPolicy, expires ...time.Time)` 或按现签名追加参数——以读到的实际签名为准，5 个调用点同步）。各调用点：

- resolveExact（模型记录命中）→ `policyFromModel(rec)`
- ②③④ 级（无模型记录 / 未入库）→ nil（权威数据不存在语义）
- ResolveFallbackCandidates → 每候选按记录构造
- Warm → 按记录构造

`ResolveReasoning` 改造（638-657）：

```go
func (r *ModelRegistry) ResolveReasoning(ctx context.Context, model string) bool {
 if e, ok := r.cacheGet(ctx, "chat:"+model); ok && e.policy != nil {
  return e.policy.Reasoning
 }
 // cache miss 或 policy 缺失：回退 catalog 纯函数解析（不触发 DB 查询的
 // 目录路径保持现状）。
 return modelSupportsReasoning(model)
}
```

（读现 638-657 的 catalog 逻辑，保留其兜底。cacheGet 现签名若只返回 cfg 则改为返回 entry——以实际代码为准。）

`gateway.go` chainLink 加字段：

```go
 // Policy 是该模型的权威数据快照（DB 模型记录预计算）；nil = 权威数据
 // 不存在（enforceModelPolicy 跳过 L1-L3，只做能力门控）。
 Policy *ModelPolicy
```

`resolveChain` 主模型与候选 link 均赋值 `Policy: g.registry.PolicyFor(ctx, model)`（新方法：cacheGet 命中返回 policy，miss 返回 nil；无 DB 查询）：

```go
// PolicyFor 返回模型权威数据快照（缓存命中）；miss 返回 nil（调用方按
// 权威数据不存在处理，不触发 DB 查询）。
func (r *ModelRegistry) PolicyFor(ctx context.Context, model string) *ModelPolicy {
 if e, ok := r.cacheGet(ctx, "chat:"+model); ok {
  return e.policy
 }
 return nil
}
```

- [ ] **Step 5: 运行验证通过**

Run: `go test ./internal/llmgateway/infrastructure/ -v`
Expected: PASS（含迁移前既有测试——gateway_test 的 ResolveReasoning 路径仍走 catalog 兜底）

- [ ] **Step 6: Commit**

```bash
git add internal/llmgateway/infrastructure/model_registry.go internal/llmgateway/infrastructure/gateway.go
git commit -m "feat(llmgateway): precomputed model policy absorbs ResolveReasoning N+1"
```

---

### Task 6: gateway enforceModelPolicy（L1-L4）+ 语义化拦截错误 + 测试迁移

**Files:**

- Create: `internal/llmgateway/infrastructure/model_policy.go`（enforceModelPolicy 纯函数 + 估算）
- Create: `internal/llmgateway/infrastructure/model_policy_test.go`
- Modify: `internal/llmgateway/infrastructure/gateway.go`（invoke 接线、applyMaxTokensPolicy 静态 clamp 分支移除）
- Modify: `internal/llmgateway/infrastructure/errors.go`（L3/L4 sentinel + WARN 路径指标说明）
- Modify: `internal/llmgateway/domain/errors.go`（新建，L3/L4 语义化错误）
- Modify: `api/middleware/error_mapping.go`（+L2/L3/L4 → 400 映射）
- Modify: `internal/llmgateway/infrastructure/max_tokens_policy_gateway_test.go`（迁移到 DB policy）
- Modify: `api/middleware/error_mapping_test.go`（映射断言）

**Interfaces:**

- Consumes: `ModelPolicy`（Task 5）、`chainLink.Policy`（Task 5）
- Produces:
  - `func enforceModelPolicy(req *CompletionRequest, p *ModelPolicy, reasoning bool) (*CompletionRequest, error)`：floor（仅 reasoning，抬升 `constants.DefaultOutputReserveTokens`）→ L1 clamp → 注入（采样默认值，仅请求未设）→ L2 预检 → L3 校验；返回副本，绝不修改共享 req；返回 `(req, nil)` 表示无拦截。
  - `var ErrSamplingOutOfRange = &policyBlockedError{...}`（domain 包，`Permanent() bool`）
  - `var ErrCapabilityUnsupported = &policyBlockedError{...}`（domain 包）
  - `func estimateMessagesTokens(msgs []domain.Message) int`（bytes/3 确定性估算）
  - `CompletionRequest.Temperature *float64` 新字段（与 SamplingParams 合并注入；读 domain/llm.go 确认现有字段——若 Temperature 是 `float64` 则加 `TemperatureSet bool` 或指针，以现有协议客户端消费方式为准）

- [ ] **Step 1: 先读现文件**

Read `internal/llmgateway/domain/llm.go`（CompletionRequest 现有字段——Temperature 如何表达「未设置」）与 `internal/llmgateway/infrastructure/ollama.go` buildChatRequest（Temperature 消费）。若 `CompletionRequest.Temperature float64`（0=unset）则注入仅当 `req.Temperature == 0`。

- [ ] **Step 2: 写失败测试（迁移 + 新增）**

`max_tokens_policy_gateway_test.go` 迁移（DB 权威）：gatewayFixture 的 mockModelRepo 加 `{Name: "qwen-turbo", ContextWindow: 32768, MaxTokens: 8192}`；断言不变（20000 → 8192；0 透传——但注意 0 现在要注入 DB 值 8192！按 §4 L1：请求 0 → 注入模型 max_tokens(>0)。**原「0 透传」测试改为「0 注入 DB 值」**）。

`model_policy_test.go`：

```go
package infrastructure_test

func TestEnforceModelPolicy_clampThenInjectThenValidate(t *testing.T) {
 // 覆盖 spec §12：执行顺序 clamp → 注入 → 预检 → 校验，
 // 注入值越界必须被 L3 拒绝。
 p := &infrastructure.ModelPolicy{
  MaxTokens: 8192, ContextWindow: 32768,
  MaxTemperature: floatPtr(0.5),
  SamplingDefaults: &domain.SamplingParams{Temperature: floatPtr(0.8)},
 }
 req := &infrastructure.CompletionRequest{Model: "m", MaxTokens: 20000} // 无显式 temperature
 got, err := infrastructure.EnforceModelPolicy(req, p, false)
 require.ErrorIs(t, err, infrastructure.ErrSamplingOutOfRange) // 注入 0.8 > max_temperature 0.5 → L3 拒
 require.Nil(t, got)
}

func TestEnforceModelPolicy_l1Clamp(t *testing.T) {
 p := &infrastructure.ModelPolicy{MaxTokens: 8192, ContextWindow: 32768}
 req := &infrastructure.CompletionRequest{Model: "m", MaxTokens: 20000}
 got, err := infrastructure.EnforceModelPolicy(req, p, false)
 require.NoError(t, err)
 require.Equal(t, 8192, got.MaxTokens)
}

func TestEnforceModelPolicy_injectModelSampling(t *testing.T) {
 p := &infrastructure.ModelPolicy{MaxTokens: 8192,
  SamplingDefaults: &domain.SamplingParams{Temperature: floatPtr(0.7)}}
 req := &infrastructure.CompletionRequest{Model: "m", MaxTokens: 100}
 got, err := infrastructure.EnforceModelPolicy(req, p, false)
 require.NoError(t, err)
 require.Equal(t, 0.7, *got.Temperature)
}

func TestEnforceModelPolicy_l2WindowExceeded(t *testing.T) {
 p := &infrastructure.ModelPolicy{MaxTokens: 8192, ContextWindow: 4096}
 req := &infrastructure.CompletionRequest{
  Model: "m", MaxTokens: 100,
  Messages: []infrastructure.Message{{Role: "user", Content: strings.Repeat("x", 4096*3)}},
 }
 _, err := infrastructure.EnforceModelPolicy(req, p, false)
 require.ErrorIs(t, err, infrastructure.ErrContextLengthExceeded)
 require.True(t, infrastructure.IsContextLengthExceeded(err))
}

func TestEnforceModelPolicy_windowUnknownSkips(t *testing.T) {
 p := &infrastructure.ModelPolicy{MaxTokens: 8192, ContextWindow: 0}
 req := &infrastructure.CompletionRequest{Model: "m", MaxTokens: 100}
 got, err := infrastructure.EnforceModelPolicy(req, p, false)
 require.NoError(t, err)
 require.NotNil(t, got)
}

func TestEnforceModelPolicy_l3SamplingOutOfRange(t *testing.T) {
 p := &infrastructure.ModelPolicy{MaxTokens: 8192, MaxTemperature: floatPtr(0.5)}
 req := &infrastructure.CompletionRequest{Model: "m", MaxTokens: 100, Temperature: floatPtr(0.9)}
 _, err := infrastructure.EnforceModelPolicy(req, p, false)
 require.ErrorIs(t, err, infrastructure.ErrSamplingOutOfRange)
}

func TestEnforceModelPolicy_l4ToolUseKnownNon(t *testing.T) {
 p := &infrastructure.ModelPolicy{MaxTokens: 8192,
  Capabilities: []domain.ModelCapability{}} // 显式空能力集 = known-non
 req := &infrastructure.CompletionRequest{Model: "m", MaxTokens: 100, Tools: []domain.Tool{{Type: "function"}}}
 _, err := infrastructure.EnforceModelPolicy(req, p, false)
 require.ErrorIs(t, err, infrastructure.ErrCapabilityUnsupported)
}

func TestEnforceModelPolicy_l4ToolUseUnknownAllows(t *testing.T) {
 // unknown 放行：policy nil 时 enforce 不执行（gateway 接线层处理），
 // 此处验证 Capabilities 含 tool_use 时放行。
 p := &infrastructure.ModelPolicy{MaxTokens: 8192, Capabilities: []domain.ModelCapability{domain.CapToolUse}}
 req := &infrastructure.CompletionRequest{Model: "m", MaxTokens: 100, Tools: []domain.Tool{{Type: "function"}}}
 got, err := infrastructure.EnforceModelPolicy(req, p, false)
 require.NoError(t, err)
 require.NotNil(t, got)
}

func TestEnforceModelPolicy_reasoningFloorBeforeClamp(t *testing.T) {
 p := &infrastructure.ModelPolicy{MaxTokens: 8192}
 req := &infrastructure.CompletionRequest{Model: "m", MaxTokens: 100}
 got, err := infrastructure.EnforceModelPolicy(req, p, true) // reasoning
 require.NoError(t, err)
 require.Equal(t, 4096, got.MaxTokens) // constants.DefaultOutputReserveTokens
}
```

- [ ] **Step 3: 运行验证失败**

Run: `go test ./internal/llmgateway/infrastructure/ -run 'TestEnforceModelPolicy|TestGatewayComplete_appliesMaxTokensPolicy' -v`
Expected: FAIL（EnforceModelPolicy 未定义 + 0 注入语义未实现）

- [ ] **Step 4: 最小实现**

`internal/llmgateway/domain/errors.go`（新建）：

```go
package domain

// policyBlockedError 是网关策略拦截的语义化错误：permanent（不重试不降级）。
type policyBlockedError struct{ msg string }

func (e *policyBlockedError) Error() string   { return e.msg }
func (e *policyBlockedError) Permanent() bool { return true }

// ErrSamplingOutOfRange 表示采样参数越界（L3：注入或显式值超模型上限）。
var ErrSamplingOutOfRange = &policyBlockedError{msg: "sampling parameter out of range"}

// ErrCapabilityUnsupported 表示请求能力与模型声明不匹配（L4：known-non）。
var ErrCapabilityUnsupported = &policyBlockedError{msg: "requested capability unsupported by model"}
```

`internal/llmgateway/infrastructure/model_policy.go`（新建）：

```go
package infrastructure

import (
 "strings"

 "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
 "github.com/byteBuilderX/stratum/pkg/constants"
)

// estimateMessagesTokens 确定性估算消息 token 数（bytes/3，英文方向约 33%
// 高估 = 保守 fail-closed 侧）。与 agent 预算链估算算法无关，网关层独立。
func estimateMessagesTokens(msgs []domain.Message) int {
 total := 0
 for _, m := range msgs {
  total += len(m.Role) + len(m.Content)
 }
 return total/3 + 1
}

// policyBlockedReason 生成拦截错误的语义化文案（不含敏感信息）。
func policyBlockedReason(format string, args ...any) error {
 return &domain.PolicyBlockedError{...} // 见下方简化
}
```

（简化：L3/L4 直接返回 domain sentinel wrap：`fmt.Errorf("llmgateway: %w (model %s, max %v)", domain.ErrSamplingOutOfRange, ...)`——errors.Is 可匹配。）

```go
// EnforceModelPolicy 对单次尝试副本应用模型权威策略（纯函数，绝不修改共享
// req）。执行顺序固定：floor（仅 reasoning）→ L1 clamp → 采样注入 →
// L2 窗口预检 → L3 采样校验 → L4 能力校验。返回 nil 错误时返回克隆副本
//（无变化返回原 req），拦截时返回 (nil, 错误)。
func EnforceModelPolicy(req *CompletionRequest, p *ModelPolicy, reasoning bool) (*CompletionRequest, error) {
 cloned := *req
 cloned.Model = req.Model
 changed := false
 maxTokens := req.MaxTokens

 if reasoning && maxTokens > 0 && maxTokens < constants.DefaultOutputReserveTokens {
  maxTokens = constants.DefaultOutputReserveTokens
  changed = true
 }
 // L1 clamp：请求值 > 模型上限 → clamp；请求 0 → 注入模型值（>0）。
 if p.MaxTokens > 0 {
  if maxTokens > p.MaxTokens {
   maxTokens = p.MaxTokens
   changed = true
  } else if maxTokens <= 0 {
   maxTokens = p.MaxTokens
   changed = true
  }
 }
 cloned.MaxTokens = maxTokens

 // 采样注入：请求未显式设置（0=unset）→ 模型默认 → provider 默认。
 temp := req.Temperature
 if temp == nil && p.SamplingDefaults != nil && p.SamplingDefaults.Temperature != nil {
  v := *p.SamplingDefaults.Temperature
  temp = &v
  changed = true
 }
 cloned.Temperature = temp

 // L2 窗口预检：context_window=0（未知）跳过；估算 + 有效 max_tokens > 窗口 → 拒。
 if p.ContextWindow > 0 && maxTokens > 0 {
  if estimateMessagesTokens(req.Messages)+maxTokens > p.ContextWindow {
   return nil, fmt.Errorf("llmgateway: %w (model %s, estimated %d + max_tokens %d > window %d)",
    ErrContextLengthExceeded, req.Model, estimateMessagesTokens(req.Messages), maxTokens, p.ContextWindow)
  }
 }
 // L3 采样校验（注入后的有效值）：temperature > min(1, max_temperature) → 拒；
 // max_temperature=0 且带 temperature → 拒（不支持）。
 if temp != nil {
  if p.MaxTemperature == nil {
   if *temp < 0 || *temp > 1 {
    return nil, fmt.Errorf("llmgateway: %w (temperature %v outside [0,1])", domain.ErrSamplingOutOfRange, *temp)
   }
  } else if *p.MaxTemperature == 0 {
   return nil, fmt.Errorf("llmgateway: %w (temperature not supported, max_temperature=0)", domain.ErrSamplingOutOfRange)
  } else if *temp > *p.MaxTemperature {
   return nil, fmt.Errorf("llmgateway: %w (temperature %v exceeds max %v)", domain.ErrSamplingOutOfRange, *temp, *p.MaxTemperature)
  }
 }
 // L4 能力不匹配（tool_use）：known-non（显式标注且不含 tool_use）→ 拒；
 // unknown（policy nil 由调用方短路）放行。
 if len(req.Tools) > 0 && len(p.Capabilities) > 0 {
  hasToolUse := false
  for _, c := range p.Capabilities {
   if c == domain.CapToolUse {
    hasToolUse = true
    break
   }
  }
  if !hasToolUse {
   return nil, fmt.Errorf("llmgateway: %w (model %s lacks tool_use)", domain.ErrCapabilityUnsupported, req.Model)
  }
 }
 if !changed {
  return req, nil
 }
 return &cloned, nil
}
```

注意：`CompletionRequest.Temperature` 若现为 `float64`（0=unset）则改为 `*float64`（协议客户端 buildChatRequest 消费处改：`if req.Temperature != nil { body.Temperature = *req.Temperature }`——读 ollama.go/anthropic.go 后同步；或保持 float64 + 单独 `TemperatureSet bool`，**以读到的实际结构为准**，二选一禁止混用）。

`gateway.go` invoke 接线：

```go
 attemptReq := g.applyCapabilityGate(req, link)
 if link.Policy != nil {
  enforced, err := EnforceModelPolicy(attemptReq, link.Policy, link.Reasoning)
  if err != nil {
   g.metrics.IncLLMRequest(link.Model, link.Config.Name, llmStatusError)
   g.metrics.IncPolicyBlocked(link.Model) // 见 observability 指标扩展
   g.logger.Warn("llmgateway: model policy blocked request",
    zap.String("model", link.Model), zap.Error(err))
   return nil, false, err
  }
  attemptReq = enforced
 } else {
  // 权威数据不存在：L1-L3 跳过，维持 applyCapabilityGate 结果。
  g.metrics.IncPolicyMissing(link.Model)
 }
```

`applyMaxTokensPolicy` 静态 clamp 分支移除（保留 reasoning floor）：

```go
func (g *Gateway) applyMaxTokensPolicy(req *CompletionRequest, link chainLink) *CompletionRequest {
 if req.MaxTokens <= 0 || !link.Reasoning {
  return req
 }
 if req.MaxTokens >= constants.DefaultOutputReserveTokens {
  return req
 }
 cloned := *req
 cloned.Model = link.Model
 cloned.MaxTokens = constants.DefaultOutputReserveTokens
 g.logger.Warn("llmgateway: max_tokens raised to floor for reasoning model",
  zap.String("model", link.Model),
  zap.Int("max_tokens", req.MaxTokens),
  zap.Int("raised_to", constants.DefaultOutputReserveTokens))
 return &cloned
}
```

（floor 在 enforce 内已做——**二选一**：enforce 内做 floor 则 applyMaxTokensPolicy 整体删除。选择：floor 逻辑保留在 enforceModelPolicy（与 L1 同函数保证「先 floor 后 clamp」原子性），applyMaxTokensPolicy 从 invoke 链移除并删除函数。）

`api/middleware/error_mapping.go` errorStatusTable 追加（读表结构后加入）：

```go
 llmgatewaydomain.ErrSamplingOutOfRange:      http.StatusBadRequest,
 llmgatewaydomain.ErrCapabilityUnsupported:   http.StatusBadRequest,
```

- duck-type 探测（MapErrorToStatus 内、HTTPError 探测后）：

```go
 var ctxLen interface{ ContextLengthExceeded() bool }
 if errors.As(err, &ctxLen) && ctxLen.ContextLengthExceeded() {
  return http.StatusBadRequest
 }
```

`observability.MetricsProvider` 加 `IncPolicyBlocked(model string)` / `IncPolicyMissing(model string)`（读 observability 接口与 NoopMetrics 后追加；指标名 `llmgateway.policy_blocked` / `llmgateway.policy_missing`）。

`max_tokens_policy_gateway_test.go` 迁移（DB 权威）：gatewayFixture 的 mockModelRepo 扩展 `{Name: "qwen-turbo", ContextWindow: 32768, MaxTokens: 8192}`；`TestGatewayComplete_appliesMaxTokensPolicy` 断言不变；`TestGatewayComplete_policyKeepsUnsetMaxTokens` 改名 `_injectsModelMaxTokens`，断言 `proto.req.MaxTokens == 8192`。

- [ ] **Step 5: 运行验证通过**

Run: `go test ./internal/llmgateway/... ./api/middleware/ -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/llmgateway/ api/middleware/error_mapping.go
git commit -m "feat(llmgateway): gateway enforceModelPolicy L1-L4 with semantic block errors"
```

---

### Task 7: ProviderConfig extra_headers + 三客户端共享 header helper

**Files:**

- Modify: `internal/llmgateway/infrastructure/provider_runtime.go`（protocol() 并入 ExtraHeaders）
- Modify: `internal/llmgateway/infrastructure/model_registry.go`（ProviderConfig 构造处 5 点）
- Create: `internal/llmgateway/infrastructure/http_headers.go`（共享 helper）
- Create: `internal/llmgateway/infrastructure/http_headers_test.go`
- Modify: `internal/llmgateway/infrastructure/openai_compat.go` / `anthropic.go` / `ollama.go`（chat + operational 端点应用）
- Modify: `internal/llmgateway/infrastructure/gateway_test.go`（ProviderConfig 断言同步，若有）

**Interfaces:**

- Consumes: `Provider.ExtraHeaders`（Task 2）
- Produces: `ProviderConfig.ExtraHeaders map[string]string`；`func applyExtraHeaders(header http.Header, extra map[string]string)`（先应用 extra，调用方随后设置硬编码鉴权头覆盖）

- [ ] **Step 1: 写失败测试**

`http_headers_test.go`：

```go
func TestApplyExtraHeaders_beforeHardcodedAuth(t *testing.T) {
 h := make(http.Header)
 applyExtraHeaders(h, map[string]string{"X-Tenant": "t1"})
 h.Set("Authorization", "Bearer final")
 require.Equal(t, "Bearer final", h.Get("Authorization"))
 require.Equal(t, "t1", h.Get("X-Tenant"))
}

func TestApplyExtraHeaders_nilSafe(t *testing.T) {
 h := make(http.Header)
 applyExtraHeaders(h, nil)
 require.Empty(t, h)
}
```

- [ ] **Step 2: 运行验证失败**

Run: `go test ./internal/llmgateway/infrastructure/ -run TestApplyExtraHeaders -v`
Expected: FAIL

- [ ] **Step 3: 最小实现**

`http_headers.go`：

```go
package infrastructure

import "net/http"

// applyExtraHeaders 把 provider extra_headers 应用到请求头（key 已由写时
// 校验归一化）。调用方必须在之后设置自身硬编码鉴权头（Authorization/
// x-api-key/Content-Type 等），使鉴权头永远覆盖用户配置。
func applyExtraHeaders(h http.Header, extra map[string]string) {
 for k, v := range extra {
  h.Set(k, v)
 }
}
```

`provider_runtime.go` protocol() 与 model_registry.go 各 ProviderConfig 构造点加 `ExtraHeaders: provider.ExtraHeaders`（provider 从 registry 解析结果带出——读 registry 的 ProviderConfig 构造处确认 provider 实体可达性；resolveExact 已有 provider 实体 ✓）。

`openai_compat.go` 两个 chat 请求（Complete/CompleteStream）+ Discover/Health/ListModels：header 构造处先 `applyExtraHeaders(h, cfg.ExtraHeaders)` 再 `h.Set("Authorization", "Bearer "+cfg.APIKey)`；`anthropic.go` setHeaders 同（先 extra 后 x-api-key）；`ollama.go` 同。

- [ ] **Step 4: 运行验证通过**

Run: `go test ./internal/llmgateway/... -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/llmgateway/infrastructure/
git commit -m "feat(llmgateway): provider extra_headers merged before hardcoded auth headers"
```

---

### Task 8: agent 账本链插 DB 权威（Goal 3 唯一例外）

**Files:**

- Modify: `internal/agent/domain/system_assistant.go`（TenantModelDetail 加 MaxTokens）
- Modify: `api/wiring/tenant_resolver.go`（投影加 MaxTokens: m.MaxTokens）
- Modify: `internal/agent/application/agent_service.go`（resolveOutputReserve 签名 + 链头）
- Modify: `api/wiring/agent.go:323` 附近（若需同步——VendorWindowLookup 不动，确认）

**Interfaces:**

- Consumes: `TenantModelDetailsProvider.ListTenantModelDetails(ctx, tenantID)`（已有依赖 `s.deps.ModelDetailsProvider`，agent_service.go:2813）
- Produces: `resolveOutputReserve(ctx context.Context, tenantID, model string, explicitMaxTokens int) int`：显式 > DB 模型 max_tokens > vendor 表 maxOut > 4096

- [ ] **Step 1: 写失败测试**

`agent_service_test.go`（读现有 fixture——mockModelDetailsProvider 若不存在则新建 stub）：

```go
func TestAgentService_resolveOutputReserve_prefersDBModelMaxTokens(t *testing.T) {
 // 显式 0、vendor maxOut 4096、DB 模型 max_tokens 8192 → 8192
 deps := fixtureDeps(t)
 deps.ModelDetailsProvider = &stubModelDetailsProvider{details: []domain.TenantModelDetail{
  {Model: "qwen-turbo", MaxTokens: 8192},
 }}
 svc := NewAgentService(deps)
 got := svc.resolveOutputReserve(context.Background(), "tenant-1", "qwen-turbo", 0)
 require.Equal(t, 8192, got)
}
```

- [ ] **Step 2: 运行验证失败**

Run: `go test ./internal/agent/application/ -run TestAgentService_resolveOutputReserve -v`
Expected: FAIL

- [ ] **Step 3: 最小实现**

`system_assistant.go` TenantModelDetail 加字段：

```go
 // MaxTokens 是模型权威最大输出（0=未知）；预算账本链头读取。
 MaxTokens int `json:"maxTokens"`
```

`api/wiring/tenant_resolver.go` 投影加 `MaxTokens: m.MaxTokens`（ListModelsByTenantDetails 返回 domain.Model 含 MaxTokens ✓）。

`agent_service.go`：

```go
// resolveOutputReserve 解析主模型输出预留（Spec 第 2 节 outputReserve 来源链）：
// 显式 cfg.MaxTokens（>0）> DB 模型 max_tokens（权威）> vendor 表 maxOut >
// DefaultOutputReserveTokens。DB 权威插入链头：欠预留（预留 < 发送值）会溢出
// 窗口被 provider 400 永久中止，预留必须与 L1 注入一致。
func (s *AgentService) resolveOutputReserve(ctx context.Context, tenantID, model string, explicitMaxTokens int) int {
 if explicitMaxTokens > 0 {
  return explicitMaxTokens
 }
 if s.deps.ModelDetailsProvider != nil {
  if details, err := s.deps.ModelDetailsProvider.ListTenantModelDetails(ctx, tenantID); err == nil {
   for _, d := range details {
    if d.Model == model && d.MaxTokens > 0 {
     return d.MaxTokens
    }
   }
  }
 }
 if s.deps.VendorWindowLookup != nil {
  if _, maxOut := s.deps.VendorWindowLookup(model); maxOut > 0 {
   return maxOut
  }
 }
 return constants.DefaultOutputReserveTokens
}
```

调用点 2144 改：

```go
  WithOutputReserve(s.resolveOutputReserve(ctx, meta.TenantID, a.GetConfig().LLMModel, a.GetConfig().MaxTokens)),
```

- [ ] **Step 4: 运行验证通过**

Run: `go test ./internal/agent/... ./api/wiring/ -run 'TestAgentService_resolveOutputReserve|TestTenantCapabilityResolver' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/agent/ api/wiring/
git commit -m "feat(agent): output reserve chain prefers DB model max_tokens"
```

---

### Task 9: contract golden + e2e provision 同步 + 测试 mock 同步

**Files:**

- Modify: `api/http/contract_test.go`（stub 带新字段 + 新用例：Update 写 extra_headers 后响应不含值）
- Modify: `api/http/testdata/contracts/*.golden.json`（若响应含新字段——GET 响应是否带 sampling_params 取决于 handler 返回体；**extra_headers/default_sampling write-only 永不出现在 golden**）
- Modify: `test/e2e/llm_admin_test.go`（provisionPublicCatalog 手工 DDL 同步 4 列）
- Modify: 其余 repository 测试 mock（port 签名同步后的编译错误逐个修复）

- [ ] **Step 1: 先读现文件**

Read `api/http/contract_test.go`（contractModelRepo/contractProviderRepo stub、llm 用例结构）与 `test/e2e/llm_admin_test.go` 75-118（provisionPublicCatalog）。

- [ ] **Step 2: 写失败测试/断言**

`contract_test.go` 新用例（先读现用例结构，仿写）：

```go
 {
  name:   "update provider hides extra_headers values",
  method: http.MethodPut,
  path:   "/admin/providers/p1",
  body: map[string]any{
   "name": "p1", "extraHeaders": map[string]any{"X-Tenant": "secret-value"},
  },
  wantStatus: http.StatusOK,
  wantBody: map[string]any{
   "id": "p1", "name": "p1", "kind": "openai_compat",
   // extraHeaders 键与值都不出现
  },
 }
```

- [ ] **Step 3: 运行验证失败**

Run: `go test ./api/http/ -run TestContract -v`
Expected: FAIL

- [ ] **Step 4: 最小实现**

contract stub 扩展（Update 返回带新字段的记录；Get 返回含 sampling_params 的模型）；handler 层确认 Update 响应体（读 model_mgmt_handler.go:62 Update 的 render 逻辑——若返回更新后 model 则 golden 断言其不含 extraHeaders 字段）。

`test/e2e/llm_admin_test.go` provisionPublicCatalog 手工 DDL 加 4 列（providers 8 → 10 列、models 14 → 16 列；INSERT 列清单与值同步）。

- [ ] **Step 5: 运行验证通过**

Run: `go test ./api/http/ -run TestContract -v && go vet ./test/e2e/`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add api/http/ test/e2e/
git commit -m "test(llmgateway): contract golden write-only assertion and e2e catalog columns"
```

---

### Task 10: 前端高级折叠区

**Files:**

- Modify: `web/src/modules/llm/model/llm.ts`（Model/Provider/UpdateModelInput/UpdateProviderInput 类型）
- Modify: `web/src/modules/llm/components/ModelEditDrawer.tsx`（高级折叠区）
- Modify: `web/src/modules/llm/components/ProviderForm.tsx`（headers 掩码 + 默认采样）

**Interfaces:**

- Consumes: 后端 JSON 契约（Task 9）：`samplingParams {temperature?, topP?, ...}`、`maxTemperature`、`extraHeaders`（仅接收不回显）
- Produces: 类型字段与表单字段名与后端 camelCase 一致

- [ ] **Step 1: 读现文件**

Read `web/src/modules/llm/model/llm.ts`、`web/src/modules/llm/components/ModelEditDrawer.tsx`、`web/src/modules/llm/components/ProviderForm.tsx`。

- [ ] **Step 2: 类型扩展**

`llm.ts`：

```ts
export interface SamplingParams {
  temperature?: number
  topP?: number
  frequencyPenalty?: number
  presencePenalty?: number
  seed?: number
}

export interface Model {
  // ... 既有字段
  samplingParams?: SamplingParams | null
  maxTemperature?: number | null
}

export interface UpdateModelInput {
  // ... 既有字段
  samplingParams?: SamplingParams | null   // null = 清空；省略 = 保留
  maxTemperature?: number | null           // null = 清空
}

export interface UpdateProviderInput {
  // ... 既有字段
  extraHeaders?: Record<string, string> | null  // 仅请求；响应永不返回
  defaultSampling?: Record<string, unknown> | null
}
```

- [ ] **Step 3: ModelEditDrawer 高级折叠区**

在 Form 内追加 `Collapse` 高级区（AntD）：`samplingParams.temperature`（Slider 0-1 step 0.01，提示「请求未显式配置时生效」）、`maxTemperature`（Slider 0-1，tooltip「0=不支持 temperature；NULL=全局契约」）。提交时 `samplingParams` 空对象与 `maxTemperature` null 处理遵循「空=保留、null=清空」——初始值从 record 填充，用户未动不提交。

- [ ] **Step 4: ProviderForm headers 掩码**

高级区加 headers 键值编辑（受控 Input）：已配置键显示值掩码「已配置」，改动才提交；黑名单键（Authorization/Content-Type/X-API-Key 等）Select/输入禁用。

- [ ] **Step 5: 验证**

Run: `make fe-lint && make fe-build`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add web/src/modules/llm/
git commit -m "feat(web): model editable params advanced sections"
```

---

### Task 11: 本地验证全量

**Files:** 无（验证）

- [ ] **Step 1: 守卫与快速验证**

Run:

```bash
bash scripts/quality/risk-regression-guard.sh --explain
go vet ./... && go test -short ./...
make fe-lint && make fe-build
make code-quality
```

Expected: 全绿；门禁函数复杂度不超限（新增函数已按 ≤120 行/嵌套 ≤4 设计）。

- [ ] **Step 2: 修复残留**

Run: `make risk-guardrails`（提交前守卫）；任何失败修复后重跑。

- [ ] **Step 3: Commit**

```bash
git add -A
git commit -m "chore: verification pass for model editable params"  # 无残留时不提交
```

---

### Task 12: PR → CI → 合并 → CD 部署（goal 达成）

**Files:** 无（流程）

- [ ] **Step 1: worktree 提交与推送**

```bash
cd /home/yang/go-projects/stratum-model-editable-params
git push -u origin feat/model-editable-params
gh pr create --base main --title "feat(llmgateway): 模型管理可编辑参数——权威硬拦截+默认注入+extra_headers" --body "What: ... Why: ... HowToTest: ..."
```

- [ ] **Step 2: CI 等待期间检查 base 落后**

```bash
git fetch origin main
git merge-base --is-ancestor origin/main HEAD~0 || echo "base behind"
```

落后则 `git merge origin/main` → 本地验证 → push。

- [ ] **Step 3: CI 全绿后合并**

```bash
gh pr merge --squash --delete-branch
git worktree remove ../stratum-model-editable-params
```

- [ ] **Step 4: 确认 CD pipeline 触发并成功**

CI 成功（deploy.yml `workflow_run` on completed success + branch main + SHA==main）→ 观察 Actions run：build-backend/frontend/feishu-adapter → build-and-push → deploy（helm upgrade --atomic --wait）→ Verify deployment（kubectl rollout status + port-forward 18080 /api/health + PUBLIC_BASE_URL/api/health jq `.status=="ok"` `.service=="Stratum"`）→ attest/upload evidence。

- [ ] **Step 5: 远端验证（只读）**

```bash
ssh root@101.200.181.141 "kubectl get pods -n stratum -o wide && curl -s localhost:18080/api/health | jq ."
```

Expected: Running、health ok。**goal 条件达成：CD 部署成功。**

---

## Self-Review（写完后内检）

- **Spec 覆盖**：§5 数据模型 → Task 1/2/3；§4 采纳链 → Task 6；§6 执行点/吸收 N+1 → Task 5/6；§7 契约 → Task 9；§8 extra_headers 安全 → Task 2/7；§9 前端 → Task 10；§10 冲击梳理 → Task 6（L1/账本 Task 8）；§11 审计 → Task 3/4；§12 测试策略 → 各任务测试步骤；§13 文件清单 → 逐项对应。
- **占位符扫描**：无 TBD；所有代码块完整；「读现文件后替换」均指向确定性改动点（签名/列/断言），实现时以读到的实际代码为准做等价替换。
- **类型一致性**：`EnforceModelPolicy(req, p, reasoning)`、`ModelPolicy{MaxTokens,ContextWindow,MaxTemperature,SamplingDefaults,Capabilities,Reasoning}`、`Update(ctx, m, tenantID, audit)`、`resolveOutputReserve(ctx, tenantID, model, explicit)`、`CompletionRequest.Temperature *float64`（或 TemperatureSet——实现首步确认）贯穿 Task 3-8 一致；审计 INSERT 11 参数与 `ChangeAuditInsertSQL` 一致；metrics 名 `llmgateway.policy_blocked`/`policy_missing` 一致。
