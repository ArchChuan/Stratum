package domain

import "testing"

func TestExecutionFingerprintContentHashDeterministic(t *testing.T) {
	f := ExecutionFingerprint{
		ModelResolved:  "deepseek-v4-flash",
		ModelRoutedVia: []string{"qwen-max"},
		PromptVersion:  "system_prompt:v3",
		SkillRevisions: map[string]string{"skill-1": "abc"},
		TunableSnapshot: map[string]any{
			"temperature":    0.7,
			"max_iterations": 5,
		},
		ABBucket: 2,
	}
	h1 := f.ContentHash()
	if h1 != f.ContentHash() {
		t.Error("content hash must be deterministic")
	}
}

func TestExecutionFingerprintMapOrderInsensitive(t *testing.T) {
	// 极端情况：map 迭代顺序随机，hash 必须对 key 顺序不敏感。
	a := ExecutionFingerprint{ModelResolved: "m", SkillRevisions: map[string]string{"a": "1", "b": "2", "c": "3"}}
	b := ExecutionFingerprint{ModelResolved: "m", SkillRevisions: map[string]string{"c": "3", "a": "1", "b": "2"}}
	if a.ContentHash() != b.ContentHash() {
		t.Error("map key order must not affect content hash")
	}
	// 嵌套 map 同理。
	a2 := ExecutionFingerprint{ModelResolved: "m", TunableSnapshot: map[string]any{"x": 1, "y": 2, "z": 3}}
	b2 := ExecutionFingerprint{ModelResolved: "m", TunableSnapshot: map[string]any{"z": 3, "y": 2, "x": 1}}
	if a2.ContentHash() != b2.ContentHash() {
		t.Error("tunable map key order must not affect content hash")
	}
}

func TestExecutionFingerprintSensitiveToEachField(t *testing.T) {
	base := ExecutionFingerprint{
		ModelResolved: "m", PromptVersion: "v1", SkillRevisions: map[string]string{"s": "1"},
		TunableSnapshot: map[string]any{"t": 1}, ABBucket: 0,
	}
	mutate := func(mut func(*ExecutionFingerprint)) ExecutionFingerprint {
		// 深拷贝 map，避免 mutate 污染共享底层的 base。
		f := base
		f.SkillRevisions = make(map[string]string, len(base.SkillRevisions))
		for k, v := range base.SkillRevisions {
			f.SkillRevisions[k] = v
		}
		f.TunableSnapshot = make(map[string]any, len(base.TunableSnapshot))
		for k, v := range base.TunableSnapshot {
			f.TunableSnapshot[k] = v
		}
		mut(&f)
		return f
	}
	cases := []struct {
		name string
		f    ExecutionFingerprint
	}{
		{"model resolved", mutate(func(f *ExecutionFingerprint) { f.ModelResolved = "other" })},
		{"routed via", mutate(func(f *ExecutionFingerprint) { f.ModelRoutedVia = []string{"x"} })},
		{"prompt version", mutate(func(f *ExecutionFingerprint) { f.PromptVersion = "v2" })},
		{"config version", mutate(func(f *ExecutionFingerprint) { f.ConfigVersion = "c2" })},
		{"skill revision", mutate(func(f *ExecutionFingerprint) { f.SkillRevisions["s"] = "2" })},
		{"tunable", mutate(func(f *ExecutionFingerprint) { f.TunableSnapshot["t"] = 2 })},
		{"ab bucket", mutate(func(f *ExecutionFingerprint) { f.ABBucket = 1 })},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if base.ContentHash() == tc.f.ContentHash() {
				t.Error("hash must change when field changes")
			}
		})
	}
}

func TestExecutionFingerprintEmptyMaps(t *testing.T) {
	// 极端情况：nil 与空 map 序列化必须一致（omitempty），不能因 map 边界产生不同 hash。
	a := ExecutionFingerprint{ModelResolved: "m", PromptVersion: "v"}
	b := ExecutionFingerprint{ModelResolved: "m", PromptVersion: "v", SkillRevisions: map[string]string{}, TunableSnapshot: map[string]any{}}
	if a.ContentHash() != b.ContentHash() {
		t.Error("nil vs empty maps must hash identically")
	}
}

func TestExecutionFingerprintEmptySnapshotStaysStable(t *testing.T) {
	// 极端情况：完全空 fingerprint 不 panic，产出固定长度 hash。
	h := (ExecutionFingerprint{}).ContentHash()
	if len(h) != 64 {
		t.Errorf("expected 64 hex chars, got %d", len(h))
	}
}
