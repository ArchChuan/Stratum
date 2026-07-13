package application

import (
	"context"
	"testing"

	"github.com/byteBuilderX/stratum/internal/skill/domain"
	"github.com/byteBuilderX/stratum/internal/skill/domain/port"
	"go.uber.org/zap"
)

func TestVersionServiceCreateSkillCreatesDraftVersionAndTestCase(t *testing.T) {
	repo := newFakeVersionRepo()
	svc := NewVersionService(repo, zap.NewNop())

	view, err := svc.CreateSkillDraft(context.Background(), CreateSkillDraftInput{
		Name:           "投诉分类",
		Goal:           "判断客户投诉类型",
		WhenToUse:      "用户表达投诉时",
		SampleInput:    "快递没更新",
		ExpectedOutput: "物流问题",
	})
	if err != nil {
		t.Fatalf("CreateSkillDraft() error = %v", err)
	}
	if view.Skill.Name != "投诉分类" {
		t.Fatalf("expected skill name, got %q", view.Skill.Name)
	}
	if view.Draft.Capability.Goal != "判断客户投诉类型" {
		t.Fatalf("expected capability goal, got %q", view.Draft.Capability.Goal)
	}
	if view.Draft.ToolContract.ToolName == "" {
		t.Fatal("expected generated tool name")
	}
	if len(repo.testCases) != 1 {
		t.Fatalf("expected first test case, got %d", len(repo.testCases))
	}
}

func TestVersionServicePublishDraftRequiresPublishableVersion(t *testing.T) {
	repo := newFakeVersionRepo()
	svc := NewVersionService(repo, zap.NewNop())
	view, err := svc.CreateSkillDraft(context.Background(), CreateSkillDraftInput{
		Name:           "投诉分类",
		Goal:           "判断客户投诉类型",
		WhenToUse:      "用户表达投诉时",
		SampleInput:    "快递没更新",
		ExpectedOutput: "物流问题",
	})
	if err != nil {
		t.Fatal(err)
	}
	draft := repo.versions[view.Draft.ID]
	draft.ToolContract.Confirmed = false
	repo.versions[view.Draft.ID] = draft

	if _, err := svc.PublishDraft(context.Background(), view.Skill.ID); err == nil {
		t.Fatal("expected unconfirmed contract to block publish")
	}
}

func TestVersionServiceUpdateCapabilityUpdatesCurrentDraft(t *testing.T) {
	repo := newFakeVersionRepo()
	svc := NewVersionService(repo, zap.NewNop())
	view, err := svc.CreateSkillDraft(context.Background(), CreateSkillDraftInput{
		Name:           "投诉分类",
		Goal:           "判断客户投诉类型",
		WhenToUse:      "用户表达投诉时",
		SampleInput:    "快递没更新",
		ExpectedOutput: "物流问题",
	})
	if err != nil {
		t.Fatal(err)
	}

	updated, err := svc.UpdateCapability(context.Background(), view.Skill.ID, UpdateCapabilityInput{
		Goal:       "识别投诉类别并给出动作",
		WhenToUse:  "用户描述售后问题时",
		InputSpec:  "投诉文本",
		OutputSpec: "类别、理由、动作",
	})
	if err != nil {
		t.Fatalf("UpdateCapability() error = %v", err)
	}
	if updated.Capability.Goal != "识别投诉类别并给出动作" {
		t.Fatalf("expected updated goal, got %q", updated.Capability.Goal)
	}
	if updated.Capability.InputSpec != "投诉文本" {
		t.Fatalf("expected input spec, got %q", updated.Capability.InputSpec)
	}
}

func TestVersionServiceUpdateContractCanConfirmDraftForPublish(t *testing.T) {
	repo := newFakeVersionRepo()
	svc := NewVersionService(repo, zap.NewNop())
	view, err := svc.CreateSkillDraft(context.Background(), CreateSkillDraftInput{
		Name:           "complaint",
		Goal:           "判断客户投诉类型",
		WhenToUse:      "用户表达投诉时",
		SampleInput:    "快递没更新",
		ExpectedOutput: "物流问题",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = svc.UpdateContract(context.Background(), view.Skill.ID, UpdateContractInput{
		ToolName:     "classify_complaint",
		Description:  "判断客户投诉类型",
		InputSchema:  map[string]any{"type": "object"},
		OutputSchema: map[string]any{"type": "object"},
		Confirmed:    true,
	})
	if err != nil {
		t.Fatalf("UpdateContract() error = %v", err)
	}
	published, err := svc.PublishDraft(context.Background(), view.Skill.ID)
	if err != nil {
		t.Fatalf("PublishDraft() error = %v", err)
	}
	if published.Status != domain.VersionStatusPublished {
		t.Fatalf("expected published status, got %q", published.Status)
	}
}

func TestVersionServiceUpdateImplementationUpdatesCurrentDraft(t *testing.T) {
	repo := newFakeVersionRepo()
	svc := NewVersionService(repo, zap.NewNop())
	view, err := svc.CreateSkillDraft(context.Background(), CreateSkillDraftInput{
		Name:           "complaint",
		Goal:           "判断客户投诉类型",
		WhenToUse:      "用户表达投诉时",
		SampleInput:    "快递没更新",
		ExpectedOutput: "物流问题",
	})
	if err != nil {
		t.Fatal(err)
	}

	updated, err := svc.UpdateImplementation(context.Background(), view.Skill.ID, UpdateImplementationInput{
		Mode:    "prompt",
		Source:  map[string]any{"promptTemplate": "新的实现：{{.input}}"},
		Runtime: map[string]any{"timeoutSec": 30},
	})
	if err != nil {
		t.Fatalf("UpdateImplementation() error = %v", err)
	}
	if updated.Implementation.Source["promptTemplate"] != "新的实现：{{.input}}" {
		t.Fatalf("expected implementation update, got %#v", updated.Implementation.Source)
	}
}

func TestVersionServiceGetWorkspaceFallsBackToActiveVersionAfterPublish(t *testing.T) {
	repo := newFakeVersionRepo()
	svc := NewVersionService(repo, zap.NewNop())
	view, err := svc.CreateSkillDraft(context.Background(), CreateSkillDraftInput{
		Name:           "complaint",
		Goal:           "判断客户投诉类型",
		WhenToUse:      "用户表达投诉时",
		SampleInput:    "快递没更新",
		ExpectedOutput: "物流问题",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateContract(context.Background(), view.Skill.ID, UpdateContractInput{
		ToolName:     "classify_complaint",
		Description:  "判断客户投诉类型",
		InputSchema:  map[string]any{"type": "object"},
		OutputSchema: map[string]any{"type": "object"},
		Confirmed:    true,
	}); err != nil {
		t.Fatal(err)
	}
	published, err := svc.PublishDraft(context.Background(), view.Skill.ID)
	if err != nil {
		t.Fatal(err)
	}

	workspace, err := svc.GetWorkspace(context.Background(), view.Skill.ID)
	if err != nil {
		t.Fatalf("GetWorkspace() after publish error = %v", err)
	}
	if workspace.Draft.ID != published.ID || workspace.Draft.Status != domain.VersionStatusPublished {
		t.Fatalf("expected active version fallback, got %#v", workspace.Draft)
	}
}

type fakeVersionRepo struct {
	skills    map[string]port.SkillProductRow
	versions  map[string]domain.SkillVersion
	testCases map[string]port.SkillTestCaseRow
}

func newFakeVersionRepo() *fakeVersionRepo {
	return &fakeVersionRepo{
		skills:    map[string]port.SkillProductRow{},
		versions:  map[string]domain.SkillVersion{},
		testCases: map[string]port.SkillTestCaseRow{},
	}
}

func (r *fakeVersionRepo) InsertSkillWithDraft(
	_ context.Context,
	skill port.SkillProductRow,
	draft domain.SkillVersion,
	firstCase port.SkillTestCaseRow,
) error {
	r.skills[skill.ID] = skill
	r.versions[draft.ID] = draft
	r.testCases[firstCase.ID] = firstCase
	return nil
}

func (r *fakeVersionRepo) GetSkill(_ context.Context, skillID string) (port.SkillProductRow, bool, error) {
	skill, ok := r.skills[skillID]
	return skill, ok, nil
}

func (r *fakeVersionRepo) GetDraftVersion(_ context.Context, skillID string) (domain.SkillVersion, bool, error) {
	for _, version := range r.versions {
		if version.SkillID == skillID && version.Status == domain.VersionStatusDraft {
			return version, true, nil
		}
	}
	return domain.SkillVersion{}, false, nil
}

func (r *fakeVersionRepo) GetActiveVersion(_ context.Context, skillID string) (domain.SkillVersion, bool, error) {
	skill, ok := r.skills[skillID]
	if !ok || skill.ActiveVersionID == "" {
		return domain.SkillVersion{}, false, nil
	}
	version, ok := r.versions[skill.ActiveVersionID]
	return version, ok, nil
}

func (r *fakeVersionRepo) UpdateDraftCapability(_ context.Context, skillID string, capability domain.Capability) (domain.SkillVersion, error) {
	for id, version := range r.versions {
		if version.SkillID == skillID && version.Status == domain.VersionStatusDraft {
			version.Capability = capability
			r.versions[id] = version
			return version, nil
		}
	}
	return domain.SkillVersion{}, domain.ErrSkillNotFound
}

func (r *fakeVersionRepo) UpdateDraftContract(_ context.Context, skillID string, contract domain.ToolContract) (domain.SkillVersion, error) {
	for id, version := range r.versions {
		if version.SkillID == skillID && version.Status == domain.VersionStatusDraft {
			version.ToolContract = contract
			r.versions[id] = version
			return version, nil
		}
	}
	return domain.SkillVersion{}, domain.ErrSkillNotFound
}

func (r *fakeVersionRepo) UpdateDraftImplementation(_ context.Context, skillID string, implementation domain.Implementation) (domain.SkillVersion, error) {
	for id, version := range r.versions {
		if version.SkillID == skillID && version.Status == domain.VersionStatusDraft {
			version.Implementation = implementation
			r.versions[id] = version
			return version, nil
		}
	}
	return domain.SkillVersion{}, domain.ErrSkillNotFound
}

func (r *fakeVersionRepo) CountEnabledTestCases(_ context.Context, skillID string) (int, error) {
	count := 0
	for _, tc := range r.testCases {
		if tc.SkillID == skillID && tc.Enabled {
			count++
		}
	}
	return count, nil
}

func (r *fakeVersionRepo) PublishDraft(
	_ context.Context,
	skillID string,
	draftVersionID string,
	nextVersionNo int,
	baseline map[string]any,
) (domain.SkillVersion, error) {
	version := r.versions[draftVersionID]
	version.Status = domain.VersionStatusPublished
	version.VersionNo = nextVersionNo
	version.TestBaseline = baseline
	r.versions[draftVersionID] = version

	skill := r.skills[skillID]
	skill.ActiveVersionID = draftVersionID
	skill.DraftVersionID = ""
	skill.Status = "published"
	r.skills[skillID] = skill
	return version, nil
}

func (r *fakeVersionRepo) NextVersionNo(_ context.Context, skillID string) (int, error) {
	next := 1
	for _, version := range r.versions {
		if version.SkillID == skillID && version.VersionNo >= next {
			next = version.VersionNo + 1
		}
	}
	return next, nil
}
