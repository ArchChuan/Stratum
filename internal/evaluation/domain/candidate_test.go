package domain

import "testing"

func TestGenerateParameterPatchesUsesDeterministicCartesianProduct(t *testing.T) {
	patches, err := GenerateParameterPatches(map[string][]any{
		"temperature": {0.1, 0.3},
		"maxTokens":   {512, 1024},
	})
	if err != nil {
		t.Fatalf("GenerateParameterPatches returned error: %v", err)
	}
	if len(patches) != 4 {
		t.Fatalf("expected 4 patches, got %d", len(patches))
	}
	if patches[0]["maxTokens"] != 512 || patches[0]["temperature"] != 0.1 {
		t.Fatalf("unexpected deterministic first patch: %#v", patches[0])
	}
}

func TestGenerateParameterPatchesRejectsProtectedFields(t *testing.T) {
	for _, field := range []string{"secretRefs", "permissions", "api_key"} {
		if _, err := GenerateParameterPatches(map[string][]any{field: {"unsafe"}}); err == nil {
			t.Fatalf("expected protected field %q to be rejected", field)
		}
	}
}

func TestValidatePromptPatchAllowsOnlyPromptContent(t *testing.T) {
	if err := ValidatePromptPatch(map[string]any{"instructions": "先分析输入，再按规则输出。"}); err != nil {
		t.Fatalf("valid prompt patch rejected: %v", err)
	}
	if err := ValidatePromptPatch(map[string]any{"permissions": map[string]any{"network": true}}); err == nil {
		t.Fatal("expected permissions patch to be rejected")
	}
}
