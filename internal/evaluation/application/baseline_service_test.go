package application

import (
	"context"
	"testing"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
)

func TestBaselineServiceDispatchesMCPWithTenant(t *testing.T) {
	creator := &fakeBaselineCreator{}
	service := NewBaselineService(creator)
	ref, err := service.CreatePublishedBaseline(context.Background(), "tenant-1", domain.ResourceKindMCP, "server-1")
	if err != nil || creator.tenantID != "tenant-1" || creator.kind != domain.ResourceKindMCP ||
		ref.ResourceID != "server-1" {
		t.Fatalf("ref=%+v creator=%+v err=%v", ref, creator, err)
	}
}

type fakeBaselineCreator struct {
	tenantID string
	kind     domain.ResourceKind
}

func (f *fakeBaselineCreator) CreatePublishedBaseline(
	_ context.Context, tenantID string, kind domain.ResourceKind, resourceID string,
) (domain.ResourceRef, error) {
	f.tenantID, f.kind = tenantID, kind
	return domain.ResourceRef{Kind: kind, ResourceID: resourceID, RevisionID: "published-1"}, nil
}
