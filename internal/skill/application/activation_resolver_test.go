package application

import (
	"context"
	"testing"

	"github.com/byteBuilderX/stratum/internal/skill/domain"
	"github.com/byteBuilderX/stratum/internal/skill/domain/port"
	"go.uber.org/zap"
)

// seedActivation wires one skill + one revision into the fake repo so
// ResolveActivation has a deterministic fixture to resolve against.
func seedActivation(repo *fakeVersionRepo, skill port.SkillProductRow, rev domain.SkillRevision) {
	repo.skills[skill.ID] = skill
	repo.revisions[rev.ID] = rev
}

func TestResolveActivation_ActiveRevisionFallbackAndNameFallback(t *testing.T) {
	repo := newFakeVersionRepo()
	seedActivation(repo,
		port.SkillProductRow{ID: "sk1", Name: "投诉分类", Description: "产品描述", ActiveRevisionID: "rev1"},
		domain.SkillRevision{
			ID: "rev1", SkillID: "sk1", Status: domain.VersionStatusPublished,
			Instructions:       "先判断投诉类别",
			ActivationContract: domain.ActivationContract{}, // 空 → 回退到 skill 名称/描述
			Requirements:       domain.Requirements{MCPToolIDs: []string{"mcp:orders:get"}},
		},
	)
	svc := NewVersionService(repo, zap.NewNop())

	// 空 revisionID → 解析 active revision。
	view, found, err := svc.ResolveActivation(context.Background(), "sk1", "")
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("expected active revision to be found")
	}
	if view.RevisionID != "rev1" {
		t.Fatalf("want rev1, got %q", view.RevisionID)
	}
	// contract 为空 → name/description 回退到 skill product。
	if view.Name != "投诉分类" || view.Description != "产品描述" {
		t.Fatalf("name/description fallback failed: %#v", view)
	}
	if len(view.MCPToolIDs) != 1 || view.MCPToolIDs[0] != "mcp:orders:get" {
		t.Fatalf("requirements not projected: %#v", view)
	}
}

func TestResolveActivation_ContractNameTakesPrecedence(t *testing.T) {
	repo := newFakeVersionRepo()
	seedActivation(repo,
		port.SkillProductRow{ID: "sk1", Name: "产品名", Description: "产品描述", ActiveRevisionID: "rev1"},
		domain.SkillRevision{
			ID: "rev1", SkillID: "sk1", Status: domain.VersionStatusCandidate,
			ActivationContract: domain.ActivationContract{Name: "classify_complaint", Description: "契约描述"},
		},
	)
	svc := NewVersionService(repo, zap.NewNop())

	view, found, err := svc.ResolveActivation(context.Background(), "sk1", "rev1")
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("candidate revision must be activatable")
	}
	if view.Name != "classify_complaint" || view.Description != "契约描述" {
		t.Fatalf("contract name/description must take precedence: %#v", view)
	}
}

func TestResolveActivation_DraftRevisionNotActivatable(t *testing.T) {
	repo := newFakeVersionRepo()
	seedActivation(repo,
		port.SkillProductRow{ID: "sk1", Name: "产品名", ActiveRevisionID: "rev1"},
		domain.SkillRevision{ID: "rev1", SkillID: "sk1", Status: domain.VersionStatusDraft},
	)
	svc := NewVersionService(repo, zap.NewNop())

	_, found, err := svc.ResolveActivation(context.Background(), "sk1", "rev1")
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatal("draft revision must not be activatable (status filter)")
	}
}

func TestResolveActivation_MissingSkill(t *testing.T) {
	svc := NewVersionService(newFakeVersionRepo(), zap.NewNop())
	_, found, err := svc.ResolveActivation(context.Background(), "ghost", "")
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatal("missing skill must yield found=false")
	}
}
