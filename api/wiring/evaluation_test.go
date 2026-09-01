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

func TestParseJudgeResponseDimensions(t *testing.T) {
	content := `{"passed":false,"reason":"事实错误","confidence":0.6,
		"dimensions":[
			{"name":"faithfulness","score":0.4,"passed":false,"reason":"与实际不符","confidence":0.7},
			{"name":"relevance","score":0.9,"passed":true,"reason":"","confidence":0.9},
			{"name":"completeness","score":0.8,"passed":true}
		]}`
	got, err := parseJudgeResponse(content)
	if err != nil {
		t.Fatalf("parseJudgeResponse: %v", err)
	}
	if got.Passed || got.Message != "事实错误" {
		t.Fatalf("verdict mismatch: %+v", got)
	}
	if got.Confidence != 0.6 {
		t.Fatalf("confidence = %v, want 0.6", got.Confidence)
	}
	if len(got.Dimensions) != 3 {
		t.Fatalf("dimensions = %d, want 3", len(got.Dimensions))
	}
	faith := got.Dimensions[0]
	if faith.Name != "faithfulness" || faith.Score != 0.4 || faith.Passed || faith.Confidence != 0.7 {
		t.Fatalf("faithfulness mismatch: %+v", faith)
	}
	if got.Dimensions[2].Confidence != 1.0 { // 缺失回退 1.0
		t.Fatalf("completeness confidence = %v, want 1.0", got.Dimensions[2].Confidence)
	}
}

func TestParseJudgeResponseInvalidDimensionsDropped(t *testing.T) {
	// 说明：name 空、score 越界（2.5 / -0.1）的维度应被丢弃，仅保留合法维度。
	content := `{"passed":true,"reason":"通过","dimensions":[
		{"name":"","score":0.5,"passed":true},
		{"name":"relevance","score":2.5,"passed":true},
		{"name":"completeness","score":-0.1,"passed":true},
		{"name":"faithfulness","score":0.7,"passed":true}
	]}`
	got, err := parseJudgeResponse(content)
	if err != nil {
		t.Fatalf("parseJudgeResponse: %v", err)
	}
	if len(got.Dimensions) != 1 || got.Dimensions[0].Name != "faithfulness" {
		t.Fatalf("dimensions = %+v, want only faithfulness", got.Dimensions)
	}
}

func TestParseJudgeResponseNoDimensionsTolerated(t *testing.T) {
	content := `{"passed":false,"reason":"不及格","confidence":0.3}`
	got, err := parseJudgeResponse(content)
	if err != nil {
		t.Fatalf("parseJudgeResponse: %v", err)
	}
	if got.Passed || len(got.Dimensions) != 0 {
		t.Fatalf("old-style verdict must stay single: %+v", got)
	}
}
