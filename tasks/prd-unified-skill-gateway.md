# PRD: Skill 统一能力入口（Unified Skill Gateway）

## 1. 介绍

当前项目中 skill 包存在以下问题：
- `internal/skill` 定义了基础接口，但原子执行能力薄弱（无可观测性、无限流、无统一错误体系）
- `internal/mcp/skill_adapter.go` 和 `internal/orchestrator/registry.go` 各自独立，没有统一的编排入口
- 流程化 skill 调用（多 skill 组合、条件分支、状态传递）完全缺失
- 企业级能力（metrics、tracing、熔断、审计日志、请求追踪 ID）分散或缺失

本 PRD 定义一个 **Unified Skill Gateway**：在现有代码基础上重构，提供原子工具调用和标准化流程化 skill 调用的统一对内 Go 包入口，同时集成企业级工程规范。

---

## 2. 目标

- 提供统一的 `SkillGateway` 接口，屏蔽底层 skill 类型差异（LLM/Code/MCP）
- 支持原子调用：单个 skill 的标准化执行，含超时、重试、熔断
- 支持流程化调用：多 skill 按 DAG 或链式编排，支持条件分支和上下文传递
- 集成可观测性：Prometheus metrics、OpenTelemetry tracing、结构化审计日志
- 统一错误码体系和请求追踪 ID（trace_id 贯穿全链路）
- 支持插件化扩展：新 skill 类型无需修改核心代码
- 保持向后兼容：现有 `skill_handler.go`、`MCPSkillAdapter`、`orchestrator.Registry` 可平滑迁移

---

## 3. 用户故事

### US-001: 统一 SkillGateway 接口定义
**描述：** 作为项目内部开发者，我需要一个统一的 `SkillGateway` Go 接口，使得 agent、orchestrator、handler 等模块通过同一入口调用任意类型的 skill，而不需要关心底层实现。

**验收标准：**
- [ ] 定义 `SkillGateway` 接口，包含 `Execute(ctx, req) (resp, error)` 和 `ExecutePipeline(ctx, pipeline) (resp, error)` 方法
- [ ] 定义标准 `SkillRequest` 和 `SkillResponse` 结构体，包含 `TraceID`、`SkillID`、`Input`、`Metadata` 字段
- [ ] 定义统一错误类型 `SkillError`，包含 `Code`、`Message`、`Cause` 字段
- [ ] 现有 `skill.Skill` 接口保持不变，新接口通过适配器兼容
- [ ] `go build ./...` 通过，无编译错误

### US-002: 原子 skill 执行（含企业级能力）
**描述：** 作为调用方，我希望通过 `SkillGateway.Execute` 调用单个 skill 时，自动获得超时控制、重试、熔断、metrics 上报和 trace span 注入，无需在调用方重复实现。

**验收标准：**
- [ ] 每次 `Execute` 调用自动注入 `trace_id`（若上游未传则生成新 UUID）
- [ ] 支持可配置超时（默认 30s），超时返回 `ErrSkillTimeout` 错误码
- [ ] 支持可配置重试次数（默认 0），指数退避，仅对幂等错误重试
- [ ] 集成 Prometheus counter/histogram：`skill_executions_total`、`skill_execution_duration_seconds`，label 含 `skill_id`、`skill_type`、`status`
- [ ] 集成 OpenTelemetry：每次执行创建子 span，span 名为 `skill.execute.<skill_id>`
- [ ] 执行结果（成功/失败）写入结构化审计日志，含 `trace_id`、`skill_id`、`duration`、`status`
- [ ] `go test ./internal/skillgateway/...` 通过

### US-003: 流程化 skill 编排（Pipeline）
**描述：** 作为 agent 开发者，我希望通过 Go DSL（builder 模式）定义多步骤 skill 流程，支持顺序执行、条件分支和上下文变量传递，并通过 `SkillGateway.ExecutePipeline` 统一执行。

**验收标准：**
- [ ] 提供 `PipelineBuilder` 支持链式调用：`.Step(skillID, input).If(condition).Then(...).Else(...).Build()`
- [ ] 支持步骤间上下文传递：前一步的 `Output` 可作为后一步的 `Input` 参数（通过 `$prev.output` 引用）
- [ ] 支持并行步骤：`.Parallel(step1, step2)` 并发执行，等待全部完成
- [ ] Pipeline 执行失败时返回包含失败步骤信息的 `PipelineError`
- [ ] Pipeline 整体执行有 trace span，每个步骤为子 span
- [ ] `go test ./internal/skillgateway/...` 中 pipeline 相关测试通过

### US-004: 插件化 skill 注册与发现
**描述：** 作为扩展开发者，我希望通过实现标准接口并调用 `Register` 方法注册新 skill 类型，无需修改 gateway 核心代码。

**验收标准：**
- [ ] 定义 `SkillProvider` 接口，实现者可通过 `gateway.RegisterProvider(provider)` 注册
- [ ] 内置三个 Provider：`LLMSkillProvider`、`CodeSkillProvider`、`MCPSkillProvider`（适配现有代码）
- [ ] `MCPSkillProvider` 复用现有 `MCPSkillAdapter` 逻辑，不重复实现
- [ ] `orchestrator.Registry` 通过适配器接入 `SkillGateway`，现有 `skill_handler.go` 无需修改
- [ ] 注册重复 skill ID 返回 `ErrSkillAlreadyExists` 错误
- [ ] `go test ./internal/skillgateway/...` 通过

### US-005: 统一错误码体系与熔断
**描述：** 作为运维人员，我希望所有 skill 调用失败都有明确的错误码，并且对持续失败的 skill 自动熔断，防止级联故障。

**验收标准：**
- [ ] 定义错误码枚举：`ErrSkillNotFound(4001)`、`ErrSkillTimeout(4002)`、`ErrSkillExecFailed(5001)`、`ErrCircuitOpen(5002)`、`ErrPipelineStepFailed(5003)`
- [ ] 集成熔断器（使用 `sony/gobreaker` 或自实现）：连续失败 5 次后熔断，30s 后半开
- [ ] 熔断状态变更写入日志并上报 Prometheus gauge `skill_circuit_breaker_state`
- [ ] 所有错误响应包含 `trace_id` 字段，便于日志关联
- [ ] `go test ./internal/skillgateway/...` 中熔断相关测试通过

---

## 4. 功能需求

- FR-1: `SkillGateway` 必须提供 `Execute(ctx context.Context, req SkillRequest) (SkillResponse, error)` 方法
- FR-2: `SkillGateway` 必须提供 `ExecutePipeline(ctx context.Context, pipeline Pipeline) (PipelineResult, error)` 方法
- FR-3: 系统必须为每个 `Execute` 调用自动生成或传播 `trace_id`
- FR-4: 系统必须上报 `skill_executions_total` 和 `skill_execution_duration_seconds` Prometheus 指标
- FR-5: 系统必须为每个 skill 执行创建 OpenTelemetry span
- FR-6: 系统必须支持通过 `SkillProvider` 接口注册自定义 skill 类型
- FR-7: 系统必须对每个 skill 维护独立的熔断器状态
- FR-8: `PipelineBuilder` 必须支持顺序、条件分支、并行三种步骤类型
- FR-9: Pipeline 步骤间必须支持通过 `$prev.output` 引用上一步输出
- FR-10: 所有错误必须使用统一 `SkillError` 类型，包含错误码和 `trace_id`
- FR-11: 系统必须写入结构化审计日志，记录每次 skill 执行的调用方、skill_id、duration、status

---

## 5. 非目标（范围外）

- 不实现对外 HTTP/gRPC API（现有 `skill_handler.go` 保持不变）
- 不实现 CLI 工具
- 不实现 skill 的持久化存储（skill 定义仍在内存中）
- 不实现 skill 版本管理和热加载（列为 Open Questions）
- 不实现 YAML/JSON 声明式 pipeline 配置（仅 Go DSL）
- 不替换现有 `internal/skill` 包的基础接口定义

---

## 6. 技术考量

- 新模块路径：`internal/skillgateway/`，不影响现有 `internal/skill/`
- 熔断器依赖：优先使用 `sony/gobreaker`（已是 Go 生态标准库），若项目不引入新依赖则自实现简单状态机
- Metrics：复用项目现有 Prometheus 集成（`pkg/` 下已有相关代码）
- Tracing：复用项目现有 OpenTelemetry 集成（`internal/observability/`）
- 向后兼容：`orchestrator.Registry` 通过 `RegistryAdapter` 实现 `SkillProvider` 接口接入 gateway

---

## 7. 成功指标

- 所有现有 skill 相关测试继续通过（无回归）
- `internal/skillgateway/` 测试覆盖率 ≥ 80%
- 单个 skill 原子调用 P99 延迟增加 ≤ 1ms（gateway 自身开销）
- 新增 skill 类型只需实现 `SkillProvider` 接口，无需修改 gateway 核心代码

---

## 8. 开放问题

- skill 版本管理策略（同一 skill ID 多版本共存）是否需要？
- Pipeline 定义是否需要持久化（数据库/文件）以支持复用？
- 熔断器参数（阈值、恢复时间）是否需要运行时动态配置？
- `CodeSkill` 的实际代码执行能力（当前为 placeholder）是否在本期实现？
