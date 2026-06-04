# SPEC: Unified Skill Gateway

  > 技术规范来源：tasks/prd-unified-skill-gateway.md
  > 生成日期：2026-05-30 | 目标分支：feat/local-cicd-setup | Commit：deaf2c2

## 1. Summary

### 1.1 What This SPEC Covers

  本 SPEC 定义 `internal/skillgateway` 包的完整技术实现，该包作为项目内所有 skill 调用的统一对内 Go
  入口。覆盖：核心接口定义、原子执行引擎（含超时/重试/熔断）、Pipeline 编排引擎（顺序/条件/并行）、插件化 Provider 注册机制、可观测性集成（Prometheus +
  结构化审计日志）、统一错误码体系，以及与现有 `orchestrator.Registry`、`MCPSkillAdapter`、`skill_handler.go` 的向后兼容适配层。

### 1.2 PRD Reference

- Source: `tasks/prd-unified-skill-gateway.md`
- User Stories covered: US-001, US-002, US-003, US-004, US-005
- Functional Requirements covered: FR-1 ~ FR-11

### 1.3 Design Decisions Summary

  | Decision | Choice | Rationale |
  |----------|--------|-----------|
  | 熔断器实现 | 自实现简单状态机 | 项目无 `gobreaker` 依赖，引入新依赖需评审；状态机逻辑简单，自实现可控 |
   |
  | Metrics 复用 | 直接使用 `pkg/observability.PrometheusMetrics` | `skill_executions_total` 和 `skill_execution_duration_seconds` 已存在，避免重复注册
  panic |
  | Pipeline 上下文传递 | Go `map[string]any` + 键名约定 `$steps.<step_name>.output` | 比 `$prev.output` 更灵活，支持跨步骤引用；PRD 中 `$prev.output`
  作为语法糖保留 |
  | 模块位置 | `internal/skillgateway/` | 不污染现有 `internal/skill/`，新旧代码边界清晰 |
  | 向后兼容 | `RegistryAdapter` 包装 `orchestrator.Registry` | `skill_handler.go` 零修改，通过适配器接入 gateway |

  ---
  
## 2. Architecture
  
### 2.1 System Context
  
  ┌─────────────────────────────────────────────────────────┐
  │                    调用方（内部）                          │
  │  agent.BaseAgent  │  api/handler  │  orchestrator        │
  └──────────┬────────┴───────┬───────┴──────────┬───────────┘
             │                │                  │
             └────────────────▼──────────────────┘
                      ┌───────────────┐
                      │  SkillGateway │  ← 统一入口
                      │  (interface)  │
                      └───────┬───────┘
                              │
                ┌─────────────┼─────────────┐
                ▼             ▼             ▼
          AtomicEngine   PipelineEngine  ProviderRegistry
          (超时/重试/熔断) (DAG编排)      (插件注册)
                │                             │
                └──────────────┬──────────────┘
                               ▼
                ┌──────────────────────────────┐
                │         SkillProvider        │
                ├──────────┬───────────────────┤
                │LLMProvider│MCPProvider│CodeProvider│RegistryAdapter│
                └──────────┴───────────────────┘
                      │           │
                llmgateway   mcp.MCPSkillAdapter

### 2.2 Component Design

  **`SkillGateway`（接口）**

- 对外暴露两个方法：`Execute` 和 `ExecutePipeline`
- 实现类 `DefaultGateway` 组合 `AtomicEngine`、`PipelineEngine`、`ProviderRegistry`

  **`AtomicEngine`**

- 职责：单个 skill 的标准化执行
- 内置：trace_id 注入、超时控制、指数退避重试、熔断器检查、metrics 上报、审计日志

  **`PipelineEngine`**

- 职责：多步骤 skill 编排执行
- 内置：步骤间上下文传递、条件分支求值、并行步骤协调

  **`ProviderRegistry`**

- 职责：管理所有 `SkillProvider` 实现，按 skill_id 路由到对应 provider
- 内置：重复注册检测、并发安全

  **`CircuitBreaker`**

- 职责：per-skill 熔断状态管理
- 状态：`Closed → Open → HalfOpen → Closed`

  **`PipelineBuilder`**

- 职责：提供链式 DSL 构建 `Pipeline` 定义

### 2.3 Module Interactions

  **原子执行流程：**
  调用方
    → gateway.Execute(ctx, SkillRequest)
    → AtomicEngine.execute(ctx, req)
        → 注入/传播 trace_id
        → CircuitBreaker.Allow(skillID)?
            → No: 返回 ErrCircuitOpen
            → Yes: ProviderRegistry.Resolve(skillID)
                → provider.Execute(ctx, input)
                → 成功: CircuitBreaker.RecordSuccess(skillID)
                → 失败: CircuitBreaker.RecordFailure(skillID)
        → metrics.IncSkillExecution(skillID, status)
        → metrics.RecordSkillExecutionDuration(skillID, duration)
        → auditLogger.Log(trace_id, skillID, duration, status)
    → 返回 SkillResponse / SkillError

  **Pipeline 执行流程：**
  调用方
    → gateway.ExecutePipeline(ctx, pipeline)
    → PipelineEngine.execute(ctx, pipeline)
        → 为 pipeline 生成 pipeline_trace_id
        → 按步骤类型分发：
            Sequential: 逐步调用 AtomicEngine.execute，传递 stepContext
            Conditional: 求值 condition(stepContext)，选择 then/else 分支
            Parallel: goroutine 并发调用，sync.WaitGroup 等待
        → 每步结果写入 stepContext["$steps..output"]
        → 任一步失败: 返回 PipelineError{FailedStep, Cause}
    → 返回 PipelineResult

### 2.4 File Structure

  internal/skillgateway/
  ├── gateway.go              [NEW] SkillGateway 接口 + DefaultGateway 实现
  ├── types.go                [NEW] SkillRequest, SkillResponse, SkillError, 错误码常量
  ├── atomic.go               [NEW] AtomicEngine
  ├── circuit_breaker.go      [NEW] CircuitBreaker 状态机
  ├── pipeline.go             [NEW] PipelineEngine + Pipeline 类型定义
  ├── pipeline_builder.go     [NEW] PipelineBuilder DSL
  ├── provider.go             [NEW] SkillProvider 接口 + ProviderRegistry
  ├── providers/
  │   ├── llm_provider.go     [NEW] LLMSkillProvider（包装 internal/skill.LLMSkill）
  │   ├── code_provider.go    [NEW] CodeSkillProvider（包装 internal/skill.CodeSkill）
  │   ├── mcp_provider.go     [NEW] MCPSkillProvider（复用 mcp.MCPSkillAdapter）
  │   └── registry_adapter.go [NEW] RegistryAdapter（包装 orchestrator.Registry）
  ├── audit.go                [NEW] 结构化审计日志
  ├── gateway_test.go         [NEW] 集成测试
  ├── atomic_test.go          [NEW] 原子执行单元测试
  ├── circuit_breaker_test.go [NEW] 熔断器单元测试
  ├── pipeline_test.go        [NEW] Pipeline 单元测试
  └── provider_test.go        [NEW] Provider 注册单元测试

  pkg/observability/
  └── prometheus.go           [MODIFY] 为 skill_executions_total 添加 skill_type label；
                                       新增 skill_circuit_breaker_state gauge

  ---

## 3. Data Model

### 3.1 核心类型定义

  ```go
  // types.go

  type SkillRequest struct {
      TraceID  string         // 若为空则自动生成 UUID
      SkillID  string         // 必填
      Input    any            // skill 输入，由 provider 解析
      Timeout  time.Duration  // 0 表示使用默认值 30s
      Retry    RetryConfig
      Metadata map[string]string
  }

  type RetryConfig struct {
      MaxAttempts int           // 0 = 不重试
      BaseDelay   time.Duration // 指数退避基础延迟，默认 100ms
  }

  type SkillResponse struct {
      TraceID   string
      SkillID   string
      Output    any
      Duration  time.Duration
      Metadata  map[string]string
  }

  type SkillError struct {
      Code    ErrorCode
      Message string
      TraceID string
      Cause   error
  }

  func (e *SkillError) Error() string

  type ErrorCode int

  const (
      ErrSkillNotFound      ErrorCode = 4001
      ErrSkillTimeout       ErrorCode = 4002
      ErrSkillExecFailed    ErrorCode = 5001
      ErrCircuitOpen        ErrorCode = 5002
      ErrPipelineStepFailed ErrorCode = 5003
      ErrSkillAlreadyExists ErrorCode = 4003
  )

  3.2 Pipeline 类型

  // pipeline.go

  type StepType string

  const (
      StepSequential StepType = "sequential"
      StepConditional StepType = "conditional"
      StepParallel   StepType = "parallel"
  )

  type Step struct {
      Name      string
      Type      StepType
      SkillID   string
      Input     any            // 支持字符串模板引用 $steps.<name>.output
      Condition func(StepContext) bool
      Then      *Step
      Else      *Step
      Parallel  []*Step
  }

  type Pipeline struct {
      ID    string
      Steps []*Step
  }

  type StepContext map[string]any  // key: "$steps.<name>.output"

  type PipelineResult struct {
      PipelineID string
      TraceID    string
      Steps      []StepResult
      Duration   time.Duration
  }

  type StepResult struct {
      Name     string
      Output   any
      Error    error
      Duration time.Duration
  }

  type PipelineError struct {
      *SkillError
      FailedStep string
      StepIndex  int
  }

  3.3 Provider 接口

  // provider.go

  type SkillProvider interface {
      // SkillIDs 返回该 provider 管理的所有 skill ID
      SkillIDs() []string
      // Execute 执行指定 skill
      Execute(ctx context.Context, skillID string, input any) (any, error)
      // Has 检查是否管理该 skill
      Has(skillID string) bool
  }

  3.4 熔断器状态

  // circuit_breaker.go

  type CBState int

  const (
      CBClosed   CBState = iota // 正常，允许请求
      CBOpen                    // 熔断，拒绝请求
      CBHalfOpen                // 半开，允许一个探测请求
  )

  type CircuitBreakerConfig struct {
      FailureThreshold int           // 连续失败次数触发熔断，默认 5
      RecoveryTimeout  time.Duration // 熔断后恢复等待时间，默认 30s
  }

  type breaker struct {
      state          CBState
      failures       int
      lastFailureAt  time.Time
      mu             sync.Mutex
  }

  type CircuitBreakerManager struct {
      breakers map[string]*breaker
      config   CircuitBreakerConfig
      metrics  *observability.PrometheusMetrics
      logger   *zap.Logger
      mu       sync.RWMutex
  }

  ---
  4. API Design（Go 包接口）
  
  本模块为对内 Go 包，无 HTTP 端点变更。

  4.1 SkillGateway 接口

  type SkillGateway interface {
      // Execute 原子执行单个 skill
      Execute(ctx context.Context, req SkillRequest) (SkillResponse, error)
      // ExecutePipeline 执行多步骤 pipeline
      ExecutePipeline(ctx context.Context, pipeline Pipeline) (PipelineResult, error)
      // RegisterProvider 注册 skill provider
      RegisterProvider(provider SkillProvider) error
  }

  // NewDefaultGateway 构造函数
  func NewDefaultGateway(
      metrics *observability.PrometheusMetrics,
      logger *zap.Logger,
      cbConfig *CircuitBreakerConfig, // nil 使用默认值
  ) *DefaultGateway

  4.2 PipelineBuilder DSL

  builder := skillgateway.NewPipelineBuilder("pipeline-id").
      Step("step1", "skill:llm:summarize", input1).
      Step("step2", "skill:code:format", "$steps.step1.output").
      If(func(ctx StepContext) bool {
          return ctx["$steps.step2.output"] != nil
      }).
      Then(
          NewPipelineBuilder("").Step("step3a", "skill:mcp:save", "$steps.step2.output"),
      ).
      Else(
          NewPipelineBuilder("").Step("step3b", "skill:mcp:log", "failed"),
      ).
      Parallel(
          NewPipelineBuilder("").Step("p1", "skill:llm:translate", "$steps.step1.output"),
          NewPipelineBuilder("").Step("p2", "skill:llm:classify", "$steps.step1.output"),
      ).
      Build()

  4.3 错误处理约定

  所有错误均为 *SkillError，调用方通过 errors.As 提取：

  resp, err := gateway.Execute(ctx, req)
  if err != nil {
      var skillErr *skillgateway.SkillError
      if errors.As(err, &skillErr) {
          switch skillErr.Code {
          case skillgateway.ErrCircuitOpen:
              // 熔断，降级处理
          case skillgateway.ErrSkillTimeout:
              // 超时，记录并返回
          }
      }
  }

  4.4 Breaking Changes

  - pkg/observability.PrometheusMetrics.IncSkillExecution 签名变更：新增 skillType string 参数（第二个参数），现有调用方需更新
  - pkg/observability.PrometheusMetrics 新增 skill_circuit_breaker_state gauge，无破坏性

  ---
  5. Business Logic
  
  5.1 原子执行算法

  func AtomicEngine.execute(ctx, req):
      1. trace_id = req.TraceID || uuid.New()
      2. ctx = context.WithValue(ctx, traceIDKey, trace_id)
      3. skill_type = providerRegistry.TypeOf(req.SkillID)
      4. if !circuitBreaker.Allow(req.SkillID):
             return SkillError{ErrCircuitOpen, trace_id}
      5. attempts = 0
      6. loop:
             attempts++
             ctx_with_timeout, cancel = context.WithTimeout(ctx, req.Timeout || 30s)
             defer cancel()
             output, err = providerRegistry.Execute(ctx_with_timeout, req.SkillID, req.Input)
             if err == nil:
                 circuitBreaker.RecordSuccess(req.SkillID)
                 break loop
             if isTimeout(err):
                 circuitBreaker.RecordFailure(req.SkillID)
                 return SkillError{ErrSkillTimeout, trace_id, err}
             if attempts >= req.Retry.MaxAttempts + 1:
                 circuitBreaker.RecordFailure(req.SkillID)
                 return SkillError{ErrSkillExecFailed, trace_id, err}
             sleep(req.Retry.BaseDelay * 2^(attempts-1))  // 指数退避
      7. metrics.IncSkillExecution(skillID, skill_type, "success")
      8. metrics.RecordSkillExecutionDuration(skillID, duration)
      9. auditLog(trace_id, skillID, duration, "success", caller)
      10. return SkillResponse{trace_id, skillID, output, duration}

  5.2 熔断器状态转换

  Closed:
      RecordFailure() → failures++
          if failures >= threshold: state = Open, lastFailureAt = now
      RecordSuccess() → failures = 0
      Allow() → true

  Open:
      Allow():
          if now - lastFailureAt >= recoveryTimeout:
              state = HalfOpen
              return true  // 允许一个探测请求
          return false

  HalfOpen:
      RecordSuccess() → state = Closed, failures = 0
      RecordFailure() → state = Open, lastFailureAt = now
      Allow() → false  // 探测请求已发出，等待结果

  5.3 Pipeline 步骤间上下文传递

  - 每个步骤执行后，结果写入 StepContext["$steps.<step_name>.output"]
  - $prev.output 作为语法糖，等价于上一个顺序步骤的 $steps.<prev_name>.output
  - Input 中的字符串若以 $steps. 或 $prev. 开头，在执行前通过 resolveInput(input, ctx) 替换为实际值
  - 非字符串类型的 Input 直接传递，不做模板替换

  5.4 Edge Cases

  ┌───────────────────────────────────────────┬───────────────────────────────────────────────────────────────────────┐
  │                   场景                    │                               处理方式                                │
  ├───────────────────────────────────────────┼───────────────────────────────────────────────────────────────────────┤
  │ skill_id 不存在                           │ 立即返回 ErrSkillNotFound，不进入熔断计数                             │
  ├───────────────────────────────────────────┼───────────────────────────────────────────────────────────────────────┤
  │ 并行步骤部分失败                          │ 等待所有 goroutine 完成，收集所有错误，返回第一个失败的 PipelineError │
  ├───────────────────────────────────────────┼───────────────────────────────────────────────────────────────────────┤
  │ Pipeline 步骤引用不存在的 $steps.X.output │ resolveInput 返回空字符串，不报错（宽松模式）                         │
  ├───────────────────────────────────────────┼───────────────────────────────────────────────────────────────────────┤
  │ 重试时 context 已取消                     │ 立即停止重试，返回 context 错误包装为 ErrSkillExecFailed              │
  ├───────────────────────────────────────────┼───────────────────────────────────────────────────────────────────────┤
  │ 熔断器 HalfOpen 时并发请求                │ 只允许第一个请求通过（mutex 保护状态检查），其余返回 ErrCircuitOpen   │
  ├───────────────────────────────────────────┼───────────────────────────────────────────────────────────────────────┤
  │ RegisterProvider 注册已存在的 skill_id    │ 返回 ErrSkillAlreadyExists，不覆盖                                    │
  └───────────────────────────────────────────┴───────────────────────────────────────────────────────────────────────┘

  ---
  6. Error Handling
  
  6.1 Error Taxonomy

  ┌───────────────────────┬──────┬───────────────────────────┬──────────────────────┐
  │       ErrorCode       │  值  │         触发条件          │      是否可重试      │
  ├───────────────────────┼──────┼───────────────────────────┼──────────────────────┤
  │ ErrSkillNotFound      │ 4001 │ skill_id 未注册           │ 否                   │
  ├───────────────────────┼──────┼───────────────────────────┼──────────────────────┤
  │ ErrSkillTimeout       │ 4002 │ context deadline exceeded │ 视业务而定           │
  ├───────────────────────┼──────┼───────────────────────────┼──────────────────────┤
  │ ErrSkillAlreadyExists │ 4003 │ 重复注册同一 skill_id     │ 否                   │
  ├───────────────────────┼──────┼───────────────────────────┼──────────────────────┤
  │ ErrSkillExecFailed    │ 5001 │ provider.Execute 返回错误 │ 是（按 RetryConfig） │
  ├───────────────────────┼──────┼───────────────────────────┼──────────────────────┤
  │ ErrCircuitOpen        │ 5002 │ 熔断器处于 Open 状态      │ 否（等待恢复）       │
  ├───────────────────────┼──────┼───────────────────────────┼──────────────────────┤
  │ ErrPipelineStepFailed │ 5003 │ Pipeline 某步骤失败       │ 否（整体失败）       │
  └───────────────────────┴──────┴───────────────────────────┴──────────────────────┘

  6.2 Retry Strategy

  - 仅对 ErrSkillExecFailed 重试（非超时、非熔断、非 not found）
  - 指数退避：delay = BaseDelay * 2^(attempt-1)，BaseDelay 默认 100ms
  - 最大等待上限：单次重试间隔不超过 10s（防止退避过长）
  - 重试期间若 context 取消，立即停止

  6.3 Failure Modes

  ┌─────────────────────────────┬─────────────────────────────────────────────────┐
  │          依赖失败           │                    降级策略                     │
  ├─────────────────────────────┼─────────────────────────────────────────────────┤
  │ LLM provider 不可用         │ 熔断后返回 ErrCircuitOpen，调用方自行降级       │
  ├─────────────────────────────┼─────────────────────────────────────────────────┤
  │ MCP server 断连             │ MCPSkillProvider.Execute 返回错误，触发熔断计数 │
  ├─────────────────────────────┼─────────────────────────────────────────────────┤
  │ Prometheus metrics 注册失败 │ 启动时 panic（promauto 行为），属于配置错误     │
  ├─────────────────────────────┼─────────────────────────────────────────────────┤
  │ 审计日志写入失败            │ 仅记录 warn 日志，不影响主流程返回              │
  └─────────────────────────────┴─────────────────────────────────────────────────┘

  ---
  7. Security
  
  7.1 Authentication & Authorization

  - SkillGateway 为对内包，不暴露 HTTP，无需额外认证
  - 调用方身份通过 SkillRequest.Metadata["caller"] 传递，写入审计日志
  - 不做权限校验（权限控制在 HTTP handler 层已有）

  7.2 Input Validation

  - SkillRequest.SkillID 不能为空，否则返回 ErrSkillNotFound
  - SkillRequest.Timeout 若为负数，重置为默认值 30s
  - RetryConfig.MaxAttempts 上限为 10，超出则截断
  - Pipeline Step.Name 不能包含 .（防止 $steps. 路径歧义）

  7.3 Data Protection

  - 审计日志不记录 Input/Output 内容（可能含敏感数据），仅记录元数据
  - trace_id 使用 UUID v4，不含业务信息

  ---
  8. Performance
  
  8.1 Expected Load

  - 当前项目为单实例部署，预估 skill 调用 QPS < 100
  - Pipeline 步骤数预估 < 10 步
  - 并行步骤数预估 < 5 个

  8.2 Optimization Strategy

  - ProviderRegistry 使用 sync.RWMutex，读多写少场景性能充足
  - CircuitBreakerManager per-skill 独立 mutex，避免全局锁竞争
  - 并行步骤使用 goroutine + sync.WaitGroup，无额外 worker pool（当前规模不需要）
  - metrics 上报为异步（Prometheus client 内部缓冲），不阻塞主流程

  8.3 Gateway 自身开销目标

  - 原子执行 gateway 自身开销（不含 skill 执行时间）P99 ≤ 1ms
  - 主要开销来源：UUID 生成、mutex 操作、metrics 上报

  ---
  9. Testing Strategy
  
  9.1 Unit Tests

  ┌─────────────────────────┬──────────────────────────────────────────┬────────────────────────────────────┐
  │          文件           │                 测试重点                 │             Mock 策略              │
  ├─────────────────────────┼──────────────────────────────────────────┼────────────────────────────────────┤
  │ atomic_test.go          │ 超时、重试次数、熔断触发                 │ mock SkillProvider（返回预设错误） │
  ├─────────────────────────┼──────────────────────────────────────────┼────────────────────────────────────┤
  │ circuit_breaker_test.go │ 状态转换、并发安全、恢复时间             │ 无 mock，直接测试状态机            │
  ├─────────────────────────┼──────────────────────────────────────────┼────────────────────────────────────┤
  │ pipeline_test.go        │ 顺序/条件/并行执行、上下文传递、步骤失败 │ mock SkillProvider                 │
  ├─────────────────────────┼──────────────────────────────────────────┼────────────────────────────────────┤
  │ provider_test.go        │ 重复注册、not found、并发注册            │ 无 mock                            │
  └─────────────────────────┴──────────────────────────────────────────┴────────────────────────────────────┘

  9.2 Integration Tests

  gateway_test.go 使用真实 DefaultGateway + mock providers：
  - 完整原子执行链路（trace_id 传播、metrics 上报验证）
  - Pipeline 端到端执行
  - RegistryAdapter 与 orchestrator.Registry 集成

  9.3 Edge Case Tests

  - 熔断器 HalfOpen 并发竞争（多 goroutine 同时请求）
  - Pipeline 并行步骤全部失败
  - $steps.nonexistent.output 引用不存在步骤
  - context 在重试间隔中取消
  - RegisterProvider 并发注册相同 skill_id

  9.4 Acceptance Criteria Mapping

  ┌───────────────┬──────────────────┬─────────────┬────────────────────────────────────────────────┐
  │     US/FR     │     测试文件     │    类型     │                      描述                      │
  ├───────────────┼──────────────────┼─────────────┼────────────────────────────────────────────────┤
  │ US-001        │ gateway_test.go  │ unit        │ 接口编译验证，SkillRequest/Response 字段完整性 │
  ├───────────────┼──────────────────┼─────────────┼────────────────────────────────────────────────┤
  │ US-002 / FR-3 │ atomic_test.go   │ unit        │ trace_id 自动生成和传播                        │
  ├───────────────┼──────────────────┼─────────────┼────────────────────────────────────────────────┤
  │ US-002 / FR-4 │ gateway_test.go         │ integration │ metrics counter 在执行后递增                   │
  ├───────────────┼─────────────────────────┼─────────────┼────────────────────────────────────────────────┤
  │ US-002        │ atomic_test.go          │ unit        │ 超时返回 ErrSkillTimeout                       │
  ├───────────────┼─────────────────────────┼─────────────┼────────────────────────────────────────────────┤
  │ US-002        │ atomic_test.go          │ unit        │ 重试 N 次后返回 ErrSkillExecFailed             │
  ├───────────────┼─────────────────────────┼─────────────┼────────────────────────────────────────────────┤
  │ US-003 / FR-8 │ pipeline_test.go        │ unit        │ 顺序/条件/并行三种步骤类型                     │
  ├───────────────┼─────────────────────────┼─────────────┼────────────────────────────────────────────────┤
  │ US-003 / FR-9 │ pipeline_test.go        │ unit        │ $steps.X.output 和 $prev.output 引用           │
  ├───────────────┼─────────────────────────┼─────────────┼────────────────────────────────────────────────┤
  │ US-004 / FR-6 │ provider_test.go        │ unit        │ RegisterProvider 成功和重复注册错误            │
  ├───────────────┼─────────────────────────┼─────────────┼────────────────────────────────────────────────┤
  │ US-004        │ provider_test.go        │ unit        │ RegistryAdapter 包装 orchestrator.Registry     │
  ├───────────────┼─────────────────────────┼─────────────┼────────────────────────────────────────────────┤
  │ US-005 / FR-7 │ circuit_breaker_test.go │ unit        │ 5次失败触发熔断，30s后半开                     │
  ├───────────────┼─────────────────────────┼─────────────┼────────────────────────────────────────────────┤
  │ US-005        │ circuit_breaker_test.go │ unit        │ 熔断状态变更写入日志                           │
  ├───────────────┼─────────────────────────┼─────────────┼────────────────────────────────────────────────┤
  │ FR-10         │ atomic_test.go          │ unit        │ 所有错误为 *SkillError 类型含 trace_id         │
  ├───────────────┼─────────────────────────┼─────────────┼────────────────────────────────────────────────┤
  │ FR-11         │ gateway_test.go         │ integration │ 审计日志包含必要字段                           │
  └───────────────┴─────────────────────────┴─────────────┴────────────────────────────────────────────────┘

  ---
  10. Implementation Plan
  
  10.1 Phases

  Phase 1 — 基础骨架（US-001）
  1. types.go：定义所有类型和错误码
  2. provider.go：SkillProvider 接口 + ProviderRegistry
  3. gateway.go：SkillGateway 接口 + DefaultGateway 空实现

  Phase 2 — 原子执行（US-002, US-005）
  4. circuit_breaker.go：熔断器状态机
  5. audit.go：结构化审计日志
  6. atomic.go：AtomicEngine（超时/重试/熔断/metrics/audit）
  7. pkg/observability/prometheus.go：添加 skill_type label 和 skill_circuit_breaker_state

  Phase 3 — Pipeline 编排（US-003）
  8. pipeline.go：PipelineEngine + 类型定义
  9. pipeline_builder.go：PipelineBuilder DSL

  Phase 4 — Provider 适配（US-004）
  10. providers/registry_adapter.go：包装 orchestrator.Registry
  11. providers/mcp_provider.go：包装 mcp.MCPSkillAdapter
  12. providers/llm_provider.go：包装 internal/skill.LLMSkill
  13. providers/code_provider.go：包装 internal/skill.CodeSkill

  Phase 5 — 测试
  14. 各模块单元测试
  15. gateway_test.go 集成测试

  10.2 Issue Mapping

  | Issue | SPEC Sections | Priority | Depends On |
  |---|---|---|---|
  | #1 基础类型和接口 | 3.1, 3.3, 4.1 | high | — |
  | #2 原子执行引擎 | 5.1, 6.1, 6.2, 8.3 | high | #1, #3, #4 |
  | #3 熔断器 | 3.4, 5.2, 6.3 | high | #1 |
  | #4 Prometheus 扩展 | 4.4, 8.1 | medium | #1 |
  | #5 Pipeline 引擎 | 3.2, 4.2, 5.3, 5.4 | medium | #2 |
  | #6 Provider 适配层 | 2.2, 2.4 (providers/) | medium | #1 |
  | #7 测试套件 | 9.1, 9.2, 9.3, 9.4 | medium | #2, #3, #5, #6 |

  10.3 Incremental Delivery

  - Phase 1 (#1) 完成后：`go build ./internal/skillgateway/...` 通过，接口契约确定，其他模块可并行开发
  - Phase 2 (#2, #3, #4) 完成后：原子执行可用，可接入现有 `skill_handler.go` 做灰度验证
  - Phase 3 (#5) 完成后：Pipeline 能力可用，agent 模块可开始集成
  - Phase 4 (#6) 完成后：现有 `orchestrator.Registry` 和 `MCPSkillAdapter` 通过适配器接入，`skill_handler.go` 零修改验证
  - Phase 5 (#7) 完成后：覆盖率达标（≥80%），CI 通过，可合并主干

  ---
  11. Open Questions & Risks

  11.1 Unresolved Questions

  - skill 版本管理：同一 skill_id 多版本共存是否需要？当前设计不支持，需产品确认
  - Pipeline 持久化：Pipeline 定义是否需要存入数据库以支持复用？当前仅内存 DSL
  - 熔断参数动态配置：FailureThreshold 和 RecoveryTimeout 是否需要运行时热更新？当前为构造时固定
  - CodeSkill 实际执行：`CodeSkillProvider` 当前为 placeholder，本期不实现真实代码执行

  11.2 Technical Risks

  | Risk | Impact | Mitigation |
  |---|---|---|
  | `IncSkillExecution` 签名变更导致编译失败 | high | Phase 4 中统一修改所有调用方，CI 强制验证 |
  | 熔断器 HalfOpen 并发竞争导致多个探测请求 | medium | per-breaker mutex 保护状态检查，单元测试覆盖并发场景 |
  | Pipeline 步骤数量过多导致 goroutine 泄漏 | low | 并行步骤数上限 20（可配置），超出返回错误 |
  | Prometheus 重复注册 panic | high | 使用 `promauto` 包（注册一次），或在 `NewDefaultGateway` 中用 `sync.Once` 保护 |

  11.3 Assumptions

  - 项目 OTel tracing 为 stub（已验证 `internal/observability/trace.go`），本期不引入真实 span，用 UUID trace_id 替代
  - `pkg/observability.PrometheusMetrics` 为单例，`NewDefaultGateway` 接收注入而非自行创建
  - `orchestrator.Registry` 的 `Get` 方法返回 `skill.Skill`，`RegistryAdapter` 需要类型断言为 `skill.SkillExecutor` 才能调用 `Execute`
  - 审计日志使用项目已有的 `*zap.Logger`，不引入独立日志后端
