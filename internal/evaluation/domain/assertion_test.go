package domain

import "testing"

type structuredAssertionFixture struct {
	Relevant bool `json:"relevant"`
	Details  struct {
		Count int      `json:"count"`
		IDs   []string `json:"ids"`
	} `json:"details"`
}

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

func TestEvaluateAssertionExactUsesJSONSemantics(t *testing.T) {
	actual := structuredAssertionFixture{Relevant: true}
	actual.Details.Count = 1
	actual.Details.IDs = []string{"doc-1"}
	expected := map[string]any{"details": map[string]any{"ids": []any{"doc-1"}, "count": float64(1)},
		"relevant": true}
	result, err := EvaluateAssertion(AssertionExact, actual, expected)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Passed {
		t.Fatalf("semantically equivalent structured JSON failed: %+v", result)
	}

	result, err = EvaluateAssertion(AssertionExact, "exact", "exact ")
	if err != nil || result.Passed {
		t.Fatalf("string exact semantics changed: result=%+v err=%v", result, err)
	}
	if _, err := EvaluateAssertion(AssertionExact, make(chan int), nil); err == nil {
		t.Fatal("unsupported actual value did not fail explicitly")
	}
	result, err = EvaluateAssertion(AssertionExact, uint64(9007199254740992), uint64(9007199254740993))
	if err != nil || result.Passed {
		t.Fatalf("distinct large integers collapsed during normalization: result=%+v err=%v", result, err)
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
