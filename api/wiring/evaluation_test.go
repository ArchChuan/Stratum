package wiring

import "testing"

func TestParsePromptRewritePatchesAcceptsFencedJSON(t *testing.T) {
	patches, err := parsePromptRewritePatches("```json\n" +
		`[{"prompt_patch":{"instructions":"更准确地分类输入"},"rationale":"修复漏分类"}]` + "\n```")
	if err != nil {
		t.Fatalf("parsePromptRewritePatches returned error: %v", err)
	}
	if len(patches) != 1 || patches[0].PromptPatch["instructions"] == "" {
		t.Fatalf("unexpected patches: %#v", patches)
	}
}

func TestParsePromptRewritePatchesRejectsProtectedFields(t *testing.T) {
	_, err := parsePromptRewritePatches(`[{"prompt_patch":{"permissions":{"network":true}}}]`)
	if err == nil {
		t.Fatal("expected protected prompt patch to be rejected")
	}
}

func TestParseJudgeResponseConfidence(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    float64
	}{
		{"explicit confidence", `{"passed":true,"reason":"ok","confidence":0.72}`, 0.72},
		{"null confidence falls back to 1.0", `{"passed":true,"reason":"ok","confidence":null}`, 1.0},
		{"missing confidence falls back to 1.0", `{"passed":false,"reason":"bad"}`, 1.0},
		{"out-of-range confidence falls back to 1.0", `{"passed":true,"reason":"ok","confidence":1.8}`, 1.0},
		{"negative confidence falls back to 1.0", `{"passed":true,"reason":"ok","confidence":-0.3}`, 1.0},
		{"code fence tolerated", "```json\n{\"passed\":true,\"reason\":\"ok\",\"confidence\":0.4}\n```", 0.4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseJudgeResponse(tc.content)
			if err != nil {
				t.Fatalf("parseJudgeResponse: %v", err)
			}
			if got.Confidence != tc.want {
				t.Fatalf("confidence = %v, want %v", got.Confidence, tc.want)
			}
		})
	}
}
