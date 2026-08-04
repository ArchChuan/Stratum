package domain

import "testing"

func TestToolRiskLevelValidate(t *testing.T) {
	for _, level := range []ToolRiskLevel{
		ToolRiskRead,
		ToolRiskWriteReversible,
		ToolRiskDestructive,
		ToolRiskUnclassified,
	} {
		if err := level.Validate(); err != nil {
			t.Errorf("expected %q valid, got %v", level, err)
		}
	}
}

func TestToolRiskLevelValidateRejectsInvalid(t *testing.T) {
	// 极端情况：空值、未知字符串、派生类型同值（类型底层相同但未被枚举）都必须拒绝。
	for _, level := range []ToolRiskLevel{"", "write", "READ", "unknown", "read_write", " "} {
		if err := level.Validate(); err == nil {
			t.Errorf("expected %q invalid, got nil", level)
		}
	}
}
