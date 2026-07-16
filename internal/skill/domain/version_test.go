package domain

import "testing"

func TestActivationContractValidateRequiresConfirmedValidContract(t *testing.T) {
	contract := ActivationContract{
		Name:         "classify_complaint",
		Description:  "判断客户投诉类型并给出处理建议",
		InputSchema:  map[string]any{"type": "object", "properties": map[string]any{}},
		OutputSchema: map[string]any{"type": "object"},
		Confirmed:    true,
	}

	if err := contract.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestActivationContractValidateRejectsUnsafeName(t *testing.T) {
	contract := ActivationContract{
		Name:         "投诉 分类",
		Description:  "判断客户投诉类型",
		InputSchema:  map[string]any{"type": "object"},
		OutputSchema: map[string]any{"type": "object"},
		Confirmed:    true,
	}

	if err := contract.Validate(); err == nil {
		t.Fatal("expected invalid tool name error")
	}
}

func TestSkillRevisionPublishableRequiresInstructionsAndRequirements(t *testing.T) {
	revision := SkillRevision{
		Status: VersionStatusDraft,
		Capability: Capability{
			Goal:      "判断客户投诉类型",
			WhenToUse: "用户表达投诉时",
			Examples:  []CapabilityExample{{Input: "快递没更新", ExpectedOutput: "物流问题"}},
		},
		ActivationContract: ActivationContract{
			Name:         "classify_complaint",
			Description:  "判断客户投诉类型",
			InputSchema:  map[string]any{"type": "object"},
			OutputSchema: map[string]any{"type": "object"},
			Confirmed:    true,
		},
		Instructions: "根据投诉内容分类；需要订单数据时调用允许的 MCP 工具。",
		Requirements: Requirements{
			MCPToolIDs:            []string{"mcp:orders:get_order"},
			KnowledgeWorkspaceIDs: []string{"support-policy"},
			MemoryScopes:          []string{"user"},
		},
	}

	if err := revision.ValidatePublishable(1); err != nil {
		t.Fatalf("ValidatePublishable() error = %v", err)
	}
}

func TestSkillRevisionContentHashTracksInstructionsAndRequirements(t *testing.T) {
	revision := SkillRevision{
		Capability: Capability{Goal: "分类", WhenToUse: "收到投诉时"},
		ActivationContract: ActivationContract{
			Name: "classify", Description: "分类", InputSchema: map[string]any{"type": "object"},
			OutputSchema: map[string]any{"type": "object"}, Confirmed: true,
		},
		Instructions: "分类用户输入",
		Requirements: Requirements{MCPToolIDs: []string{"mcp:orders:get_order"}},
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

func TestRequirementsValidateRejectsNonMCPToolID(t *testing.T) {
	requirements := Requirements{MCPToolIDs: []string{"orders.get_order"}}
	if err := requirements.Validate(); err == nil {
		t.Fatal("expected invalid MCP tool ID error")
	}
}
