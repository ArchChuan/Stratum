package port

import (
	"context"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/internal/skill/domain"
)

type SkillProductRow struct {
	ID               string
	Name             string
	Description      string
	Status           string
	ActiveRevisionID string
	DraftRevisionID  string
	// CreatedBy is the user who created the skill product row ("" for historical/builtin rows).
	CreatedBy string
}

type VersionRepo interface {
	InsertSkillWithDraft(ctx context.Context, skill SkillProductRow, draft domain.SkillRevision, audit *auditdomain.ResourceChangeAuditEvent) error
	GetSkill(ctx context.Context, skillID string) (SkillProductRow, bool, error)
	ListSkills(ctx context.Context) ([]SkillProductRow, error)
	DeleteSkill(ctx context.Context, skillID string, audit *auditdomain.ResourceChangeAuditEvent) error
	GetDraftRevision(ctx context.Context, skillID string) (domain.SkillRevision, bool, error)
	GetActiveRevision(ctx context.Context, skillID string) (domain.SkillRevision, bool, error)
	GetRevision(ctx context.Context, skillID, revisionID string) (domain.SkillRevision, bool, error)
	InsertCandidate(ctx context.Context, candidate domain.SkillRevision, audit *auditdomain.ResourceChangeAuditEvent) error
	UpdateDraftCapability(ctx context.Context, skillID string, capability domain.Capability, contentHash string, audit *auditdomain.ResourceChangeAuditEvent) (domain.SkillRevision, error)
	UpdateDraftActivation(ctx context.Context, skillID string, contract domain.ActivationContract, contentHash string, audit *auditdomain.ResourceChangeAuditEvent) (domain.SkillRevision, error)
	UpdateDraftInstructions(ctx context.Context, skillID, instructions string, requirements domain.Requirements, contentHash string, audit *auditdomain.ResourceChangeAuditEvent) (domain.SkillRevision, error)
	UpdateDraftBundle(ctx context.Context, skillID, expectedContentHash string, skill SkillProductRow, draft domain.SkillRevision, audit *auditdomain.ResourceChangeAuditEvent) (domain.SkillRevision, error)
	PublishDraft(ctx context.Context, skillID, draftRevisionID string, nextRevisionNo int, checks map[string]any, audit *auditdomain.ResourceChangeAuditEvent) (domain.SkillRevision, error)
	NextRevisionNo(ctx context.Context, skillID string) (int, error)
}
