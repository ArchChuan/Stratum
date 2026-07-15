package domain

import "testing"

func TestEvaluateAssertionModes(t *testing.T) {
	tests := []struct {
		name     string
		mode     AssertionMode
		actual   any
		expected any
		passed   bool
	}{
		{name: "exact JSON object", mode: AssertionExact, actual: map[string]any{"label": "ok"}, expected: map[string]any{"label": "ok"}, passed: true},
		{name: "contains text", mode: AssertionContains, actual: "订单已经发货", expected: "发货", passed: true},
		{name: "contains missing text", mode: AssertionContains, actual: "订单处理中", expected: "发货", passed: false},
		{name: "regex", mode: AssertionRegex, actual: "ticket-1234", expected: `^ticket-\d+$`, passed: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := EvaluateAssertion(tt.mode, tt.actual, tt.expected)
			if err != nil {
				t.Fatalf("EvaluateAssertion returned error: %v", err)
			}
			if result.Passed != tt.passed {
				t.Fatalf("expected passed=%v, got %v (%s)", tt.passed, result.Passed, result.Message)
			}
		})
	}
}

func TestEvaluateAssertionRejectsInvalidRegex(t *testing.T) {
	if _, err := EvaluateAssertion(AssertionRegex, "value", "["); err == nil {
		t.Fatal("expected invalid regex error")
	}
}

func TestResourceRefValidation(t *testing.T) {
	valid := ResourceRef{Kind: ResourceKindSkill, ResourceID: "skill-1", RevisionID: "version-2"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid ref rejected: %v", err)
	}
	if err := (ResourceRef{Kind: ResourceKindSkill, ResourceID: "skill-1"}).Validate(); err == nil {
		t.Fatal("expected missing revision id error")
	}
}
