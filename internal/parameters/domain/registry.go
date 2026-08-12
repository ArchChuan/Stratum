package domain

import (
	"fmt"
	"sort"
)

// ParametersRegistry is the code-level central registry of all optimizable /
// tunable parameters. Built at startup from built-in definitions; the
// evaluation loop and the frontend schema both read from it, replacing the
// legacy hard-coded whitelists.
type ParametersRegistry struct {
	byKey        map[string]*ParameterDefinition
	byEvalKey    map[string]string // evaluation bare-name → registry key
	resourceKeys []string          // sorted resource-scope keys
}

// NewParametersRegistry builds a registry with all built-in definitions.
func NewParametersRegistry() *ParametersRegistry {
	r := &ParametersRegistry{
		byKey:     make(map[string]*ParameterDefinition, 40),
		byEvalKey: make(map[string]string, 24),
	}
	r.registerAgentParams()
	r.registerRAGParams()
	r.registerMCPParams()
	r.registerOptimizerParams()
	r.registerJudgeParams()
	r.registerTraceParams()
	r.registerMemoryParams()
	r.registerPromptParams()
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
// is registered — the legacy whitelists are replaced by this check.
func (r *ParametersRegistry) IsEvaluationKey(bareKey string) bool {
	_, ok := r.byEvalKey[bareKey]
	return ok
}

// KeyForEvaluation resolves a bare evaluation key to its registry key.
func (r *ParametersRegistry) KeyForEvaluation(bareKey string) (string, bool) {
	key, ok := r.byEvalKey[bareKey]
	return key, ok
}

// EvaluationKeys returns every registered bare evaluation key.
func (r *ParametersRegistry) EvaluationKeys() []string {
	keys := make([]string, 0, len(r.byEvalKey))
	for k := range r.byEvalKey {
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
			DisplayName: "温度", Description: "采样温度,0 表示不设置(网关/provider 默认生效)",
			ValueType: TypeFloat, Default: 0.7,
			VisualHint:     VisualHint{Control: ControlSlider, Min: f(0), Max: f(2), Step: f(0.1)},
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
			Key: "agent.compaction_safety_ratio", Scope: ScopeResource, Category: "agent",
			DisplayName: "压缩安全比率", Description: "压缩安全比率,0 表示默认",
			ValueType: TypeFloat, Default: 0.0,
			VisualHint:     VisualHint{Control: ControlSlider, Min: f(0), Max: f(0.95), Step: f(0.05)},
			Optimizable:    true,
			EvaluationKeys: []string{"compaction_safety_ratio"},
		},
		{
			Key: "agent.compaction_cooldown_sec", Scope: ScopeResource, Category: "agent",
			DisplayName: "压缩冷却(秒)", Description: "压缩触发后的冷却窗口,0 表示默认常量",
			ValueType: TypeInt, Default: int64(0),
			VisualHint:  VisualHint{Control: ControlSlider, Min: f(0), Max: f(120), Step: f(5), Unit: "s"},
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

// registerOptimizerParams are the platform-level LLM optimizer defaults that
// replace the hard-coded qwen-plus/0.2/2048 in gatewayPromptRewriter.
func (r *ParametersRegistry) registerOptimizerParams() {
	f := func(v float64) *float64 { return &v }
	for _, def := range []ParameterDefinition{
		{
			Key: "evaluation.optimizer.model", Scope: ScopePlatform, Category: "evaluation",
			DisplayName: "优化器模型", Description: "提示词/参数优化器使用的 LLM 模型",
			ValueType: TypeString, Default: "qwen-plus",
			VisualHint:  VisualHint{Control: ControlSelect},
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
			ValueType: TypeString, Default: "qwen-plus",
			VisualHint:  VisualHint{Control: ControlSelect},
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
// hard-coded in pkg/constants/memory.go.
func (r *ParametersRegistry) registerMemoryParams() {
	f := func(v float64) *float64 { return &v }
	for _, def := range []ParameterDefinition{
		{
			Key: "memory.recall_top_k", Scope: ScopePlatform, Category: "memory",
			DisplayName: "记忆召回 Top-K", Description: "记忆召回返回条数",
			ValueType: TypeInt, Default: int64(10),
			VisualHint:  VisualHint{Control: ControlSlider, Min: f(1), Max: f(50), Step: f(1)},
			Optimizable: true,
		},
		{
			Key: "memory.fact_injection_top_n", Scope: ScopePlatform, Category: "memory",
			DisplayName: "事实注入条数", Description: "会话上下文注入的抽取事实条数",
			ValueType: TypeInt, Default: int64(8),
			VisualHint:  VisualHint{Control: ControlSlider, Min: f(1), Max: f(20), Step: f(1)},
			Optimizable: true,
		},
		{
			Key: "memory.history_injection_top_n", Scope: ScopePlatform, Category: "memory",
			DisplayName: "历史注入条数", Description: "会话上下文注入的历史消息条数",
			ValueType: TypeInt, Default: int64(3),
			VisualHint:  VisualHint{Control: ControlSlider, Min: f(0), Max: f(10), Step: f(1)},
			Optimizable: true,
		},
		{
			Key: "memory.long_term_top_k", Scope: ScopePlatform, Category: "memory",
			DisplayName: "长期记忆 Top-K", Description: "长期记忆检索条数",
			ValueType: TypeInt, Default: int64(5),
			VisualHint:  VisualHint{Control: ControlSlider, Min: f(1), Max: f(20), Step: f(1)},
			Optimizable: true,
		},
		{
			Key: "memory.max_facts_per_extraction", Scope: ScopePlatform, Category: "memory",
			DisplayName: "单次抽取事实上限", Description: "每次抽取的最大事实数",
			ValueType: TypeInt, Default: int64(20),
			VisualHint:  VisualHint{Control: ControlSlider, Min: f(1), Max: f(50), Step: f(1)},
			Optimizable: true,
		},
	} {
		_ = r.Register(def)
	}
}

// registerPromptParams are the six legacy prompt keys. Values flow through the
// existing prompt registry (agent>tenant>global); the platform default here is
// the global tier. Frontend exposure stays at /prompts.
func (r *ParametersRegistry) registerPromptParams() {
	for _, def := range []ParameterDefinition{
		{
			Key: "prompt.system_prompt", Scope: ScopePlatform, Category: "prompt",
			DisplayName: "系统提示词", Description: "全局系统提示词(经 prompt 注册表生效)",
			ValueType: TypeString, Default: "",
			VisualHint:  VisualHint{Control: ControlTextarea},
			Optimizable: true, EvaluationKeys: []string{"system_prompt"},
		},
		{
			Key: "prompt.instructions", Scope: ScopePlatform, Category: "prompt",
			DisplayName: "行为指令", Description: "全局行为指令(经 prompt 注册表生效)",
			ValueType: TypeString, Default: "",
			VisualHint:  VisualHint{Control: ControlTextarea},
			Optimizable: true, EvaluationKeys: []string{"instructions"},
		},
		{
			Key: "prompt.memory_extraction_prompt", Scope: ScopePlatform, Category: "prompt",
			DisplayName: "记忆抽取提示词", Description: "记忆抽取(经 prompt 注册表生效)",
			ValueType: TypeString, Default: "",
			VisualHint:  VisualHint{Control: ControlTextarea},
			Optimizable: true, EvaluationKeys: []string{"memory_extraction_prompt"},
		},
		{
			Key: "prompt.memory_summary_prompt", Scope: ScopePlatform, Category: "prompt",
			DisplayName: "记忆摘要提示词", Description: "记忆摘要(经 prompt 注册表生效)",
			ValueType: TypeString, Default: "",
			VisualHint:  VisualHint{Control: ControlTextarea},
			Optimizable: true, EvaluationKeys: []string{"memory_summary_prompt"},
		},
		{
			Key: "prompt.memory_enrichment_prompt", Scope: ScopePlatform, Category: "prompt",
			DisplayName: "记忆富化提示词", Description: "记忆富化(经 prompt 注册表生效)",
			ValueType: TypeString, Default: "",
			VisualHint:  VisualHint{Control: ControlTextarea},
			Optimizable: true, EvaluationKeys: []string{"memory_enrichment_prompt"},
		},
		{
			Key: "prompt.compaction_prompt", Scope: ScopePlatform, Category: "prompt",
			DisplayName: "压缩提示词", Description: "上下文压缩(经 prompt 注册表生效)",
			ValueType: TypeString, Default: "",
			VisualHint:  VisualHint{Control: ControlTextarea},
			Optimizable: true, EvaluationKeys: []string{"compaction_prompt"},
		},
	} {
		_ = r.Register(def)
	}
}

func fp(v float64) *float64 { return &v }
