package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/skill/domain"
)

type SkillProductRow struct {
	ID              string
	Name            string
	Description     string
	Status          string
	ActiveVersionID string
	DraftVersionID  string
}

type SkillTestCaseRow struct {
	ID             string
	SkillID        string
	Name           string
	Input          any
	ExpectedOutput any
	AssertionMode  string
	Enabled        bool
}

type VersionRepo interface {
	InsertSkillWithDraft(ctx context.Context, skill SkillProductRow, draft domain.SkillVersion, firstCase SkillTestCaseRow) error
	GetSkill(ctx context.Context, skillID string) (SkillProductRow, bool, error)
	GetDraftVersion(ctx context.Context, skillID string) (domain.SkillVersion, bool, error)
	GetActiveVersion(ctx context.Context, skillID string) (domain.SkillVersion, bool, error)
	GetVersion(ctx context.Context, skillID, versionID string) (domain.SkillVersion, bool, error)
	InsertCandidate(ctx context.Context, candidate domain.SkillVersion) error
	UpdateDraftCapability(ctx context.Context, skillID string, capability domain.Capability, contentHash string) (domain.SkillVersion, error)
	UpdateDraftContract(ctx context.Context, skillID string, contract domain.ToolContract, contentHash string) (domain.SkillVersion, error)
	UpdateDraftImplementation(ctx context.Context, skillID string, implementation domain.Implementation, contentHash string) (domain.SkillVersion, error)
	CountEnabledTestCases(ctx context.Context, skillID string) (int, error)
	PublishDraft(ctx context.Context, skillID, draftVersionID string, nextVersionNo int, baseline map[string]any) (domain.SkillVersion, error)
	NextVersionNo(ctx context.Context, skillID string) (int, error)
}
