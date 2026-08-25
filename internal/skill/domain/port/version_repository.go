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
	// InsertSkill persists the skill product row, its first published revision
	// and, when editors is non-empty, the granted editor set — all in the same
	// transaction. Each editor must hold role admin/owner at write time.
	InsertSkill(ctx context.Context, skill SkillProductRow, revision domain.SkillRevision, audit *auditdomain.ResourceChangeAuditEvent, editors []string) error
	GetSkill(ctx context.Context, skillID string) (SkillProductRow, bool, error)
	ListSkills(ctx context.Context) ([]SkillProductRow, error)
	DeleteSkill(ctx context.Context, skillID string, audit *auditdomain.ResourceChangeAuditEvent) error
	GetActiveRevision(ctx context.Context, skillID string) (domain.SkillRevision, bool, error)
	GetRevision(ctx context.Context, skillID, revisionID string) (domain.SkillRevision, bool, error)
	InsertCandidate(ctx context.Context, candidate domain.SkillRevision, audit *auditdomain.ResourceChangeAuditEvent) error
	// SaveRevision persists a new published revision derived from the current
	// active revision, demotes the previous active revision to deprecated and
	// repoints the skill's active_revision_id — all in the same transaction.
	// expectedContentHash guards concurrent edits (409 on mismatch). editorActor,
	// when non-empty, re-validates the actor's editor qualification (tenant
	// membership AND presence in resource_editors) inside the write transaction,
	// closing the check-then-write TOCTOU window for editor-granted updates.
	SaveRevision(ctx context.Context, skillID, expectedContentHash string, skill SkillProductRow, revision domain.SkillRevision, audit *auditdomain.ResourceChangeAuditEvent, editorActor string) (domain.SkillRevision, error)
	// ListRevisions returns the skill's version history newest-first, with
	// IsCurrent marking the version the skill's active_revision_id points to.
	// The bool is false when the skill does not exist.
	ListRevisions(ctx context.Context, skillID string) ([]domain.SkillRevision, bool, error)
	// RollbackRevision repoints the skill's active_revision_id to a historical
	// published revision (deprecated → published), demoting the current active
	// to deprecated, and records an audit event. It does not create a new version.
	RollbackRevision(ctx context.Context, skillID, targetRevisionID, actorID string, audit *auditdomain.ResourceChangeAuditEvent) error
	NextRevisionNo(ctx context.Context, skillID string) (int, error)
}
