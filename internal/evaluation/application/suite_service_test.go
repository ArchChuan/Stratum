package application

import (
	"context"
	"testing"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
)

func TestSuiteServiceCreatesDraftAndPublishesImmutableRevision(t *testing.T) {
	repo := &fakeSuiteRepo{}
	svc := NewSuiteService(repo)

	suite, revision, err := svc.Create(context.Background(), "tenant-1", CreateSuiteInput{
		Name: "投诉分类基线", ResourceKind: domain.ResourceKindSkill,
		Cases: []domain.EvalCase{{Name: "物流", Input: "快递没更新", ExpectedOutput: "物流", AssertionMode: domain.AssertionContains, Enabled: true}},
	})
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if revision.Status != domain.SuiteRevisionDraft || suite.DraftRevisionID != revision.ID || revision.Cases[0].ID == "" {
		t.Fatalf("unexpected draft: suite=%+v revision=%+v", suite, revision)
	}

	published, err := svc.Publish(context.Background(), "tenant-1", suite.ID)
	if err != nil {
		t.Fatalf("Publish returned error: %v", err)
	}
	if published.Status != domain.SuiteRevisionPublished || published.VersionNo != 1 {
		t.Fatalf("unexpected published revision: %+v", published)
	}
}

type fakeSuiteRepo struct {
	suite    domain.EvalSuite
	revision domain.EvalSuiteRevision
}

func (f *fakeSuiteRepo) CreateSuite(_ context.Context, _ string, suite domain.EvalSuite, revision domain.EvalSuiteRevision) error {
	f.suite, f.revision = suite, revision
	return nil
}

func (f *fakeSuiteRepo) GetDraftRevision(_ context.Context, _ string, suiteID string) (domain.EvalSuiteRevision, bool, error) {
	return f.revision, f.revision.SuiteID == suiteID && f.revision.Status == domain.SuiteRevisionDraft, nil
}

func (f *fakeSuiteRepo) PublishRevision(_ context.Context, _ string, suiteID, revisionID string, versionNo int) (domain.EvalSuiteRevision, error) {
	f.revision.Status = domain.SuiteRevisionPublished
	f.revision.VersionNo = versionNo
	f.suite.ActiveRevisionID = revisionID
	f.suite.DraftRevisionID = ""
	return f.revision, nil
}

func (f *fakeSuiteRepo) NextVersionNo(_ context.Context, _ string, _ string) (int, error) {
	return 1, nil
}

func (f *fakeSuiteRepo) GetRevision(_ context.Context, _ string, revisionID string) (domain.EvalSuiteRevision, bool, error) {
	return f.revision, f.revision.ID == revisionID, nil
}
