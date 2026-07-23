package dto

import (
	"testing"

	"github.com/gin-gonic/gin/binding"
)

func TestEvaluationResourceKindsBinding(t *testing.T) {
	kinds := []string{"skill", "agent", "mcp", "knowledge"}

	for _, kind := range kinds {
		t.Run(kind, func(t *testing.T) {
			requests := []any{
				EvaluationResourceRef{Kind: kind, ResourceID: "resource-1", RevisionID: "revision-1"},
				CreateEvaluationSuiteRequest{
					Name: kind + " suite", ResourceKind: kind,
					Cases: []EvaluationCaseRequest{{
						Input: "input", ExpectedOutput: "output", AssertionMode: "exact",
					}},
				},
				RecordEvaluationFeedbackRequest{
					TraceID: "trace-1", ResourceKind: kind, ResourceID: "resource-1", IdempotencyKey: "key-1",
				},
			}
			for _, request := range requests {
				if err := binding.Validator.ValidateStruct(request); err != nil {
					t.Fatalf("kind %q rejected for %T: %v", kind, request, err)
				}
			}
		})
	}
}

func TestEvaluationResourceKindsBindingRejectsWorkflow(t *testing.T) {
	requests := []any{
		EvaluationResourceRef{Kind: "workflow", ResourceID: "resource-1", RevisionID: "revision-1"},
		CreateEvaluationSuiteRequest{
			Name: "workflow suite", ResourceKind: "workflow",
			Cases: []EvaluationCaseRequest{{
				Input: "input", ExpectedOutput: "output", AssertionMode: "exact",
			}},
		},
		RecordEvaluationFeedbackRequest{
			TraceID: "trace-1", ResourceKind: "workflow", ResourceID: "resource-1", IdempotencyKey: "key-1",
		},
	}

	for _, request := range requests {
		if err := binding.Validator.ValidateStruct(request); err == nil {
			t.Fatalf("workflow accepted for %T", request)
		}
	}
}
