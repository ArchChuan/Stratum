package skilladapter

import (
	"context"
	"testing"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
)

func TestAdapterExecutesRequestedSkillRevisionAndUnwrapsContent(t *testing.T) {
	executor := &fakeVersionExecutor{output: map[string]any{"content": "分类结果：物流问题"}}
	adapter := New(executor)

	result, err := adapter.ExecuteRevision(context.Background(), "tenant-1", domain.ResourceRef{
		Kind: domain.ResourceKindSkill, ResourceID: "skill-1", RevisionID: "version-2",
	}, domain.EvalCase{ID: "case-1", Input: map[string]any{"input": "快递没更新"}})
	if err != nil {
		t.Fatalf("ExecuteRevision returned error: %v", err)
	}
	if result.Output != "分类结果：物流问题" || result.TraceID == "" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if executor.versionID != "version-2" {
		t.Fatalf("expected version-2, got %q", executor.versionID)
	}
	tenant, ok := postgres.FromContext(executor.ctx)
	if !ok || tenant.TenantID != "tenant-1" {
		t.Fatalf("tenant context not propagated: %#v", tenant)
	}
}

type fakeVersionExecutor struct {
	ctx       context.Context
	versionID string
	output    any
}

func (f *fakeVersionExecutor) ExecuteVersion(ctx context.Context, versionID string, _ any) (any, error) {
	f.ctx = ctx
	f.versionID = versionID
	return f.output, nil
}
