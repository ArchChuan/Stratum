package domain

import "testing"

// TestResourceChangeVocabulary 锁定共享枚举集合：六类资源 kind + 全部 operation
// 必须存在且互异。前端 RESOURCE_KIND_OPTIONS 与读取端筛选引用同一组值。
func TestResourceChangeVocabulary(t *testing.T) {
	kinds := []string{
		ResourceKindAgent, ResourceKindSkill, ResourceKindMCP, ResourceKindKnowledge,
		ResourceKindWorkflow, ResourceKindEvaluation,
	}
	seenKind := map[string]bool{}
	for _, k := range kinds {
		if k == "" {
			t.Fatalf("resource kind must not be empty")
		}
		if seenKind[k] {
			t.Fatalf("duplicate resource kind %q", k)
		}
		seenKind[k] = true
	}

	ops := []string{
		ChangeOpCreate, ChangeOpUpdate, ChangeOpDelete,
		ChangeOpPublish, ChangeOpPromote, ChangeOpRollback,
		ChangeOpReject, ChangeOpPause, ChangeOpActivate,
	}
	seenOp := map[string]bool{}
	for _, op := range ops {
		if op == "" {
			t.Fatalf("operation must not be empty")
		}
		if seenOp[op] {
			t.Fatalf("duplicate operation %q", op)
		}
		seenOp[op] = true
	}
}
