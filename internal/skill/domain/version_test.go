package domain

import "testing"

func TestSkillRevisionValidatePublishableAcceptsCompleteRevision(t *testing.T) {
	revision := SkillRevision{
		Name:         "classify_complaint",
		Description:  "判断客户投诉类型并给出处理建议",
		Instructions: "根据投诉内容分类；需要订单数据时调用允许的 MCP 工具。",
	}

	if err := revision.ValidatePublishable(); err != nil {
		t.Fatalf("ValidatePublishable() error = %v", err)
	}
}

func TestSkillRevisionValidatePublishableRejectsMissingField(t *testing.T) {
	cases := []struct {
		name  string
		field string
	}{
		{name: "missing name", field: "Name"},
		{name: "missing description", field: "Description"},
		{name: "missing instructions", field: "Instructions"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			revision := SkillRevision{
				Name:         "classify_complaint",
				Description:  "判断客户投诉类型",
				Instructions: "根据投诉内容分类。",
			}
			switch tc.field {
			case "Name":
				revision.Name = "   "
			case "Description":
				revision.Description = ""
			case "Instructions":
				revision.Instructions = "\t"
			}
			if err := revision.ValidatePublishable(); err == nil {
				t.Fatalf("expected not publishable error for %s", tc.field)
			}
		})
	}
}

func TestSkillRevisionContentHashTracksEditableSurface(t *testing.T) {
	revision := SkillRevision{
		Name:         "classify",
		Description:  "分类",
		Instructions: "分类用户输入",
	}
	first, err := revision.ComputeContentHash()
	if err != nil {
		t.Fatalf("ComputeContentHash returned error: %v", err)
	}
	second, err := revision.ComputeContentHash()
	if err != nil || second != first {
		t.Fatalf("hash must be stable: first=%q second=%q err=%v", first, second, err)
	}
	revision.Instructions = "使用新的分类规则处理用户输入"
	changed, err := revision.ComputeContentHash()
	if err != nil {
		t.Fatalf("ComputeContentHash changed returned error: %v", err)
	}
	if changed == first {
		t.Fatal("hash must change when instructions change")
	}
}
