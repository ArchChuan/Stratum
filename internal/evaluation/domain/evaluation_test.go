package domain

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/pkg/constants"
)

func TestEvaluateToolSequence(t *testing.T) {
	tests := []struct {
		name      string
		toolNames []string
		spec      ToolSpec
		passed    bool
		failures  []string
	}{
		{
			name:      "all pass",
			toolNames: []string{"read", "search", "write"},
			spec: ToolSpec{
				MustCall: []string{"read", "write"},
				Order:    []string{"read", "write"},
				MaxCalls: 5,
			},
			passed:   true,
			failures: nil,
		},
		{
			name:      "must_call missing",
			toolNames: []string{"search", "write"},
			spec:      ToolSpec{MustCall: []string{"read"}},
			passed:    false,
			failures:  []string{"process:must_call:read"},
		},
		{
			name:      "must_not_call hit",
			toolNames: []string{"read", "delete"},
			spec:      ToolSpec{MustNotCall: []string{"delete"}},
			passed:    false,
			failures:  []string{"process:must_not_call:delete"},
		},
		{
			name:      "order violated",
			toolNames: []string{"write", "read"},
			spec:      ToolSpec{Order: []string{"read", "write"}},
			passed:    false,
			failures:  []string{"process:order"},
		},
		{
			name:      "order across extra calls passes",
			toolNames: []string{"read", "search", "write"},
			spec:      ToolSpec{Order: []string{"read", "write"}},
			passed:    true,
			failures:  nil,
		},
		{
			name:      "max calls exceeded",
			toolNames: []string{"a", "b", "c"},
			spec:      ToolSpec{MaxCalls: 2},
			passed:    false,
			failures:  []string{"process:max_calls"},
		},
		{
			name:      "empty spec always passes",
			toolNames: []string{"read", "delete"},
			spec:      ToolSpec{},
			passed:    true,
			failures:  nil,
		},
		{
			name:      "max calls zero means unlimited",
			toolNames: []string{"a", "b", "c"},
			spec:      ToolSpec{MaxCalls: 0},
			passed:    true,
			failures:  nil,
		},
		{
			name:      "collects all failures not just first",
			toolNames: []string{"delete", "read"},
			spec: ToolSpec{
				MustCall:    []string{"write"},
				MustNotCall: []string{"delete"},
				Order:       []string{"read", "write"},
				MaxCalls:    1,
			},
			passed: false,
			failures: []string{
				"process:must_call:write",
				"process:must_not_call:delete",
				"process:order",
				"process:max_calls",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := EvaluateToolSequence(tt.toolNames, tt.spec)
			if got.Passed != tt.passed {
				t.Fatalf("expected passed=%v, got %v", tt.passed, got.Passed)
			}
			if !reflect.DeepEqual(got.Failures, tt.failures) {
				t.Fatalf("expected failures %v, got %v", tt.failures, got.Failures)
			}
		})
	}
}

func TestFormatToolSequence(t *testing.T) {
	t.Run("renders step and name with raw text", func(t *testing.T) {
		tools := []ToolObservation{
			{StepIndex: 0, ToolName: "search", RawText: "found 3 docs"},
			{StepIndex: 1, ToolName: "read", RawText: "doc-1 content"},
		}
		want := "[0] search: found 3 docs\n[1] read: doc-1 content"
		if got := FormatToolSequence(tools); got != want {
			t.Fatalf("expected %q, got %q", want, got)
		}
	})

	t.Run("omits raw text when empty", func(t *testing.T) {
		want := "[3] write"
		if got := FormatToolSequence([]ToolObservation{{StepIndex: 3, ToolName: "write"}}); got != want {
			t.Fatalf("expected %q, got %q", want, got)
		}
	})

	t.Run("empty input renders empty text", func(t *testing.T) {
		if got := FormatToolSequence(nil); got != "" {
			t.Fatalf("expected empty output, got %q", got)
		}
	})

	t.Run("truncates tool slice to max", func(t *testing.T) {
		tools := make([]ToolObservation, 0, constants.StepJudgeMaxTools+3)
		for i := 0; i < constants.StepJudgeMaxTools+3; i++ {
			tools = append(tools, ToolObservation{StepIndex: i, ToolName: "t"})
		}
		got := FormatToolSequence(tools)
		if lines := strings.Split(got, "\n"); len(lines) != constants.StepJudgeMaxTools {
			t.Fatalf("expected %d lines, got %d", constants.StepJudgeMaxTools, len(lines))
		}
	})

	t.Run("truncates raw text to max runes", func(t *testing.T) {
		long := strings.Repeat("文", constants.StepJudgeRawTextMaxChars+10)
		got := FormatToolSequence([]ToolObservation{{StepIndex: 0, ToolName: "read", RawText: long}})
		want := "[0] read: " + strings.Repeat("文", constants.StepJudgeRawTextMaxChars) + "…"
		if got != want {
			t.Fatalf("expected %d runes, got %d runes", len([]rune(want)), len([]rune(got)))
		}
		if !strings.HasSuffix(got, "…") {
			t.Fatal("expected truncated raw text to end with ellipsis")
		}
	})
}

func TestToConfigApplyConfigRoundTrip(t *testing.T) {
	t.Run("wrapped layout with tool spec and step judge", func(t *testing.T) {
		c := EvalCase{
			JudgeSpec:     &JudgeSpec{Model: "qwen-max", Rubric: "rubric"},
			ToolSpec:      &ToolSpec{MustCall: []string{"read"}, MaxCalls: 5},
			StepJudge:     &StepJudge{Criteria: "step rubric"},
			SourceTraceID: "trace-1",
		}
		cfg := c.ToConfig()
		if cfg == nil {
			t.Fatal("ToConfig returned nil for populated case")
		}
		raw, err := json.Marshal(cfg)
		if err != nil {
			t.Fatalf("marshal config: %v", err)
		}
		var got EvalCase
		got.ApplyConfig(raw)
		if got.JudgeSpec == nil || got.JudgeSpec.Model != "qwen-max" {
			t.Fatalf("judge spec not restored: %+v", got.JudgeSpec)
		}
		if got.ToolSpec == nil || !reflect.DeepEqual(got.ToolSpec.MustCall, []string{"read"}) || got.ToolSpec.MaxCalls != 5 {
			t.Fatalf("tool spec not restored: %+v", got.ToolSpec)
		}
		if got.StepJudge == nil || got.StepJudge.Criteria != "step rubric" {
			t.Fatalf("step judge not restored: %+v", got.StepJudge)
		}
		if got.SourceTraceID != "trace-1" {
			t.Fatalf("provenance not restored: %+v", got.SourceTraceID)
		}
	})

	t.Run("empty case config returns nil", func(t *testing.T) {
		if cfg := (EvalCase{}).ToConfig(); cfg != nil {
			t.Fatalf("expected nil config for empty case, got %+v", cfg)
		}
	})

	t.Run("bare judge spec fallback preserved", func(t *testing.T) {
		raw := []byte(`{"model":"qwen-max","rubric":"r"}`)
		var got EvalCase
		got.ApplyConfig(raw)
		if got.JudgeSpec == nil || got.JudgeSpec.Model != "qwen-max" {
			t.Fatalf("bare judge spec not applied: %+v", got.JudgeSpec)
		}
		if got.ToolSpec != nil || got.StepJudge != nil {
			t.Fatalf("bare layout must not set process fields: tool=%+v step=%+v", got.ToolSpec, got.StepJudge)
		}
	})

	t.Run("empty raw leaves case untouched", func(t *testing.T) {
		c := EvalCase{JudgeSpec: &JudgeSpec{Model: "keep"}}
		c.ApplyConfig(nil)
		if c.JudgeSpec == nil || c.JudgeSpec.Model != "keep" {
			t.Fatalf("existing judge spec overwritten by empty config: %+v", c.JudgeSpec)
		}
	})
}
