package domain

import (
	"testing"

	parametersdomain "github.com/byteBuilderX/stratum/internal/parameters/domain"
)

// TestCandidateWhitelistStaysInLockstepWithRegistry pins 断层 6: the legacy
// domain whitelist must be the registry's evaluation-key set (20 legacy keys
// + 2 newly opened compaction keys). The registry is the source of truth;
// adding a key to one side without the other fails here instead of drifting
// silently across the 5 split validation points.
func TestCandidateWhitelistStaysInLockstepWithRegistry(t *testing.T) {
	reg := parametersdomain.NewParametersRegistry()

	registered := map[string]bool{}
	for _, key := range reg.EvaluationKeys() {
		registered[key] = true
	}
	for key := range allowedParameterFields {
		if !registered[key] {
			t.Errorf("candidate whitelist accepts %q but the registry has no such evaluation key", key)
		}
	}
	for key := range allowedPromptFields {
		if !registered[key] {
			t.Errorf("candidate prompt whitelist accepts %q but the registry has no such evaluation key", key)
		}
	}
	// 反向:注册表每个 evaluation key 都必须被 domain 白名单接受——
	// 注册表新增可优化参数而未同步 domain 时,GenerateParameterPatches 会拒,
	// 优化闭环新参数永远无法进入搜索空间。
	known := map[string]bool{}
	for key := range allowedParameterFields {
		known[key] = true
	}
	for key := range allowedPromptFields {
		known[key] = true
	}
	for _, key := range reg.EvaluationKeys() {
		if !known[key] {
			t.Errorf("registry evaluation key %q is not accepted by the candidate whitelist", key)
		}
	}
}

// TestCandidateWhitelistCountAnchors20Plus2 pins the exact search-space
// boundary: 14 parameter + 6 prompt legacy keys, plus the 2 newly opened
// compaction keys — never fewer (收缩会把 MCP 闭环维度清零)。
func TestCandidateWhitelistCountAnchors20Plus2(t *testing.T) {
	if got := len(allowedParameterFields); got != 16 {
		t.Fatalf("allowedParameterFields = %d keys, want 16 (14 legacy + 2 compaction)", got)
	}
	if got := len(allowedPromptFields); got != 6 {
		t.Fatalf("allowedPromptFields = %d keys, want 6", got)
	}
	for _, key := range []string{"compaction_recent_groups", "compaction_safety_ratio"} {
		if _, ok := allowedParameterFields[key]; !ok {
			t.Errorf("newly opened key %q missing from candidate whitelist", key)
		}
	}
}
