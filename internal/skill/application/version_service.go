package application

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	auditport "github.com/byteBuilderX/stratum/internal/audit/domain/port"
	"github.com/byteBuilderX/stratum/internal/skill/domain"
	"github.com/byteBuilderX/stratum/internal/skill/domain/port"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type SkillProduct = port.SkillProductRow
type SkillRevision = domain.SkillRevision

type CreateSkillInput struct {
	Name         string
	Description  string
	Instructions string
	// ActorID is the caller; becomes the skill's created_by (owner/admin only).
	ActorID string
	// Editors are additional members granted update rights (whitelist), persisted
	// in the same transaction as the skill. Each must be an active tenant member.
	Editors []string
}

type SkillWorkspaceView struct {
	Skill SkillProduct
	// Active is the currently effective revision; empty when a legacy
	// unpublished skill (no active_revision_id) is still to produce its first version.
	Active  domain.SkillRevision
	Editors []string
}

type SaveRevisionInput struct {
	Name         string
	Description  string
	Instructions string
	// ActorID is the caller; ownership is checked against the skill's created_by.
	ActorID string
}

type CandidateInput struct {
	Source             string
	PromptPatch        map[string]any
	GenerationMetadata map[string]any
}

type VersionService struct {
	repo         port.VersionRepo
	logger       *zap.Logger
	roles        port.TenantRoleResolver
	editorRepo   port.SkillResourceEditorRepo
	failureAudit auditport.FailureAuditRecorder
}

func NewVersionService(repo port.VersionRepo, logger *zap.Logger) *VersionService {
	return &VersionService{repo: repo, logger: logger}
}

// SetTenantRoleResolver injects the tenant role resolver used by ownership
// checks. A nil resolver fails all writes closed (ownership unverifiable).
func (s *VersionService) SetTenantRoleResolver(r port.TenantRoleResolver) { s.roles = r }

// SetEditorRepo injects the resource-editor repository backing the
// admin-editor row of the ownership matrix. A nil repo denies every
// editor-granted update (fail closed).
func (s *VersionService) SetEditorRepo(r port.SkillResourceEditorRepo) { s.editorRepo = r }

// SetFailureAuditRecorder 注入失败资源操作审计。未注入时跳过记录。
func (s *VersionService) SetFailureAuditRecorder(r auditport.FailureAuditRecorder) {
	s.failureAudit = r
}

func (s *VersionService) CreateSkill(ctx context.Context, in CreateSkillInput) (SkillWorkspaceView, error) {
	// create encodes "the creator owns the resource": only owner/admin may create.
	if err := s.checkOwnership(ctx, in.ActorID, in.ActorID, nil, OpEdit); err != nil {
		return SkillWorkspaceView{}, err
	}
	skillID := uuid.Must(uuid.NewV7()).String()
	revisionID := uuid.Must(uuid.NewV7()).String()
	revision := domain.SkillRevision{
		ID:                 revisionID,
		SkillID:            skillID,
		RevisionNo:         1,
		Status:             domain.VersionStatusPublished,
		Source:             "manual",
		GenerationMetadata: map[string]any{},
		Name:               strings.TrimSpace(in.Name),
		Description:        strings.TrimSpace(in.Description),
		Instructions:       strings.TrimSpace(in.Instructions),
		CreatedBy:          in.ActorID,
	}
	contentHash, err := revision.ComputeContentHash()
	if err != nil {
		return SkillWorkspaceView{}, err
	}
	revision.ContentHash = contentHash
	skill := port.SkillProductRow{
		ID:               skillID,
		Name:             strings.TrimSpace(in.Name),
		Description:      strings.TrimSpace(in.Description),
		Status:           string(domain.VersionStatusPublished),
		ActiveRevisionID: revisionID,
		CreatedBy:        in.ActorID,
	}
	audit, err := newChangeAudit(ctx, auditdomain.ResourceKindSkill, skillID, auditdomain.ChangeOpCreate,
		in.ActorID, nil, skillSafeProjection(skill, &revision))
	if err != nil {
		return SkillWorkspaceView{}, err
	}
	if err := s.repo.InsertSkill(ctx, skill, revision, audit, in.Editors); err != nil {
		s.recordFailure(ctx, skillID, "create", err)
		return SkillWorkspaceView{}, err
	}
	s.logger.Info("skill created", zap.String("skill_id", skillID), zap.String("revision_id", revisionID))
	// Editors must be non-nil: JSON renders a nil slice as null, and the
	// frontend schema default only covers a missing field, not null.
	return SkillWorkspaceView{Skill: skill, Active: revision, Editors: []string{}}, nil
}

// recordFailure 旁路记录一次失败的 skill 创建/保存/回滚（best-effort）。
// 记录失败仅 WARN，不改变主流程错误。
func (s *VersionService) recordFailure(ctx context.Context, skillID, op string, err error) {
	if s.failureAudit == nil {
		return
	}
	if recordErr := s.failureAudit.Record(ctx, auditport.ResourceFailure{
		ResourceKind: auditdomain.ResourceKindSkill,
		ResourceID:   skillID,
		Operation:    op,
		ErrorCode:    auditport.ClassifyFailure(err),
	}); recordErr != nil {
		s.logger.Warn("failed to record skill failure audit",
			zap.String("skill_id", skillID),
			zap.String("op", op),
			zap.Error(recordErr))
	}
}

func (s *VersionService) GetWorkspace(ctx context.Context, skillID, actorID string) (SkillWorkspaceView, error) {
	skill, ok, err := s.repo.GetSkill(ctx, skillID)
	if err != nil {
		return SkillWorkspaceView{}, err
	}
	if !ok {
		return SkillWorkspaceView{}, domain.ErrSkillNotFound
	}
	var editors []string
	if s.editorRepo != nil {
		editors, err = s.editorRepo.ListEditors(ctx, reqctx.TenantIDFromContext(ctx), skillID)
		if err != nil {
			return SkillWorkspaceView{}, fmt.Errorf("skill service get workspace: list editors: %w", err)
		}
	}
	view := SkillWorkspaceView{Skill: skill, Editors: editors}
	// 存量未发布 skill(无 active_revision_id)兜底返回空 Active,前端可编辑,
	// 首次保存生成第一版。
	active, ok, err := s.repo.GetActiveRevision(ctx, skillID)
	if err != nil {
		return SkillWorkspaceView{}, err
	}
	if ok {
		view.Active = active
	}
	return view, nil
}

func (s *VersionService) ListSkills(ctx context.Context) ([]SkillProduct, error) {
	return s.repo.ListSkills(ctx)
}

func (s *VersionService) DeleteSkill(ctx context.Context, skillID, actorID string) error {
	skill, ok, err := s.repo.GetSkill(ctx, skillID)
	if err != nil {
		return err
	}
	if !ok {
		return domain.ErrSkillNotFound
	}
	// Delete stays creator/owner-only: editors do not grant delete rights.
	if err := s.checkOwnership(ctx, actorID, skill.CreatedBy, nil, OpDelete); err != nil {
		return err
	}
	active, ok, err := s.repo.GetActiveRevision(ctx, skillID)
	if err != nil {
		return err
	}
	var activePtr *domain.SkillRevision
	if ok {
		activePtr = &active
	}
	audit, err := newChangeAudit(ctx, auditdomain.ResourceKindSkill, skillID, auditdomain.ChangeOpDelete, actorID,
		skillSafeProjection(skill, activePtr), nil)
	if err != nil {
		return err
	}
	return s.repo.DeleteSkill(ctx, skillID, audit)
}

// SetEditors replaces the granted editor set of a skill resource. Tenant
// owner/admin manage the whitelist by default (OpAccess — 申请通道审批入口);
// members can never grant editors. Each editor must hold role admin/owner at
// write time, enforced inside the repository transaction (fail closed). The
// change is audited in the same transaction with before/after projections.
func (s *VersionService) SetEditors(ctx context.Context, skillID, actorID string, editorIDs []string) error {
	if s.editorRepo == nil {
		return fmt.Errorf("skill service set editors: editor repo not wired")
	}
	skill, ok, err := s.repo.GetSkill(ctx, skillID)
	if err != nil {
		return fmt.Errorf("skill service set editors: %w", err)
	}
	if !ok {
		return domain.ErrSkillNotFound
	}
	if err := s.checkOwnership(ctx, actorID, skill.CreatedBy, nil, OpAccess); err != nil {
		return err
	}
	tenantID := reqctx.TenantIDFromContext(ctx)
	before, err := s.editorRepo.ListEditors(ctx, tenantID, skillID)
	if err != nil {
		return fmt.Errorf("skill service set editors: list editors: %w", err)
	}
	audit, err := buildSkillEditorAudit(ctx, s.repo, skill, skillID, actorID, before, editorIDs)
	if err != nil {
		return err
	}
	if err := s.editorRepo.ReplaceEditors(ctx, tenantID, skillID, editorIDs, actorID, audit); err != nil {
		return err
	}
	s.logger.Info("skill editors updated", zap.String("skill_id", skillID), zap.Int("count", len(editorIDs)))
	return nil
}

// buildSkillEditorAudit renders before/after projections for the editors
// management endpoint. The projection carries the active revision (or the
// product row only when none exists).
func buildSkillEditorAudit(ctx context.Context, repo port.VersionRepo, skill port.SkillProductRow, skillID, actorID string, before, after []string) (*auditdomain.ResourceChangeAuditEvent, error) {
	active, ok, err := repo.GetActiveRevision(ctx, skillID)
	if err != nil {
		return nil, err
	}
	var activePtr *domain.SkillRevision
	if ok {
		activePtr = &active
	}
	return newChangeAudit(ctx, auditdomain.ResourceKindSkill, skillID, auditdomain.ChangeOpUpdate, actorID,
		skillSafeProjectionWithEditors(skill, activePtr, before), skillSafeProjectionWithEditors(skill, activePtr, after))
}

func (s *VersionService) GetVersion(ctx context.Context, skillID, revisionID string) (domain.SkillRevision, error) {
	revision, ok, err := s.repo.GetRevision(ctx, skillID, revisionID)
	if err != nil {
		return domain.SkillRevision{}, err
	}
	if !ok {
		return domain.SkillRevision{}, domain.ErrSkillNotFound
	}
	return revision, nil
}

func (s *VersionService) ResolvePublishedRevision(
	ctx context.Context, skillID, revisionID string,
) (domain.SkillRevision, error) {
	revision, err := s.GetVersion(ctx, skillID, revisionID)
	if err != nil {
		return domain.SkillRevision{}, err
	}
	if revision.Status != domain.VersionStatusPublished {
		return domain.SkillRevision{}, fmt.Errorf("skill revision is not published: %s", revisionID)
	}
	return revision, nil
}

func (s *VersionService) ResolveEvaluableRevision(
	ctx context.Context, skillID, revisionID string,
) (domain.SkillRevision, error) {
	revision, err := s.GetVersion(ctx, skillID, revisionID)
	if err != nil {
		return domain.SkillRevision{}, err
	}
	if revision.Status != domain.VersionStatusPublished && revision.Status != domain.VersionStatusCandidate {
		return domain.SkillRevision{}, fmt.Errorf("skill revision is not evaluable: %s", revisionID)
	}
	return revision, nil
}

func (s *VersionService) ResolveActivePublishedRevision(
	ctx context.Context, skillID string,
) (domain.SkillRevision, error) {
	revision, ok, err := s.repo.GetActiveRevision(ctx, skillID)
	if err != nil {
		return domain.SkillRevision{}, err
	}
	if !ok {
		return domain.SkillRevision{}, domain.ErrSkillNotFound
	}
	if revision.Status != domain.VersionStatusPublished {
		return domain.SkillRevision{}, fmt.Errorf("active skill revision is not published: %s", revision.ID)
	}
	return revision, nil
}

func (s *VersionService) PublishedRevisionSafeSummary(
	ctx context.Context, skillID, revisionID string,
) (map[string]any, error) {
	revision, err := s.ResolvePublishedRevision(ctx, skillID, revisionID)
	if err != nil {
		return nil, err
	}
	skill, found, err := s.repo.GetSkill(ctx, skillID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, domain.ErrSkillNotFound
	}
	return map[string]any{
		"name":           skill.Name,
		"description":    skill.Description,
		"version_label":  fmt.Sprintf("revision-%d", revision.RevisionNo),
		"changed_fields": []string{"instructions"},
	}, nil
}

func (s *VersionService) EvaluableRevisionSafeSummary(
	ctx context.Context, skillID, revisionID string,
) (map[string]any, error) {
	revision, err := s.ResolveEvaluableRevision(ctx, skillID, revisionID)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"version_label":  fmt.Sprintf("revision-%d", revision.RevisionNo),
		"changed_fields": []string{"instructions"},
	}, nil
}

func (s *VersionService) CreateCandidate(
	ctx context.Context, skillID, baselineRevisionID string, in CandidateInput,
) (domain.SkillRevision, error) {
	baseline, ok, err := s.repo.GetRevision(ctx, skillID, baselineRevisionID)
	if err != nil {
		return domain.SkillRevision{}, err
	}
	if !ok {
		return domain.SkillRevision{}, domain.ErrSkillNotFound
	}
	if in.Source != "llm_rewrite" {
		return domain.SkillRevision{}, fmt.Errorf("unsupported candidate source: %s", in.Source)
	}
	candidate := baseline
	candidate.ID = uuid.Must(uuid.NewV7()).String()
	candidate.ParentRevisionID = baseline.ID
	candidate.RevisionNo = 0
	candidate.Status = domain.VersionStatusCandidate
	candidate.Source = in.Source
	candidate.GenerationMetadata = cloneMap(in.GenerationMetadata)
	for key, value := range in.PromptPatch {
		if key != "instructions" {
			return domain.SkillRevision{}, fmt.Errorf("instruction field is not optimizable: %s", key)
		}
		text, ok := value.(string)
		if !ok || strings.TrimSpace(text) == "" {
			return domain.SkillRevision{}, fmt.Errorf("instructions must be a non-empty string")
		}
		candidate.Instructions = text
	}
	candidate.ContentHash, err = candidate.ComputeContentHash()
	if err != nil {
		return domain.SkillRevision{}, err
	}
	if err := s.repo.InsertCandidate(ctx, candidate, nil); err != nil {
		return domain.SkillRevision{}, err
	}
	return candidate, nil
}

func cloneMap(input map[string]any) map[string]any {
	if input == nil {
		return map[string]any{}
	}
	data, err := json.Marshal(input)
	if err != nil {
		return map[string]any{}
	}
	var output map[string]any
	if err := json.Unmarshal(data, &output); err != nil {
		return map[string]any{}
	}
	return output
}

// loadOwnedActive loads the skill row and its active revision, enforcing
// ownership before any mutation. A skill without an active revision (legacy
// unpublished row) yields a nil active: the first save then produces version 1.
// The returned editorActor is non-empty only when the actor writes via the
// editor whitelist row of the matrix and must be re-validated inside the
// repository write transaction.
func (s *VersionService) loadOwnedActive(ctx context.Context, skillID, actorID string) (port.SkillProductRow, *domain.SkillRevision, string, error) {
	skill, ok, err := s.repo.GetSkill(ctx, skillID)
	if err != nil {
		return port.SkillProductRow{}, nil, "", err
	}
	if !ok {
		return port.SkillProductRow{}, nil, "", domain.ErrSkillNotFound
	}
	editorActor, err := s.resolveUpdateActor(ctx, skillID, actorID, skill.CreatedBy)
	if err != nil {
		return port.SkillProductRow{}, nil, "", err
	}
	active, ok, err := s.repo.GetActiveRevision(ctx, skillID)
	if err != nil {
		return port.SkillProductRow{}, nil, "", err
	}
	if !ok {
		return skill, nil, editorActor, nil
	}
	return skill, &active, editorActor, nil
}

// resolveUpdateActor applies the ownership matrix for update-style writes.
// owner and admin pass the base matrix (empty editorActor); a member writes
// only when granted as editor — the set is resolved and re-checked, and the
// returned editorActor is re-validated inside the write transaction to close
// the check-then-write TOCTOU window. A nil editor repo denies the
// editor-granted row entirely (fail closed).
func (s *VersionService) resolveUpdateActor(ctx context.Context, skillID, actorID, createdBy string) (string, error) {
	if err := s.checkOwnership(ctx, actorID, createdBy, nil, OpEdit); err == nil {
		return "", nil
	}
	if s.editorRepo == nil {
		return "", domain.ErrForbidden
	}
	tenantID := reqctx.TenantIDFromContext(ctx)
	editors, err := s.editorRepo.ListEditors(ctx, tenantID, skillID)
	if err != nil {
		return "", fmt.Errorf("skill service list editors: %w", err)
	}
	if err := s.checkOwnership(ctx, actorID, createdBy, editors, OpEdit); err != nil {
		return "", err
	}
	return actorID, nil
}

// SaveRevision persists a new published revision derived from the current
// active revision and repoints the skill's active_revision_id — the saved
// content is immediately effective (保存即生效, no publish step). The edit
// surface is name/description/instructions. expectedContentHash guards
// concurrent edits (stale → ErrSkillDraftStale). A legacy unpublished skill
// (nil active) produces its first revision on save.
func (s *VersionService) SaveRevision(
	ctx context.Context,
	skillID, expectedContentHash string,
	in SaveRevisionInput,
) (SkillWorkspaceView, error) {
	skill, active, editorActor, err := s.loadOwnedActive(ctx, skillID, in.ActorID)
	if err != nil {
		return SkillWorkspaceView{}, err
	}
	if !contentHashMatches(expectedContentHash, active) {
		return SkillWorkspaceView{}, domain.ErrSkillDraftStale
	}
	// before 投影在派生新版本前定格当前生效内容。
	var beforeRev *domain.SkillRevision
	if active != nil {
		rev := *active
		beforeRev = &rev
	}
	// before 投影读取 skill 行 + 旧 active,必须在 mutate skill 前定格,否则会投影到新值。
	before := skillSafeProjection(skill, beforeRev)
	name := strings.TrimSpace(in.Name)
	description := strings.TrimSpace(in.Description)
	next, err := s.repo.NextRevisionNo(ctx, skillID)
	if err != nil {
		return SkillWorkspaceView{}, err
	}
	newRevision, err := buildNextRevision(skillID, next, active, in)
	if err != nil {
		return SkillWorkspaceView{}, err
	}
	skill.Name = name
	skill.Description = description
	audit, err := newChangeAudit(ctx, auditdomain.ResourceKindSkill, skillID, auditdomain.ChangeOpUpdate, in.ActorID,
		before, skillSafeProjection(skill, &newRevision))
	if err != nil {
		return SkillWorkspaceView{}, err
	}
	saved, err := s.repo.SaveRevision(ctx, skillID, expectedContentHash, skill, newRevision, audit, editorActor)
	if err != nil {
		s.recordFailure(ctx, skillID, "update", err)
		return SkillWorkspaceView{}, err
	}
	skill.ActiveRevisionID = saved.ID
	return SkillWorkspaceView{Skill: skill, Active: saved, Editors: []string{}}, nil
}

// contentHashMatches reports whether the caller's expectedContentHash still
// matches the current active revision. An empty baseline accepts any state
// (legacy callers); a non-empty baseline must equal the active content hash,
// else the edit is stale.
func contentHashMatches(expected string, active *domain.SkillRevision) bool {
	return expected == "" || (active != nil && active.ContentHash == expected)
}

// buildNextRevision derives a new published revision from the current active
// one: revision_no is assigned by the caller, parent points at the old active
// when present, and the content hash is recomputed from the trimmed fields.
func buildNextRevision(skillID string, next int, active *domain.SkillRevision, in SaveRevisionInput) (domain.SkillRevision, error) {
	rev := domain.SkillRevision{
		ID:                 uuid.Must(uuid.NewV7()).String(),
		SkillID:            skillID,
		Status:             domain.VersionStatusPublished,
		Source:             "manual",
		RevisionNo:         next,
		GenerationMetadata: map[string]any{},
		Name:               strings.TrimSpace(in.Name),
		Description:        strings.TrimSpace(in.Description),
		Instructions:       strings.TrimSpace(in.Instructions),
		CreatedBy:          in.ActorID,
	}
	if active != nil {
		rev.ParentRevisionID = active.ID
		rev.GenerationMetadata = cloneMap(active.GenerationMetadata)
	}
	hash, err := rev.ComputeContentHash()
	if err != nil {
		return domain.SkillRevision{}, err
	}
	rev.ContentHash = hash
	return rev, nil
}

// ListRevisions returns the skill's version history, newest first, marking
// the currently effective version.
func (s *VersionService) ListRevisions(ctx context.Context, skillID string) ([]domain.SkillRevision, error) {
	revisions, ok, err := s.repo.ListRevisions(ctx, skillID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, domain.ErrSkillNotFound
	}
	return revisions, nil
}

// RollbackRevision repoints the skill's active_revision_id to a historical
// published revision (deprecated → published), immediately effective without
// creating a new version. Owner/admin pass the base matrix; a member rolls
// back only when granted as editor (re-validated inside the write transaction).
func (s *VersionService) RollbackRevision(ctx context.Context, skillID, targetRevisionID, actorID string) error {
	skill, active, target, err := s.loadRollbackTargets(ctx, skillID, targetRevisionID)
	if err != nil {
		return err
	}
	editorActor, err := s.resolveUpdateActor(ctx, skillID, actorID, skill.CreatedBy)
	if err != nil {
		return err
	}
	after := target
	after.Status = domain.VersionStatusPublished
	audit, err := newChangeAudit(ctx, auditdomain.ResourceKindSkill, skillID, auditdomain.ChangeOpRollback, actorID,
		skillSafeProjection(skill, &active), skillSafeProjection(skill, &after))
	if err != nil {
		return err
	}
	if err := s.repo.RollbackRevision(ctx, skillID, targetRevisionID, editorActor, audit); err != nil {
		s.recordFailure(ctx, skillID, "rollback", err)
		return err
	}
	s.logger.Info("skill rolled back", zap.String("skill_id", skillID), zap.String("revision_id", targetRevisionID))
	return nil
}

// loadRollbackTargets loads the skill, its active revision and the requested
// target revision, and verifies the target is a historical published version
// (deprecated, not the current effective version nor an evaluation candidate).
func (s *VersionService) loadRollbackTargets(ctx context.Context, skillID, targetRevisionID string) (port.SkillProductRow, domain.SkillRevision, domain.SkillRevision, error) {
	skill, ok, err := s.repo.GetSkill(ctx, skillID)
	if err != nil {
		return port.SkillProductRow{}, domain.SkillRevision{}, domain.SkillRevision{}, err
	}
	if !ok {
		return port.SkillProductRow{}, domain.SkillRevision{}, domain.SkillRevision{}, domain.ErrSkillNotFound
	}
	active, ok, err := s.repo.GetActiveRevision(ctx, skillID)
	if err != nil {
		return port.SkillProductRow{}, domain.SkillRevision{}, domain.SkillRevision{}, err
	}
	if !ok {
		return port.SkillProductRow{}, domain.SkillRevision{}, domain.SkillRevision{}, domain.ErrSkillNotFound
	}
	target, ok, err := s.repo.GetRevision(ctx, skillID, targetRevisionID)
	if err != nil {
		return port.SkillProductRow{}, domain.SkillRevision{}, domain.SkillRevision{}, err
	}
	if !ok {
		return port.SkillProductRow{}, domain.SkillRevision{}, domain.SkillRevision{}, domain.ErrSkillNotFound
	}
	if target.Status != domain.VersionStatusDeprecated {
		return port.SkillProductRow{}, domain.SkillRevision{}, domain.SkillRevision{}, domain.ErrSkillNotFound
	}
	return skill, active, target, nil
}
