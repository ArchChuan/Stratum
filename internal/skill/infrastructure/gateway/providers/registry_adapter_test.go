package providers

import (
	"context"
	"testing"

	"go.uber.org/zap"
)

func TestBuildSkillFromImplementationPrompt(t *testing.T) {
	impl := map[string]any{
		"mode": "prompt",
		"source": map[string]any{
			"promptTemplate": "收到：{{.input}}",
		},
	}

	skill, err := buildSkillFromImplementation("version-1", "投诉分类", "判断投诉", impl, zap.NewNop(), nil)
	if err != nil {
		t.Fatalf("buildSkillFromImplementation() error = %v", err)
	}
	executor, ok := skill.(interface {
		Execute(context.Context, interface{}) (interface{}, error)
	})
	if !ok {
		t.Fatalf("expected executable skill, got %T", skill)
	}
	out, err := executor.Execute(context.Background(), map[string]any{"prompt": "快递没更新", "input": "快递没更新"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	result := out.(map[string]any)
	if result["content"] != "收到：快递没更新" {
		t.Fatalf("expected rendered prompt, got %#v", result["content"])
	}
}
