package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/skill/domain"
)

type SkillProductRow struct {
	ID               string
	Name             string
	Description      string
	Status           string
	ActiveRevisionID string
	DraftRevisionID  string
}

type VersionRepo interface {
	InsertSkillWithDraft(ctx context.Context, skill SkillProductRow, draft domain.SkillRevision) error
	GetSkill(ctx context.Context, skillID string) (SkillProductRow, bool, error)
	ListSkills(ctx context.Context) ([]SkillProductRow, error)
	DeleteSkill(ctx context.Context, skillID string) error
	GetDraftRevision(ctx context.Context, skillID string) (domain.SkillRevision, bool, error)
	GetActiveRevision(ctx context.Context, skillID string) (domain.SkillRevision, bool, error)
	GetRevision(ctx context.Context, skillID, revisionID string) (domain.SkillRevision, bool, error)
	InsertCandidate(ctx context.Context, candidate domain.SkillRevision) error
	UpdateDraftCapability(ctx context.Context, skillID string, capability domain.Capability, contentHash string) (domain.SkillRevision, error)
	UpdateDraftActivation(ctx context.Context, skillID string, contract domain.ActivationContract, contentHash string) (domain.SkillRevision, error)
	UpdateDraftInstructions(ctx context.Context, skillID, instructions string, requirements domain.Requirements, contentHash string) (domain.SkillRevision, error)
	PublishDraft(ctx context.Context, skillID, draftRevisionID string, nextRevisionNo int, checks map[string]any) (domain.SkillRevision, error)
	NextRevisionNo(ctx context.Context, skillID string) (int, error)
}
