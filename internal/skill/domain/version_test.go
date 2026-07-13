package domain

import "testing"

func TestToolContractValidateRequiresConfirmedValidContract(t *testing.T) {
	contract := ToolContract{
		ToolName:        "classify_complaint",
		Description:     "判断客户投诉类型并给出处理建议",
		InputSchema:     map[string]any{"type": "object", "properties": map[string]any{}},
		OutputSchema:    map[string]any{"type": "object"},
		CallingGuidance: "只在用户表达投诉时调用",
		Confirmed:       true,
	}

	if err := contract.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestToolContractValidateRejectsUnsafeToolName(t *testing.T) {
	contract := ToolContract{
		ToolName:     "投诉 分类",
		Description:  "判断客户投诉类型",
		InputSchema:  map[string]any{"type": "object"},
		OutputSchema: map[string]any{"type": "object"},
		Confirmed:    true,
	}

	if err := contract.Validate(); err == nil {
		t.Fatal("expected invalid tool name error")
	}
}

func TestSkillVersionPublishableRequiresCapabilityContractImplementationAndTests(t *testing.T) {
	version := SkillVersion{
		Status: VersionStatusDraft,
		Capability: Capability{
			Goal:      "判断客户投诉类型",
			WhenToUse: "用户表达投诉时",
			Examples:  []CapabilityExample{{Input: "快递没更新", ExpectedOutput: "物流问题"}},
		},
		ToolContract: ToolContract{
			ToolName:     "classify_complaint",
			Description:  "判断客户投诉类型",
			InputSchema:  map[string]any{"type": "object"},
			OutputSchema: map[string]any{"type": "object"},
			Confirmed:    true,
		},
		Implementation: Implementation{
			Mode:   "prompt",
			Source: map[string]any{"promptTemplate": "分类：{{.input}}"},
		},
	}

	if err := version.ValidatePublishable(1); err != nil {
		t.Fatalf("ValidatePublishable() error = %v", err)
	}
}
