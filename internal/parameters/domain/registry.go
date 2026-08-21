package domain

import (
	"fmt"
	"sort"
	"strings"

	"github.com/byteBuilderX/stratum/pkg/constants"
)

// ParametersRegistry is the code-level central registry of all optimizable /
// tunable parameters. Built at startup from built-in definitions; the
// evaluation loop and the frontend schema both read from it, replacing the
// legacy hard-coded whitelists.
type ParametersRegistry struct {
	byKey          map[string]*ParameterDefinition
	byEvalKey      map[string]string   // evaluation bare-name → registry key
	promptEvalKeys map[string]struct{} // gate-only prompt patch keys (no definition)
	resourceKeys   []string            // sorted resource-scope keys
}

// NewParametersRegistry builds a registry with all built-in definitions.
func NewParametersRegistry() *ParametersRegistry {
	r := &ParametersRegistry{
		byKey:          make(map[string]*ParameterDefinition, 40),
		byEvalKey:      make(map[string]string, 24),
		promptEvalKeys: make(map[string]struct{}, 6),
	}
	r.registerAgentParams()
	r.registerRAGParams()
	r.registerMCPParams()
	r.registerOptimizerParams()
	r.registerJudgeParams()
	r.registerFactCheckParams()
	r.registerTraceParams()
	r.registerMemoryParams()
	r.registerMemoryWorkerParams()
	r.registerPromptEvaluationKeys()
	r.resourceKeys = r.sortedScopeKeys(ScopeResource)
	return r
}

// Register adds a definition. Duplicate keys are an error (unlike the legacy
// last-write-wins registry) so definition drift fails fast.
func (r *ParametersRegistry) Register(def ParameterDefinition) error {
	if def.Key == "" {
		return fmt.Errorf("parameter registry: empty key")
	}
	if _, exists := r.byKey[def.Key]; exists {
		return fmt.Errorf("parameter registry: duplicate key %s", def.Key)
	}
	for _, evalKey := range def.EvaluationKeys {
		if prev, exists := r.byEvalKey[evalKey]; exists && prev != def.Key {
			return fmt.Errorf("parameter registry: evaluation key %q already mapped to %s", evalKey, prev)
		}
		if _, reserved := r.promptEvalKeys[evalKey]; reserved {
			return fmt.Errorf("parameter registry: evaluation key %q reserved as gate-only prompt key", evalKey)
		}
		r.byEvalKey[evalKey] = def.Key
	}
	r.byKey[def.Key] = &def
	return nil
}

// Get returns the definition for a registry key.
func (r *ParametersRegistry) Get(key string) (*ParameterDefinition, bool) {
	def, ok := r.byKey[key]
	return def, ok
}

// IsEvaluationKey reports whether a bare evaluation key (e.g. "temperature")
// is registered — the legacy whitelists are replaced by this check. Prompt
// patch keys (instructions, system_prompt, ...) are gate-only: valid candidate
// patch fields, but with no parameter definition behind them.
func (r *ParametersRegistry) IsEvaluationKey(bareKey string) bool {
	if _, ok := r.byEvalKey[bareKey]; ok {
		return true
	}
	_, ok := r.promptEvalKeys[bareKey]
	return ok
}

// KeyForEvaluation resolves a bare evaluation key to its registry key.
func (r *ParametersRegistry) KeyForEvaluation(bareKey string) (string, bool) {
	key, ok := r.byEvalKey[bareKey]
	return key, ok
}

// KeyByShortName resolves a bare key to a registry key by matching the last
// dotted segment (e.g. "compaction_temperature" → "agent.compaction_temperature").
// Used by ValidateResourceValues for resource params that must not carry an
// EvaluationKeys alias (would pollute the evaluation search space and the
// candidate/critique whitelist lockstep).
func (r *ParametersRegistry) KeyByShortName(bareKey string) (string, bool) {
	for key := range r.byKey {
		lastDot := strings.LastIndex(key, ".")
		if key[lastDot+1:] == bareKey {
			return key, true
		}
	}
	return "", false
}

// EvaluationKeys returns every registered bare evaluation key, including the
// gate-only prompt patch keys. Candidate/critique whitelists pin themselves to
// this set.
func (r *ParametersRegistry) EvaluationKeys() []string {
	seen := make(map[string]struct{}, len(r.byEvalKey)+len(r.promptEvalKeys))
	for k := range r.byEvalKey {
		seen[k] = struct{}{}
	}
	for k := range r.promptEvalKeys {
		seen[k] = struct{}{}
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// Schema returns all definitions sorted by scope then key for stable output.
func (r *ParametersRegistry) Schema() []ParameterDefinition {
	defs := make([]ParameterDefinition, 0, len(r.byKey))
	for _, def := range r.byKey {
		defs = append(defs, *def)
	}
	return sortedDefs(defs)
}

// ForScope returns definitions belonging to one scope.
func (r *ParametersRegistry) ForScope(scope Scope) []ParameterDefinition {
	defs := make([]ParameterDefinition, 0, len(r.byKey))
	for _, def := range r.byKey {
		if def.Scope == scope {
			defs = append(defs, *def)
		}
	}
	return sortedDefs(defs)
}

// ResourceKeys returns the sorted resource-scope registry keys.
func (r *ParametersRegistry) ResourceKeys() []string {
	out := make([]string, len(r.resourceKeys))
	copy(out, r.resourceKeys)
	return out
}

func (r *ParametersRegistry) sortedScopeKeys(scope Scope) []string {
	var keys []string
	for key, def := range r.byKey {
		if def.Scope == scope {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

// ——— built-in registrations (each under 120 lines) ———

// registerAgentParams covers agent.temperature / max_tokens /
// max_context_tokens / max_iterations / model / compaction×2 / bindings.
// 0 = unset semantics: explicit 0 equals absent (omitempty JSON), the
// gateway/provider default applies.
func (r *ParametersRegistry) registerAgentParams() {
	f := func(v float64) *float64 { return &v }
	for _, def := range []ParameterDefinition{
		{
			Key: "agent.temperature", Scope: ScopeResource, Category: "agent",
			DisplayName: "温度", Description: "控制输出随机性:值越低越确定保守,值越高越发散多样;范围 0~1,0 表示不设置(网关/provider 默认生效,通常 0.7)",
			ValueType: TypeFloat, Default: 0.7,
			VisualHint:     VisualHint{Control: ControlSlider, Min: f(0), Max: f(1), Step: f(0.1)},
			Optimizable:    true,
			EvaluationKeys: []string{"temperature"},
		},
		{
			Key: "agent.reasoning_effort", Scope: ScopeResource, Category: "agent",
			DisplayName: "思考强度", Description: "推理深度档位:low/medium/high,空表示不设置(平台默认)",
			ValueType: TypeString, Default: "",
			VisualHint:     VisualHint{Control: ControlSelect, Options: []any{"low", "medium", "high"}},
			Optimizable:    true,
			EvaluationKeys: []string{"reasoning_effort"},
		},
		{
			Key: "agent.max_tokens", Scope: ScopeResource, Category: "agent",
			DisplayName: "最大输出 Token 数", Description: "单次 LLM 输出上限,0 表示不限制",
			ValueType: TypeInt, Default: int64(0),
			VisualHint:     VisualHint{Control: ControlSlider, Min: f(0), Max: f(131072), Step: f(256), Unit: "tokens"},
			Optimizable:    true,
			EvaluationKeys: []string{"max_tokens", "maxTokens"},
		},
		{
			Key: "agent.max_context_tokens", Scope: ScopeResource, Category: "agent",
			DisplayName: "最大上下文 Token 数", Description: "上下文窗口上限,0 表示不设置;顶层列权威(agents.max_context_tokens)",
			ValueType: TypeInt, Default: int64(0),
			VisualHint:     VisualHint{Control: ControlSlider, Min: f(0), Max: f(32768), Step: f(512), Unit: "tokens"},
			Optimizable:    true,
			EvaluationKeys: []string{"max_context_tokens"},
		},
		{
			Key: "agent.max_iterations", Scope: ScopeResource, Category: "agent",
			DisplayName: "最大迭代轮数", Description: "执行循环上限;顶层列权威(agents.max_iterations)",
			ValueType: TypeInt, Default: int64(10),
			VisualHint:     VisualHint{Control: ControlSlider, Min: f(1), Max: f(90), Step: f(1)},
			Optimizable:    true,
			EvaluationKeys: []string{"max_iterations"},
		},
		{
			Key: "agent.model", Scope: ScopeResource, Category: "agent",
			DisplayName: "模型", Description: "LLM 模型选择;顶层列权威(agents.llm_model)",
			ValueType: TypeString, Default: "",
			VisualHint:     VisualHint{Control: ControlSelect},
			Optimizable:    true,
			EvaluationKeys: []string{"model"},
		},
		{
			Key: "agent.compaction_recent_groups", Scope: ScopeResource, Category: "agent",
			DisplayName: "压缩最近组数", Description: "循环内压缩的 recent groups,0 表示从 MaxContextTokens 自动推导",
			ValueType: TypeInt, Default: int64(0),
			VisualHint:  VisualHint{Control: ControlSelect, Options: []any{int64(0), int64(2), int64(3), int64(5)}},
			Optimizable: true, EvaluationKeys: []string{"compaction_recent_groups"},
		},
		{
			Key: "agent.compaction_cooldown_sec", Scope: ScopeResource, Category: "agent",
			DisplayName: "压缩冷却(秒)", Description: "压缩触发后的冷却窗口,0 表示默认常量",
			ValueType: TypeInt, Default: int64(0),
			VisualHint:  VisualHint{Control: ControlSlider, Min: f(0), Max: f(120), Step: f(5), Unit: "s"},
			Optimizable: false,
		},
		// 压缩 prompt/temperature/model 三值:运行时从 AgentConfig 顶层字段直读
		// (resolveEffectiveParameters 晚于 compactor 构造),此处仅登记 schema/
		// 搜索空间。三 key 均不设 EvaluationKeys —— compaction_prompt 是 gate-only
		// 保留 key(Register() 会拒绝);compaction_temperature 的 bare-key 写时越界
		// 校验经 ValidateResourceValues 的 registry-key 短名匹配(短名匹配不进入
		// byEvalKey,避免污染 candidate/critique whitelist lockstep)。
		{
			Key: "agent.compaction_prompt", Scope: ScopeResource, Category: "agent",
			DisplayName: "压缩提示词", Description: "上下文压缩的系统提示词,空表示默认模板",
			ValueType: TypeString, Default: "",
			VisualHint:  VisualHint{Control: ControlTextarea},
			Optimizable: false,
		},
		{
			Key: "agent.compaction_temperature", Scope: ScopeResource, Category: "agent",
			DisplayName: "压缩温度", Description: "上下文压缩采样温度,范围 0~1,0 表示默认常量",
			ValueType: TypeFloat, Default: 0.0,
			VisualHint:  VisualHint{Control: ControlSlider, Min: f(0), Max: f(1), Step: f(0.1)},
			Optimizable: false,
		},
		{
			Key: "agent.compaction_model", Scope: ScopeResource, Category: "agent",
			DisplayName: "压缩模型", Description: "上下文压缩使用的独立模型,空表示跟随主模型",
			ValueType: TypeString, Default: "",
			VisualHint:  VisualHint{Control: ControlSelect},
			Optimizable: false,
		},
		{
			Key: "agent.max_tokens_per_execution", Scope: ScopeResource, Category: "agent",
			DisplayName: "单次执行 Token 预算", Description: "本次执行累计 LLM token 上限,0 表示不设限",
			ValueType: TypeInt, Default: int64(0),
			VisualHint:  VisualHint{Control: ControlSlider, Min: f(0), Max: f(2000000), Step: f(10000), Unit: "tokens"},
			Optimizable: false,
		},
		{
			Key: "agent.bindings", Scope: ScopeResource, Category: "agent",
			DisplayName: "绑定", Description: "agent 绑定关系(复杂结构,由 evaluation adapter 校验,仅登记兼容)",
			ValueType: TypeString, Default: nil,
			VisualHint:  VisualHint{Control: ControlTextarea},
			Optimizable: true, EvaluationKeys: []string{"bindings"},
			ValidateFn: func(any) error { return nil },
		},
	} {
		_ = r.Register(def)
	}
}

// registerRAGParams covers rag.top_k / score_threshold / reranking /
// query_rewrite. query_rewrite has no production effect point (WorkspaceConfig
// has no such field) — registered for search-space compatibility only.
func (r *ParametersRegistry) registerRAGParams() {
	f := func(v float64) *float64 { return &v }
	for _, def := range []ParameterDefinition{
		{
			Key: "rag.top_k", Scope: ScopeResource, Category: "rag",
			DisplayName: "检索 Top-K", Description: "知识库检索返回条数",
			ValueType: TypeInt, Default: int64(10),
			VisualHint:  VisualHint{Control: ControlSlider, Min: f(1), Max: f(50), Step: f(1)},
			Optimizable: true, EvaluationKeys: []string{"top_k"},
		},
		{
			Key: "rag.score_threshold", Scope: ScopeResource, Category: "rag",
			DisplayName: "相似度阈值", Description: "低于阈值的检索结果被过滤;前端无法回写 0(后端仅 >0 生效)",
			ValueType: TypeFloat, Default: 0.3,
			VisualHint:  VisualHint{Control: ControlSlider, Min: f(0), Max: f(1), Step: f(0.05)},
			Optimizable: true, EvaluationKeys: []string{"score_threshold"},
		},
		{
			Key: "rag.reranking", Scope: ScopeResource, Category: "rag",
			DisplayName: "重排序", Description: "是否对检索结果重排序",
			ValueType: TypeBool, Default: false,
			VisualHint:  VisualHint{Control: ControlToggle},
			Optimizable: true, EvaluationKeys: []string{"reranking"},
		},
		{
			Key: "rag.query_rewrite", Scope: ScopeResource, Category: "rag",
			DisplayName: "查询改写", Description: "查询改写(无生产生效点,仅评测检索快照概念,登记兼容)",
			ValueType: TypeBool, Default: false,
			VisualHint:  VisualHint{Control: ControlToggle},
			Optimizable: true, EvaluationKeys: []string{"query_rewrite"},
		},
	} {
		_ = r.Register(def)
	}
}

// registerMCPParams covers the MCP evolution loop's only optimizable
// dimensions. Effect point is the per-server MCP config (already exposed at
// /mcp); registered here so the MCP search space survives the whitelist
// unification.
func (r *ParametersRegistry) registerMCPParams() {
	for _, def := range []ParameterDefinition{
		{
			Key: "mcp.enabled_tools", Scope: ScopeResource, Category: "mcp",
			DisplayName: "启用工具", Description: "MCP server 启用的工具列表(复杂结构,由 evaluation MCP adapter 校验)",
			ValueType: TypeString, Default: nil,
			VisualHint:  VisualHint{Control: ControlTextarea},
			Optimizable: true, EvaluationKeys: []string{"enabled_tools"},
			ValidateFn: func(any) error { return nil },
		},
		{
			Key: "mcp.timeout_ms", Scope: ScopeResource, Category: "mcp",
			DisplayName: "超时(ms)", Description: "MCP 工具调用超时",
			ValueType: TypeInt, Default: int64(30000),
			VisualHint:  VisualHint{Control: ControlSlider, Min: fp(1000), Max: fp(120000), Step: fp(1000), Unit: "ms"},
			Optimizable: true, EvaluationKeys: []string{"timeout_ms"},
		},
		{
			Key: "mcp.max_retries", Scope: ScopeResource, Category: "mcp",
			DisplayName: "最大重试", Description: "MCP 工具调用最大重试次数",
			ValueType: TypeInt, Default: int64(2),
			VisualHint:  VisualHint{Control: ControlSlider, Min: fp(0), Max: fp(10), Step: fp(1)},
			Optimizable: true, EvaluationKeys: []string{"max_retries"},
		},
	} {
		_ = r.Register(def)
	}
}

// registerOptimizerParams are the platform-level LLM optimizer defaults.
// 模型默认值必须为空：代码内不写死兜底模型，空模型交由 llmgateway 从模型
// 目录解析默认；配置的模型失效时由 llmgateway fail-closed + 告警。
func (r *ParametersRegistry) registerOptimizerParams() {
	f := func(v float64) *float64 { return &v }
	for _, def := range []ParameterDefinition{
		{
			Key: "evaluation.optimizer.model", Scope: ScopePlatform, Category: "evaluation",
			DisplayName: "优化器模型", Description: "提示词/参数优化器使用的 LLM 模型",
			ValueType: TypeString, Default: "",
			VisualHint:  VisualHint{Control: ControlModel},
			Optimizable: true,
		},
		{
			Key: "evaluation.optimizer.temperature", Scope: ScopePlatform, Category: "evaluation",
			DisplayName: "优化器温度", Description: "优化器采样温度",
			ValueType: TypeFloat, Default: 0.2,
			VisualHint:  VisualHint{Control: ControlSlider, Min: f(0), Max: f(1), Step: f(0.1)},
			Optimizable: true,
		},
		{
			Key: "evaluation.optimizer.max_tokens", Scope: ScopePlatform, Category: "evaluation",
			DisplayName: "优化器最大 Token 数", Description: "优化器输出上限",
			ValueType: TypeInt, Default: int64(2048),
			VisualHint:  VisualHint{Control: ControlSlider, Min: f(256), Max: f(8192), Step: f(256), Unit: "tokens"},
			Optimizable: true,
		},
	} {
		_ = r.Register(def)
	}
}

// registerJudgeParams are Phase 3 LLM judge placeholders (enabled 默认 false,
// golden 校准 ≥90% 后才启用).
func (r *ParametersRegistry) registerJudgeParams() {
	f := func(v float64) *float64 { return &v }
	for _, def := range []ParameterDefinition{
		{
			Key: "evaluation.judge.model", Scope: ScopePlatform, Category: "evaluation",
			DisplayName: "评测法官模型", Description: "LLM judge 使用的模型(Phase 3,预留)",
			ValueType: TypeString, Default: "",
			VisualHint:  VisualHint{Control: ControlModel},
			Optimizable: true,
		},
		{
			Key: "evaluation.judge.temperature", Scope: ScopePlatform, Category: "evaluation",
			DisplayName: "评测法官温度", Description: "LLM judge 采样温度(Phase 3,预留)",
			ValueType: TypeFloat, Default: 0.0,
			VisualHint:  VisualHint{Control: ControlSlider, Min: f(0), Max: f(1), Step: f(0.1)},
			Optimizable: true,
		},
		{
			Key: "evaluation.judge.enabled", Scope: ScopePlatform, Category: "evaluation",
			DisplayName: "启用 LLM 评测法官", Description: "是否启用 LLM judge 断言(默认关,校准一致率 ≥90% 后才开启)",
			ValueType: TypeBool, Default: false,
			VisualHint:  VisualHint{Control: ControlToggle},
			Optimizable: true,
		},
	} {
		_ = r.Register(def)
	}
}

// registerFactCheckParams 是 agent 输出幻觉校验的平台级参数（factcheck /
// LLM-as-Judge）。默认全部关闭/空：平台未配置时 collectGraphResult 不校验。
// model 和 prompt 空 = 禁用，不兜底。
func (r *ParametersRegistry) registerFactCheckParams() {
	f := func(v float64) *float64 { return &v }
	_ = r.Register(ParameterDefinition{
		Key: "agent.factcheck.enabled", Scope: ScopePlatform, Category: "agent",
		DisplayName: "启用 Agent 输出幻觉校验", Description: "是否启用 LLM-as-Judge 事后幻觉校验(advisory,只展示不阻断)",
		ValueType: TypeBool, Default: false,
		VisualHint:  VisualHint{Control: ControlToggle},
		Optimizable: true,
	})
	_ = r.Register(ParameterDefinition{
		Key: "agent.factcheck.judge.model", Scope: ScopePlatform, Category: "agent",
		DisplayName: "幻觉校验 Judge 模型", Description: "LLM-as-Judge 使用的模型;空 = 禁用",
		ValueType: TypeString, Default: "",
		VisualHint:  VisualHint{Control: ControlModel},
		Optimizable: true,
	})
	_ = r.Register(ParameterDefinition{
		Key: "agent.factcheck.judge.prompt", Scope: ScopePlatform, Category: "agent",
		DisplayName: "幻觉校验 Judge 提示词", Description: "Judge 的系统提示词(纯规则,无占位符);空 = 禁用",
		ValueType: TypeString, Default: "",
		VisualHint:  VisualHint{Control: ControlTextarea},
		Optimizable: true,
		Sensitive:   true,
	})
	_ = r.Register(ParameterDefinition{
		Key: "agent.factcheck.top_k", Scope: ScopePlatform, Category: "agent",
		DisplayName: "幻觉校验证据检索 TopK", Description: "每个 claim 的 RAG 检索 topK;0 = 使用代码常量默认",
		ValueType: TypeInt, Default: int64(0),
		VisualHint:  VisualHint{Control: ControlSlider, Min: f(0), Max: f(20), Step: f(1)},
		Optimizable: true,
	})
	_ = r.Register(ParameterDefinition{
		Key: "agent.factcheck.max_claims", Scope: ScopePlatform, Category: "agent",
		DisplayName: "幻觉校验最大 Claim 数", Description: "最多拆分的 claim 数(控成本);0 = 使用代码常量默认",
		ValueType: TypeInt, Default: int64(0),
		VisualHint:  VisualHint{Control: ControlSlider, Min: f(0), Max: f(20), Step: f(1)},
		Optimizable: true,
	})
}

// registerTraceParams is the Phase 2 trace capture switch.
func (r *ParametersRegistry) registerTraceParams() {
	_ = r.Register(ParameterDefinition{
		Key: "trace.capture_parameters", Scope: ScopePlatform, Category: "trace",
		DisplayName: "Trace 记录参数值", Description: "执行 trace 是否携带参数实际值(默认关,哈希指纹恒记录)",
		ValueType: TypeBool, Default: false,
		VisualHint:  VisualHint{Control: ControlToggle},
		Optimizable: true,
	})
}

// registerMemoryParams covers the memory pipeline constants that are currently
// hard-coded in pkg/constants/memory.go. Resource-scope: bound to the agent
// resource (agents.parameters JSONB), per-agent tuned. recall_top_k / long_term_top_k
// have no runtime consumer (recall limit comes from the tool request) — registered
// for search-space compatibility only, same as rag.query_rewrite.
func (r *ParametersRegistry) registerMemoryParams() {
	f := func(v float64) *float64 { return &v }
	for _, def := range []ParameterDefinition{
		{
			Key: "memory.recall_top_k", Scope: ScopeResource, Category: "memory",
			DisplayName: "记忆召回 Top-K", Description: "记忆召回返回条数",
			// 默认 5 对齐运行时兜底(recall_tool Handle 非法 limit 回退)、
			// Max 20 对齐工具上限;接入消费点后未配置 agent 走本默认,无行为漂移。
			ValueType: TypeInt, Default: int64(5),
			VisualHint:  VisualHint{Control: ControlSlider, Min: f(1), Max: f(20), Step: f(1)},
			Optimizable: true,
		},
		{
			Key: "memory.fact_injection_top_n", Scope: ScopeResource, Category: "memory",
			DisplayName: "事实注入条数", Description: "会话上下文注入的抽取事实条数",
			ValueType: TypeInt, Default: int64(8),
			VisualHint:  VisualHint{Control: ControlSlider, Min: f(1), Max: f(20), Step: f(1)},
			Optimizable: true,
		},
		{
			Key: "memory.history_injection_top_n", Scope: ScopeResource, Category: "memory",
			DisplayName: "历史注入条数", Description: "会话上下文注入的历史消息条数",
			ValueType: TypeInt, Default: int64(3),
			VisualHint:  VisualHint{Control: ControlSlider, Min: f(0), Max: f(10), Step: f(1)},
			Optimizable: true,
		},
		{
			// Deprecated: 与 memory.recall_top_k 语义重复,保留注册仅兼容存量
			// agents.parameters 残留 key(删除会破坏 promote 路径的
			// ValidateResourceKey fail-closed);前端不渲染,不删。
			Key: "memory.long_term_top_k", Scope: ScopeResource, Category: "memory",
			DisplayName: "长期记忆 Top-K", Description: "长期记忆检索条数(废弃,与 recall_top_k 重复)",
			ValueType: TypeInt, Default: int64(5),
			VisualHint:  VisualHint{Control: ControlSlider, Min: f(1), Max: f(20), Step: f(1)},
			Optimizable: true,
		},
		{
			Key: "memory.max_facts_per_extraction", Scope: ScopeResource, Category: "memory",
			DisplayName: "单次抽取事实上限", Description: "每轮抽取并写入的最大事实数,与写入硬上限对齐",
			ValueType: TypeInt, Default: int64(10),
			VisualHint:  VisualHint{Control: ControlSlider, Min: f(1), Max: f(10), Step: f(1)},
			Optimizable: true,
		},
		{
			// 提取 prompt/model:agent 维度直接绑定,按 agent 逐条解析。prompt
			// 为**完整**系统提示词（支持 {user_id}/{agent_id}/{max_facts} 占位符），
			// 未配置即失败（fail-closed，无内置模板兜底）；model 非空传给提取请求,
			// 空 = 空串 → client 默认解析。
			Key: "memory.extraction_prompt", Scope: ScopeResource, Category: "memory",
			DisplayName: "提取提示词", Description: "记忆抽取的完整系统提示词(必填,支持 {user_id}/{agent_id}/{max_facts} 占位符),未配置即失败",
			ValueType: TypeString, Default: "",
			VisualHint:  VisualHint{Control: ControlTextarea},
			Optimizable: false,
		},
		{
			Key: "memory.extraction_model", Scope: ScopeResource, Category: "memory",
			DisplayName: "提取模型", Description: "记忆抽取使用的独立模型,空表示 client 默认",
			ValueType: TypeString, Default: "",
			VisualHint:  VisualHint{Control: ControlSelect},
			Optimizable: false,
		},
	} {
		_ = r.Register(def)
	}
}

// registerMemoryWorkerParams covers the cross-agent background workers
// (enrich / session summary / history summary / supersede). These are
// platform-scope: applied globally to all tenants' batch LLM calls, written
// via PUT /admin/parameters (RequireGlobalAdmin) into platform_settings and
// resolved at runtime per call (hot-update, no restart). Prompt keys carry no
// built-in default: unset → fail-closed（记忆写入/后台 LLM 任务失败并告警）。
// All keys are Optimizable:false with no
// EvaluationKeys — they must not enter the evaluation search space nor clash
// with the gate-only prompt keys. NOTE: the `memory.` prefix now carries both
// scopes (resource per-agent keys above + these platform keys); scope is the
// distinguishing axis, not the prefix.
func (r *ParametersRegistry) registerMemoryWorkerParams() {
	f := func(v float64) *float64 { return &v }
	for _, def := range []ParameterDefinition{
		{
			Key: "memory.enrich_prompt", Scope: ScopePlatform, Category: "memory",
			DisplayName: "记忆富化提示词", Description: "记忆富化 LLM 的系统提示词(必填,支持 %s(角色)/%s(消息) 占位符),未配置即失败",
			ValueType: TypeString, Default: "",
			VisualHint:  VisualHint{Control: ControlTextarea},
			Optimizable: false,
		},
		{
			Key: "memory.enrich_temperature", Scope: ScopePlatform, Category: "memory",
			DisplayName: "记忆富化温度", Description: "记忆富化 LLM 采样温度(0=默认,统一钳制 [0,1])",
			ValueType: TypeFloat, Default: 0.1,
			VisualHint:  VisualHint{Control: ControlSlider, Min: f(0), Max: f(1), Step: f(0.1)},
			Optimizable: false,
		},
		{
			Key: "memory.enrich_model", Scope: ScopePlatform, Category: "memory",
			DisplayName: "记忆富化模型", Description: "记忆富化使用的 LLM 模型(模型管理目录选择)",
			ValueType: TypeString, Default: "",
			VisualHint:  VisualHint{Control: ControlModel},
			Optimizable: false,
		},
		{
			Key: "memory.summary_prompt", Scope: ScopePlatform, Category: "memory",
			DisplayName: "记忆摘要提示词", Description: "记忆会话摘要 LLM 的系统提示词(必填,支持 %s 占位符),未配置即失败",
			ValueType: TypeString, Default: "",
			VisualHint:  VisualHint{Control: ControlTextarea},
			Optimizable: false,
		},
		{
			Key: "memory.summary_temperature", Scope: ScopePlatform, Category: "memory",
			DisplayName: "记忆摘要温度", Description: "记忆会话摘要 LLM 采样温度(0=默认,统一钳制 [0,1])",
			ValueType: TypeFloat, Default: 0.2,
			VisualHint:  VisualHint{Control: ControlSlider, Min: f(0), Max: f(1), Step: f(0.1)},
			Optimizable: false,
		},
		{
			Key: "memory.summary_model", Scope: ScopePlatform, Category: "memory",
			DisplayName: "记忆摘要模型", Description: "记忆会话摘要使用的 LLM 模型(模型管理目录选择)",
			// 模型默认值必须为空：代码内不写死兜底模型，空模型交由
			// llmgateway 从模型目录解析默认。
			ValueType: TypeString, Default: "",
			VisualHint:  VisualHint{Control: ControlModel},
			Optimizable: false,
		},
		{
			Key: "memory.embedding_model", Scope: ScopePlatform, Category: "memory",
			DisplayName: "记忆嵌入模型",
			Description: "全局记忆嵌入模型（模型管理目录选择）；未设置时记忆写入 fail-closed 并告警",
			ValueType:   TypeString, Default: "",
			VisualHint:  VisualHint{Control: ControlEmbeddingModel},
			Optimizable: false,
		},
		{
			Key: "memory.history_summary_prompt", Scope: ScopePlatform, Category: "memory",
			DisplayName: "历史摘要提示词", Description: "历史消息摘要 LLM 的系统提示词(必填),未配置即失败",
			ValueType: TypeString, Default: "",
			VisualHint:  VisualHint{Control: ControlTextarea},
			Optimizable: false,
		},
		{
			Key: "memory.history_summary_temperature", Scope: ScopePlatform, Category: "memory",
			DisplayName: "历史摘要温度", Description: "历史消息摘要 LLM 采样温度(0=默认,统一钳制 [0,1])",
			ValueType: TypeFloat, Default: 0.2,
			VisualHint:  VisualHint{Control: ControlSlider, Min: f(0), Max: f(1), Step: f(0.1)},
			Optimizable: false,
		},
		{
			Key: "memory.history_summary_model", Scope: ScopePlatform, Category: "memory",
			DisplayName: "历史摘要模型", Description: "历史消息摘要使用的 LLM 模型(模型管理目录选择)",
			ValueType: TypeString, Default: "",
			VisualHint:  VisualHint{Control: ControlModel},
			Optimizable: false,
		},
		{
			Key: "memory.supersede_prompt", Scope: ScopePlatform, Category: "memory",
			DisplayName: "记忆取代提示词", Description: "记忆取代判定 LLM 的系统提示词(必填,支持 %s(旧事实)/%s(新事实) 占位符),未配置即失败",
			ValueType: TypeString, Default: "",
			VisualHint:  VisualHint{Control: ControlTextarea},
			Optimizable: false,
		},
		{
			Key: "memory.supersede_temperature", Scope: ScopePlatform, Category: "memory",
			DisplayName: "记忆取代温度", Description: "记忆取代判定 LLM 采样温度(0=默认,统一钳制 [0,1])",
			ValueType: TypeFloat, Default: 0.0,
			VisualHint:  VisualHint{Control: ControlSlider, Min: f(0), Max: f(1), Step: f(0.1)},
			Optimizable: false,
		},
		{
			Key: "memory.supersede_model", Scope: ScopePlatform, Category: "memory",
			DisplayName: "记忆取代模型", Description: "记忆取代判定使用的 LLM 模型(模型管理目录选择)",
			ValueType: TypeString, Default: "",
			VisualHint:  VisualHint{Control: ControlModel},
			Optimizable: false,
		},
		{
			// 会话摘要触发阈值:收口原冷 config 字段(EnricherSummaryTokenThreshold),
			// 默认与常量单一事实源对齐,平台可调。
			Key: "memory.summary_token_threshold", Scope: ScopePlatform, Category: "memory",
			DisplayName: "摘要触发 Token 阈值", Description: "消息累积超过该阈值后触发记忆摘要",
			ValueType: TypeInt, Default: int64(constants.EnricherSummaryTokenThreshold),
			VisualHint:  VisualHint{Control: ControlSlider, Min: f(500), Max: f(10000), Step: f(500), Unit: "tokens"},
			Optimizable: false,
		},
	} {
		_ = r.Register(def)
	}
}

// registerPromptEvaluationKeys registers the prompt patch bare keys as
// gate-only evaluation keys. The prompt.* platform value definitions were
// removed with the prompt-management feature; the bare keys survive only so
// candidate-patch validation (validatePatchKeys) keeps admitting the prompt
// fields the evaluation optimizer produces. They have no definition behind
// them: KeyForEvaluation does not resolve them, and they are absent from
// Schema/PlatformValues.
func (r *ParametersRegistry) registerPromptEvaluationKeys() {
	for _, bare := range []string{
		"system_prompt", "instructions",
		"memory_extraction_prompt", "memory_summary_prompt",
		"memory_enrichment_prompt", "compaction_prompt",
	} {
		r.promptEvalKeys[bare] = struct{}{}
	}
}

func fp(v float64) *float64 { return &v }
