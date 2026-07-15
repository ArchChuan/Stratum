package wiring

import "testing"

func TestParsePromptRewritePatchesAcceptsFencedJSON(t *testing.T) {
	patches, err := parsePromptRewritePatches("```json\n" +
		`[{"prompt_patch":{"promptTemplate":"更准确：{{.input}}"},"rationale":"修复漏分类"}]` + "\n```")
	if err != nil {
		t.Fatalf("parsePromptRewritePatches returned error: %v", err)
	}
	if len(patches) != 1 || patches[0].PromptPatch["promptTemplate"] == "" {
		t.Fatalf("unexpected patches: %#v", patches)
	}
}

func TestParsePromptRewritePatchesRejectsProtectedFields(t *testing.T) {
	_, err := parsePromptRewritePatches(`[{"prompt_patch":{"permissions":{"network":true}}}]`)
	if err == nil {
		t.Fatal("expected protected prompt patch to be rejected")
	}
}
