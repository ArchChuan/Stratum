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

	// 存量搜索空间零收缩:14 参数 + 6 prompt 全部 Optimizable=true。
	for _, key := range []string{
		"agent.temperature", "agent.max_tokens", "agent.max_context_tokens",
		"agent.max_iterations", "agent.model", "agent.bindings",
		"rag.top_k", "rag.score_threshold", "rag.reranking", "rag.query_rewrite",
		"mcp.enabled_tools", "mcp.timeout_ms", "mcp.max_retries",
		"prompt.system_prompt", "prompt.instructions", "prompt.memory_extraction_prompt",
		"prompt.memory_summary_prompt", "prompt.memory_enrichment_prompt",
		"prompt.compaction_prompt",
	} {
		def, ok := r.Get(key)
		if !ok {
			t.Fatalf("legacy key %s not registered", key)
		}
		if !def.Optimizable {
			t.Fatalf("legacy key %s must stay optimizable (search space must not shrink)", key)
		}
	}

	// 新开放的 compaction 搜索空间维度。
	for _, key := range []string{"agent.compaction_recent_groups", "agent.compaction_safety_ratio"} {
		def, ok := r.Get(key)
		if !ok || !def.Optimizable {
			t.Fatalf("newly opened key %s must be optimizable", key)
		}
	}
}

func TestRegistryEvaluationKeyMapping(t *testing.T) {
	r := NewParametersRegistry()
	for bare, want := range map[string]string{
		"temperature":              "agent.temperature",
		"max_tokens":               "agent.max_tokens",
		"maxTokens":                "agent.max_tokens",
		"max_context_tokens":       "agent.max_context_tokens",
		"max_iterations":           "agent.max_iterations",
		"model":                    "agent.model",
		"bindings":                 "agent.bindings",
		"compaction_recent_groups": "agent.compaction_recent_groups",
		"compaction_safety_ratio":  "agent.compaction_safety_ratio",
		"top_k":                    "rag.top_k",
		"score_threshold":          "rag.score_threshold",
		"reranking":                "rag.reranking",
		"query_rewrite":            "rag.query_rewrite",
		"enabled_tools":            "mcp.enabled_tools",
		"timeout_ms":               "mcp.timeout_ms",
		"max_retries":              "mcp.max_retries",
		"system_prompt":            "prompt.system_prompt",
		"instructions":             "prompt.instructions",
		"memory_extraction_prompt": "prompt.memory_extraction_prompt",
		"memory_summary_prompt":    "prompt.memory_summary_prompt",
		"memory_enrichment_prompt": "prompt.memory_enrichment_prompt",
		"compaction_prompt":        "prompt.compaction_prompt",
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
		{name: "compaction select option", key: "agent.compaction_recent_groups", value: 3, wantOK: true, wantVal: int64(3)},
		{name: "compaction non-option int", key: "agent.compaction_recent_groups", value: 4, wantOK: false},
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
