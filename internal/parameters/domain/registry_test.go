package domain

import (
	"encoding/json"
	"testing"
)

func TestRegistryRegistersAllBuiltinKeys(t *testing.T) {
	r := NewParametersRegistry()
	defs := r.Schema()

	// 全部定义唯一注册,且 key 前缀合法。
	seen := make(map[string]bool, len(defs))
	for _, def := range defs {
		if seen[def.Key] {
			t.Fatalf("duplicate key in schema: %s", def.Key)
		}
		seen[def.Key] = true
		if def.Scope != ScopePlatform && def.Scope != ScopeResource {
			t.Fatalf("%s: invalid scope %q", def.Key, def.Scope)
		}
		// 复杂结构参数(bindings/enabled_tools)无定义默认,其余必须携带。
		if def.Default == nil && def.ValidateFn == nil {
			t.Fatalf("%s: missing default", def.Key)
		}
	}

	// 存量搜索空间零收缩:14 参数全部 Optimizable=true。
	for _, key := range []string{
		"agent.temperature", "agent.max_tokens", "agent.max_context_tokens",
		"agent.max_iterations", "agent.model", "agent.bindings",
		"rag.top_k", "rag.score_threshold", "rag.reranking", "rag.query_rewrite",
		"mcp.enabled_tools", "mcp.timeout_ms", "mcp.max_retries",
	} {
		def, ok := r.Get(key)
		if !ok {
			t.Fatalf("legacy key %s not registered", key)
		}
		if !def.Optimizable {
			t.Fatalf("legacy key %s must stay optimizable (search space must not shrink)", key)
		}
	}

}

func TestRegistryEvaluationKeyMapping(t *testing.T) {
	r := NewParametersRegistry()
	for bare, want := range map[string]string{
		"temperature":        "agent.temperature",
		"max_tokens":         "agent.max_tokens",
		"maxTokens":          "agent.max_tokens",
		"max_context_tokens": "agent.max_context_tokens",
		"max_iterations":     "agent.max_iterations",
		"model":              "agent.model",
		"bindings":           "agent.bindings",
		"top_k":              "rag.top_k",
		"score_threshold":    "rag.score_threshold",
		"reranking":          "rag.reranking",
		"query_rewrite":      "rag.query_rewrite",
		"enabled_tools":      "mcp.enabled_tools",
		"timeout_ms":         "mcp.timeout_ms",
		"max_retries":        "mcp.max_retries",
	} {
		if !r.IsEvaluationKey(bare) {
			t.Errorf("bare key %q must be registered", bare)
		}
		if got, _ := r.KeyForEvaluation(bare); got != want {
			t.Errorf("KeyForEvaluation(%q) = %q, want %q", bare, got, want)
		}
	}
	if r.IsEvaluationKey("bogus_key") {
		t.Error("unknown bare key must not be registered")
	}
}

// TestPromptEvaluationKeysAreGateOnly pins the decoupling: the 5 prompt patch
// bare keys survive as gate-only evaluation keys (valid candidate-patch
// fields for validatePatchKeys) but carry no parameter definition — they must
// not resolve through KeyForEvaluation and must not appear in the schema.
func TestPromptEvaluationKeysAreGateOnly(t *testing.T) {
	r := NewParametersRegistry()
	for _, bare := range []string{
		"system_prompt", "instructions",
		"memory_extraction_prompt", "memory_summary_prompt",
		"memory_enrichment_prompt",
	} {
		if !r.IsEvaluationKey(bare) {
			t.Errorf("prompt key %q must stay a valid candidate-patch key (gate-only)", bare)
		}
		if _, ok := r.KeyForEvaluation(bare); ok {
			t.Errorf("prompt key %q must not resolve to a parameter definition", bare)
		}
		if _, ok := r.Get("prompt." + bare); ok {
			t.Errorf("prompt.%s definition must be removed from the registry", bare)
		}
	}
	for _, def := range r.Schema() {
		if def.Key[:7] == "prompt." {
			t.Fatalf("schema must not expose prompt definitions, got %s", def.Key)
		}
	}
}

// TestRegistryAgentMemoryParams 覆盖 agent 维度新 key:压缩温度/模型、平台级
// 压缩/全局提示词、提取 prompt/model、召回 top_k 校准、long_term_top_k 保留
// 标注 deprecated。断言 compaction 温度/模型不进入 byEvalKey、不设
// EvaluationKeys(防半注册回归)。
func TestRegistryAgentMemoryParams(t *testing.T) {
	r := NewParametersRegistry()

	// 压缩温度 bare key 不进 byEvalKey(评测搜索空间干净),写时校验经短名匹配。
	if got, ok := r.KeyForEvaluation("compaction_temperature"); ok {
		t.Errorf("KeyForEvaluation(compaction_temperature) = %q, want unregistered", got)
	}
	if got, ok := r.KeyByShortName("compaction_temperature"); !ok || got != "agent.compaction_temperature" {
		t.Errorf("KeyByShortName(compaction_temperature) = %q/%v, want agent.compaction_temperature/true", got, ok)
	}

	// 平台级提示词:agent.compaction_prompt / agent.system_prompt（fail-closed,
	// 无默认模板）,不进入 byEvalKey。
	for _, key := range []string{"agent.compaction_prompt", "agent.system_prompt"} {
		def, ok := r.Get(key)
		if !ok {
			t.Fatalf("%s not registered", key)
		}
		if def.Scope != ScopePlatform || def.Optimizable || def.Default != "" {
			t.Errorf("%s scope/optimizable/default = %q/%v/%v, want platform/false/empty", key, def.Scope, def.Optimizable, def.Default)
		}
		if def.VisualHint.Control != ControlTextarea {
			t.Errorf("%s control = %q, want textarea", key, def.VisualHint.Control)
		}
		if _, ok := r.KeyForEvaluation("compaction_prompt"); ok {
			t.Error("KeyForEvaluation(compaction_prompt) must stay unregistered")
		}
	}

	// 压缩温度/模型:平台级共用配置（唯一来源，所有 agent 一致），不可优化、
	// 无 EvaluationKeys；temperature 0 = 默认常量，model 空 = 网关默认。
	platformCompaction := map[string]struct {
		control          Control
		wantMin, wantMax float64
	}{
		"agent.compaction_temperature": {control: ControlSlider, wantMin: 0, wantMax: 1},
		"agent.compaction_model":       {control: ControlModel},
	}
	for key, want := range platformCompaction {
		def, ok := r.Get(key)
		if !ok {
			t.Fatalf("%s not registered", key)
		}
		if def.Scope != ScopePlatform || def.Optimizable || len(def.EvaluationKeys) != 0 {
			t.Errorf("%s scope/optimizable/evalKeys = %q/%v/%d, want platform/false/0", key, def.Scope, def.Optimizable, len(def.EvaluationKeys))
		}
		if def.VisualHint.Control != want.control {
			t.Errorf("%s control = %q, want %q", key, def.VisualHint.Control, want.control)
		}
		if def.VisualHint.Min != nil && *def.VisualHint.Min != want.wantMin {
			t.Errorf("%s VisualHint.Min = %v, want %v", key, *def.VisualHint.Min, want.wantMin)
		}
		if def.VisualHint.Max != nil && *def.VisualHint.Max != want.wantMax {
			t.Errorf("%s VisualHint.Max = %v, want %v", key, *def.VisualHint.Max, want.wantMax)
		}
	}

	// 提取/反思 prompt/model:ScopePlatform（记忆配置平台化,编辑入口在平台参数页）,
	// 字符串自由校验,无 EvaluationKeys。
	for _, key := range []string{
		"memory.extraction_prompt", "memory.extraction_model",
		"memory.reflection_prompt", "memory.reflection_model",
	} {
		def, ok := r.Get(key)
		if !ok {
			t.Fatalf("%s not registered", key)
		}
		if def.Scope != ScopePlatform || def.Optimizable {
			t.Errorf("%s scope/optimizable = %q/%v, want platform/false", key, def.Scope, def.Optimizable)
		}
		if len(def.EvaluationKeys) != 0 {
			t.Errorf("%s must not carry EvaluationKeys (string free-form), got %v", key, def.EvaluationKeys)
		}
	}

	// 提取/反思模型:模型目录选择器(ControlModel),下拉数据来自模型管理;
	// 不得用无 options 的 select(空下拉 bug 回归)。
	for _, key := range []string{"memory.extraction_model", "memory.reflection_model"} {
		def, ok := r.Get(key)
		if !ok {
			t.Fatalf("%s not registered", key)
		}
		if def.VisualHint.Control != ControlModel {
			t.Errorf("%s control = %q, want model picker (ControlModel)", key, def.VisualHint.Control)
		}
	}

	// 记忆数值参数:ScopePlatform（平台参数页编辑,0=unset 回落定义默认）。
	for _, key := range []string{
		"memory.recall_top_k", "memory.fact_injection_top_n",
		"memory.history_injection_top_n", "memory.max_facts_per_extraction",
	} {
		def, ok := r.Get(key)
		if !ok {
			t.Fatalf("%s not registered", key)
		}
		if def.Scope != ScopePlatform {
			t.Errorf("%s scope = %q, want platform", key, def.Scope)
		}
	}

	// 召回 top_k:Default 5(对齐运行时兜底)、Max 20(工具上限)。
	recall, ok := r.Get("memory.recall_top_k")
	if !ok {
		t.Fatal("memory.recall_top_k not registered")
	}
	if d, ok := recall.Default.(int64); !ok || d != 5 {
		t.Errorf("recall_top_k Default = %#v, want 5", recall.Default)
	}
	if recall.VisualHint.Max == nil || *recall.VisualHint.Max != 20 {
		t.Errorf("recall_top_k VisualHint.Max = %v, want 20", recall.VisualHint.Max)
	}

	// long_term_top_k 保留注册(兼容存量 agents.parameters 残留 key,删会破坏
	// ValidateResourceKey fail-closed 提升路径)。
	if _, ok := r.Get("memory.long_term_top_k"); !ok {
		t.Error("memory.long_term_top_k must stay registered (deprecated)")
	}
}

func TestRegistryDuplicateKeyRejected(t *testing.T) {
	r := NewParametersRegistry()
	err := r.Register(ParameterDefinition{Key: "agent.temperature", Scope: ScopePlatform, ValueType: TypeFloat, Default: 1.0})
	if err == nil {
		t.Fatal("duplicate key must be rejected")
	}
}

func TestParameterDefinitionValidateAndNormalize(t *testing.T) {
	r := NewParametersRegistry()
	cases := []struct {
		name    string
		key     string
		value   any
		wantOK  bool
		wantVal any
	}{
		{name: "temperature in bounds", key: "agent.temperature", value: json.Number("0.3"), wantOK: true, wantVal: 0.3},
		{name: "temperature above max", key: "agent.temperature", value: 2.5, wantOK: false},
		{name: "temperature below min", key: "agent.temperature", value: -0.1, wantOK: false},
		{name: "max_tokens zero unset ok", key: "agent.max_tokens", value: 0, wantOK: true, wantVal: int64(0)},
		{name: "max_tokens negative", key: "agent.max_tokens", value: -1, wantOK: false},
		{name: "bool", key: "rag.reranking", value: true, wantOK: true, wantVal: true},
		{name: "bool wrong type", key: "rag.reranking", value: "yes", wantOK: false},
		{name: "string", key: "agent.model", value: "qwen-plus", wantOK: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			def, ok := r.Get(tc.key)
			if !ok {
				t.Fatalf("key %s not registered", tc.key)
			}
			got, err := def.Normalize(tc.value)
			if tc.wantOK {
				if err != nil {
					t.Fatalf("Normalize(%v) error: %v", tc.value, err)
				}
				if tc.wantVal != nil && got != tc.wantVal {
					t.Fatalf("Normalize(%v) = %v (%T), want %v", tc.value, got, got, tc.wantVal)
				}
			} else if err == nil {
				t.Fatalf("Normalize(%v) must fail", tc.value)
			}
		})
	}
}

// TestRegistryMemoryEmbeddingModel 断言平台级记忆嵌入模型参数定义：
// ScopePlatform、embedding_model 控件、不可优化、无评测 key（防半注册回归）。
func TestRegistryMemoryEmbeddingModel(t *testing.T) {
	r := NewParametersRegistry()
	def, ok := r.Get("memory.embedding_model")
	if !ok {
		t.Fatal("memory.embedding_model not registered")
	}
	if def.Scope != ScopePlatform {
		t.Errorf("scope = %q, want platform", def.Scope)
	}
	if def.VisualHint.Control != ControlEmbeddingModel {
		t.Errorf("control = %q, want embedding_model", def.VisualHint.Control)
	}
	if def.Optimizable {
		t.Error("optimizable must be false")
	}
	if len(def.EvaluationKeys) != 0 {
		t.Errorf("evaluation keys = %v, want none", def.EvaluationKeys)
	}
	if def.Default != "" {
		t.Errorf("default = %v, want empty (fail-closed)", def.Default)
	}
}

// TestFactCheckJudgePromptIsPlatformEditable pins the platform-page contract:
// agent.factcheck.judge.prompt 必须保持 platform 域、非 sensitive（平台参数页
// 永不渲染敏感参数）、textarea 可编辑。回归 #420:误标 Sensitive 导致提示词
// 被前端过滤,页面只显示其余 factcheck 参数。
func TestFactCheckJudgePromptIsPlatformEditable(t *testing.T) {
	r := NewParametersRegistry()
	def, ok := r.Get("agent.factcheck.judge.prompt")
	if !ok {
		t.Fatal("agent.factcheck.judge.prompt not registered")
	}
	if def.Scope != ScopePlatform {
		t.Errorf("scope = %q, want platform", def.Scope)
	}
	if def.Sensitive {
		t.Error("judge prompt must not be Sensitive: platform settings page never renders sensitive params")
	}
	if def.VisualHint.Control != ControlTextarea {
		t.Errorf("control = %q, want textarea", def.VisualHint.Control)
	}
}
