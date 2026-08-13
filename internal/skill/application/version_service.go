package application

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/internal/skill/domain"
	"github.com/byteBuilderX/stratum/internal/skill/domain/port"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type SkillProduct = port.SkillProductRow
type SkillRevision = domain.SkillRevision

type CreateSkillDraftInput struct {
	Name           string
	Goal           string
	WhenToUse      string
	SampleInput    any
	ExpectedOutput any
	Instructions   string
	Requirements   domain.Requirements
	// ActorID is the caller; becomes the skill's created_by (owner/admin only).
	ActorID string
	// Editors are additional admins granted update rights, persisted in the
	// same transaction as the skill. Each must hold role admin/owner.
	Editors []string
}

type SkillWorkspaceView struct {
	Skill   SkillProduct
	Draft   domain.SkillRevision
	Editors []string
}

type UpdateCapabilityInput struct {
	Goal       string
	WhenToUse  string
	InputSpec  string
	OutputSpec string
	// ActorID is the caller; ownership is checked against the skill's created_by.
	ActorID string
}

type UpdateActivationInput struct {
	Name         string
	Description  string
	InputSchema  map[string]any
	OutputSchema map[string]any
	Confirmed    bool
	// ActorID is the caller; ownership is checked against the skill's created_by.
	ActorID string
}

type UpdateInstructionBundleInput struct {
	Instructions string
	Requirements domain.Requirements
	// ActorID is the caller; ownership is checked against the skill's created_by.
	ActorID string
}

type UpdateDraftBundleInput struct {
	Name         string
	Description  string
	Instructions string
	Requirements domain.Requirements
	// ActorID is the caller; ownership is checked against the skill's created_by.
	ActorID string
}

type CandidateInput struct {
	Source             string
	PromptPatch        map[string]any
	GenerationMetadata map[string]any
}

type VersionService struct {
	repo             port.VersionRepo
	logger           *zap.Logger
	roles            port.TenantRoleResolver
	editorRepo       port.SkillResourceEditorRepo
	bindingValidator port.WorkspaceBindingValidator
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

// SetWorkspaceBindingValidator injects the knowledge workspace existence
// check backing draft knowledge bindings. A nil validator rejects every
// binding (fail closed).
func (s *VersionService) SetWorkspaceBindingValidator(v port.WorkspaceBindingValidator) {
	s.bindingValidator = v
}

// validateWorkspaceBindings fails closed (D10): an un-wired validator or an
// unknown workspace ID rejects the binding. Empty lists pass trivially —
// no bindings to verify.
func (s *VersionService) validateWorkspaceBindings(ctx context.Context, workspaceIDs []string) error {
	if len(workspaceIDs) == 0 {
		return nil
	}
	if s.bindingValidator == nil {
		return fmt.Errorf("skill: workspace binding validation unavailable (validator not wired)")
	}
	return s.bindingValidator.ValidateWorkspaceBindings(ctx, reqctx.TenantIDFromContext(ctx), workspaceIDs)
}

func (s *VersionService) CreateSkillDraft(ctx context.Context, in CreateSkillDraftInput) (SkillWorkspaceView, error) {
	// create encodes "the creator owns the resource": only owner/admin may create.
	if err := s.checkOwnership(ctx, in.ActorID, in.ActorID, nil); err != nil {
		return SkillWorkspaceView{}, err
	}
	skillID := uuid.Must(uuid.NewV7()).String()
	draftID := uuid.Must(uuid.NewV7()).String()
	instructions := strings.TrimSpace(in.Instructions)
	if instructions == "" {
		instructions = strings.TrimSpace(in.Goal)
	}
	draft := domain.SkillRevision{
		ID:                 draftID,
		SkillID:            skillID,
		Status:             domain.VersionStatusDraft,
		Source:             "manual",
		GenerationMetadata: map[string]any{},
		Capability: domain.Capability{
			Goal:      in.Goal,
			WhenToUse: in.WhenToUse,
			Examples:  []domain.CapabilityExample{{Input: in.SampleInput, ExpectedOutput: in.ExpectedOutput}},
		},
		ActivationContract: domain.ActivationContract{
			Name:         generatedActivationName(in.Name),
			Description:  strings.TrimSpace(in.WhenToUse + "，" + in.Goal),
			InputSchema:  map[string]any{"type": "object"},
			OutputSchema: map[string]any{"type": "object"},
			Confirmed:    false,
		},
		Instructions: instructions,
		Requirements: in.Requirements,
	}
	if err := s.validateWorkspaceBindings(ctx, in.Requirements.KnowledgeWorkspaceIDs); err != nil {
		return SkillWorkspaceView{}, err
	}
	contentHash, err := draft.ComputeContentHash()
	if err != nil {
		return SkillWorkspaceView{}, err
	}
	draft.ContentHash = contentHash
	skill := port.SkillProductRow{
		ID:              skillID,
		Name:            in.Name,
		Description:     in.Goal,
		Status:          "draft",
		DraftRevisionID: draftID,
		CreatedBy:       in.ActorID,
	}
	audit, err := newChangeAudit(ctx, auditdomain.ResourceKindSkill, skillID, auditdomain.ChangeOpCreate,
		in.ActorID, nil, skillSafeProjection(skill, &draft))
	if err != nil {
		return SkillWorkspaceView{}, err
	}
	if err := s.repo.InsertSkillWithDraft(ctx, skill, draft, audit, in.Editors); err != nil {
		return SkillWorkspaceView{}, err
	}
	s.logger.Info("skill draft created", zap.String("skill_id", skillID), zap.String("draft_revision_id", draftID))
	// Editors must be non-nil: JSON renders a nil slice as null, and the
	// frontend schema default only covers a missing field, not null.
	return SkillWorkspaceView{Skill: skill, Draft: draft, Editors: []string{}}, nil
}

func (s *VersionService) PublishDraft(ctx context.Context, skillID, actorID string) (domain.SkillRevision, error) {
	skill, draft, err := s.loadPublishDraft(ctx, skillID)
	if err != nil {
		return domain.SkillRevision{}, err
	}
	// Publish is an update-style mutation: creator/owner pass the base
	// matrix, a foreign admin may publish when granted as editor (re-checked
	// inside the write transaction via editorActor).
	editorActor, err := s.resolveUpdateActor(ctx, skillID, actorID, skill.CreatedBy)
	if err != nil {
		return domain.SkillRevision{}, err
	}
	next, err := s.repo.NextRevisionNo(ctx, skillID)
	if err != nil {
		return domain.SkillRevision{}, err
	}
	checks := map[string]any{"capability_examples": len(draft.Capability.Examples)}
	afterDraft := draft
	afterDraft.Status = domain.VersionStatusPublished
	afterDraft.RevisionNo = next
	audit, err := newChangeAudit(ctx, auditdomain.ResourceKindSkill, skillID, auditdomain.ChangeOpUpdate, actorID,
		skillSafeProjection(skill, &draft), skillSafeProjection(skill, &afterDraft))
	if err != nil {
		return domain.SkillRevision{}, err
	}
	return s.repo.PublishDraft(ctx, skillID, draft.ID, next, checks, audit, editorActor)
}

// loadPublishDraft loads the skill and its draft revision, enforcing the
// builtin-skill guard, existence and publishability. Unknown workspace
// bindings fail here so a broken activation contract never ships.
func (s *VersionService) loadPublishDraft(ctx context.Context, skillID string) (port.SkillProductRow, domain.SkillRevision, error) {
	if isBuiltinSkill(skillID) {
		return port.SkillProductRow{}, domain.SkillRevision{}, domain.ErrPlatformManagedSkill
	}
	skill, ok, err := s.repo.GetSkill(ctx, skillID)
	if err != nil {
		return port.SkillProductRow{}, domain.SkillRevision{}, err
	}
	if !ok {
		return port.SkillProductRow{}, domain.SkillRevision{}, domain.ErrSkillNotFound
	}
	draft, ok, err := s.repo.GetDraftRevision(ctx, skillID)
	if err != nil {
		return port.SkillProductRow{}, domain.SkillRevision{}, err
	}
	if !ok {
		return port.SkillProductRow{}, domain.SkillRevision{}, domain.ErrSkillDraftNotFound
	}
	if err := draft.ValidatePublishable(0); err != nil {
		return port.SkillProductRow{}, domain.SkillRevision{}, err
	}
	// Publishing freezes the draft's bindings: unknown workspaces must fail
	// here rather than ship a broken activation contract.
	if err := s.validateWorkspaceBindings(ctx, draft.Requirements.KnowledgeWorkspaceIDs); err != nil {
		return port.SkillProductRow{}, domain.SkillRevision{}, err
	}
	return skill, draft, nil
}

func (s *VersionService) GetWorkspace(ctx context.Context, skillID string) (SkillWorkspaceView, error) {
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
	draft, ok, err := s.repo.GetDraftRevision(ctx, skillID)
	if err != nil {
		return SkillWorkspaceView{}, err
	}
	if ok {
		return SkillWorkspaceView{Skill: skill, Draft: draft, Editors: editors}, nil
	}
	active, ok, err := s.repo.GetActiveRevision(ctx, skillID)
	if err != nil {
		return SkillWorkspaceView{}, err
	}
	if !ok {
		return SkillWorkspaceView{}, domain.ErrSkillNotFound
	}
	return SkillWorkspaceView{Skill: skill, Draft: active, Editors: editors}, nil
}

func (s *VersionService) ListSkills(ctx context.Context) ([]SkillProduct, error) {
	return s.repo.ListSkills(ctx)
}

func (s *VersionService) DeleteSkill(ctx context.Context, skillID, actorID string) error {
	if isBuiltinSkill(skillID) {
		return domain.ErrPlatformManagedSkill
	}
	skill, ok, err := s.repo.GetSkill(ctx, skillID)
	if err != nil {
		return err
	}
	if !ok {
		return domain.ErrSkillNotFound
	}
	// Delete stays creator/owner-only: editors do not grant delete rights.
	if err := s.checkOwnership(ctx, actorID, skill.CreatedBy, nil); err != nil {
		return err
	}
	// The draft may already be gone (published skills have no draft); the
	// before projection then carries the product row only.
	var draft *domain.SkillRevision
	if d, found, getErr := s.repo.GetDraftRevision(ctx, skillID); getErr != nil {
		return getErr
	} else if found {
		draft = &d
	}
	audit, err := newChangeAudit(ctx, auditdomain.ResourceKindSkill, skillID, auditdomain.ChangeOpDelete, actorID,
		skillSafeProjection(skill, draft), nil)
	if err != nil {
		return err
	}
	return s.repo.DeleteSkill(ctx, skillID, audit)
}

// SetEditors replaces the granted editor set of a skill resource. Only the
// creator or an owner may manage editors (an editor cannot delegate their own
// right); each editor must hold role admin/owner at write time, enforced
// inside the repository transaction (fail closed). The change is audited in
// the same transaction with before/after projections.
func (s *VersionService) SetEditors(ctx context.Context, skillID, actorID string, editorIDs []string) error {
	if s.editorRepo == nil {
		return fmt.Errorf("skill service set editors: editor repo not wired")
	}
	if isBuiltinSkill(skillID) {
		return domain.ErrPlatformManagedSkill
	}
	skill, ok, err := s.repo.GetSkill(ctx, skillID)
	if err != nil {
		return fmt.Errorf("skill service set editors: %w", err)
	}
	if !ok {
		return domain.ErrSkillNotFound
	}
	// Editors can never grant delete rights, so SetEditors reuses the
	// creator/owner-only base matrix.
	if err := s.checkOwnership(ctx, actorID, skill.CreatedBy, nil); err != nil {
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
// management endpoint. The draft may be gone for published skills; the
// projection then carries the product row only.
func buildSkillEditorAudit(ctx context.Context, repo port.VersionRepo, skill port.SkillProductRow, skillID, actorID string, before, after []string) (*auditdomain.ResourceChangeAuditEvent, error) {
	var draft *domain.SkillRevision
	if d, found, err := repo.GetDraftRevision(ctx, skillID); err != nil {
		return nil, err
	} else if found {
		draft = &d
	}
	return newChangeAudit(ctx, auditdomain.ResourceKindSkill, skillID, auditdomain.ChangeOpUpdate, actorID,
		skillSafeProjectionWithEditors(skill, draft, before), skillSafeProjectionWithEditors(skill, draft, after))
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

func (s *VersionService) UpdateCapability(ctx context.Context, skillID string, in UpdateCapabilityInput) (domain.SkillRevision, error) {
	skill, draft, editorActor, err := s.loadOwnedDraft(ctx, skillID, in.ActorID)
	if err != nil {
		return domain.SkillRevision{}, err
	}
	before := skillSafeProjection(skill, draft)
	draft.Capability.Goal = in.Goal
	draft.Capability.WhenToUse = in.WhenToUse
	draft.Capability.InputSpec = in.InputSpec
	draft.Capability.OutputSpec = in.OutputSpec
	contentHash, err := draft.ComputeContentHash()
	if err != nil {
		return domain.SkillRevision{}, err
	}
	audit, err := newChangeAudit(ctx, auditdomain.ResourceKindSkill, skillID, auditdomain.ChangeOpUpdate, in.ActorID,
		before, skillSafeProjection(skill, draft))
	if err != nil {
		return domain.SkillRevision{}, err
	}
	return s.repo.UpdateDraftCapability(ctx, skillID, draft.Capability, contentHash, audit, editorActor)
}

func (s *VersionService) UpdateActivation(ctx context.Context, skillID string, in UpdateActivationInput) (domain.SkillRevision, error) {
	skill, draft, editorActor, err := s.loadOwnedDraft(ctx, skillID, in.ActorID)
	if err != nil {
		return domain.SkillRevision{}, err
	}
	before := skillSafeProjection(skill, draft)
	contract := domain.ActivationContract{
		Name: in.Name, Description: in.Description, InputSchema: in.InputSchema,
		OutputSchema: in.OutputSchema, Confirmed: in.Confirmed,
	}
	// InputSchema/OutputSchema 是声明性元数据(契约负担已放宽,Validate 只强制
	// Name+Description)。nil 默认成空 object 仅保持存量数据形状兼容,不影响运行时
	// 工具契约——skill 激活不再依赖 schema 校验。
	if contract.InputSchema == nil {
		contract.InputSchema = map[string]any{"type": "object"}
	}
	if contract.OutputSchema == nil {
		contract.OutputSchema = map[string]any{"type": "object"}
	}
	draft.ActivationContract = contract
	contentHash, err := draft.ComputeContentHash()
	if err != nil {
		return domain.SkillRevision{}, err
	}
	audit, err := newChangeAudit(ctx, auditdomain.ResourceKindSkill, skillID, auditdomain.ChangeOpUpdate, in.ActorID,
		before, skillSafeProjection(skill, draft))
	if err != nil {
		return domain.SkillRevision{}, err
	}
	return s.repo.UpdateDraftActivation(ctx, skillID, contract, contentHash, audit, editorActor)
}

func (s *VersionService) UpdateInstructionBundle(
	ctx context.Context, skillID string, in UpdateInstructionBundleInput,
) (domain.SkillRevision, error) {
	skill, draft, editorActor, err := s.loadOwnedDraft(ctx, skillID, in.ActorID)
	if err != nil {
		return domain.SkillRevision{}, err
	}
	before := skillSafeProjection(skill, draft)
	if err := s.validateWorkspaceBindings(ctx, in.Requirements.KnowledgeWorkspaceIDs); err != nil {
		return domain.SkillRevision{}, err
	}
	draft.Instructions = in.Instructions
	draft.Requirements = in.Requirements
	contentHash, err := draft.ComputeContentHash()
	if err != nil {
		return domain.SkillRevision{}, err
	}
	audit, err := newChangeAudit(ctx, auditdomain.ResourceKindSkill, skillID, auditdomain.ChangeOpUpdate, in.ActorID,
		before, skillSafeProjection(skill, draft))
	if err != nil {
		return domain.SkillRevision{}, err
	}
	return s.repo.UpdateDraftInstructions(ctx, skillID, in.Instructions, in.Requirements, contentHash, audit, editorActor)
}

// loadOwnedDraft loads the skill row and its draft, enforcing the builtin
// guard and ownership before any mutation. The draft always exists for the
// update methods using this helper. The returned editorActor is non-empty
// only when the actor writes via the admin-editor row of the matrix and must
// be re-validated inside the repository write transaction.
func (s *VersionService) loadOwnedDraft(ctx context.Context, skillID, actorID string) (port.SkillProductRow, *domain.SkillRevision, string, error) {
	if isBuiltinSkill(skillID) {
		return port.SkillProductRow{}, nil, "", domain.ErrPlatformManagedSkill
	}
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
	draft, ok, err := s.repo.GetDraftRevision(ctx, skillID)
	if err != nil {
		return port.SkillProductRow{}, nil, "", err
	}
	if !ok {
		return port.SkillProductRow{}, nil, "", domain.ErrSkillNotFound
	}
	return skill, &draft, editorActor, nil
}

// resolveUpdateActor applies the ownership matrix for update-style writes.
// creator/owner pass the base matrix (empty editorActor); a foreign admin may
// write when granted as editor — the set is resolved and re-checked, and the
// returned editorActor is re-validated inside the write transaction to close
// the check-then-write TOCTOU window. A nil editor repo denies the
// editor-granted row entirely (fail closed).
func (s *VersionService) resolveUpdateActor(ctx context.Context, skillID, actorID, createdBy string) (string, error) {
	if err := s.checkOwnership(ctx, actorID, createdBy, nil); err == nil {
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
	if err := s.checkOwnership(ctx, actorID, createdBy, editors); err != nil {
		return "", err
	}
	return actorID, nil
}

func (s *VersionService) UpdateDraftBundle(
	ctx context.Context,
	skillID, expectedContentHash string,
	in UpdateDraftBundleInput,
) (SkillWorkspaceView, error) {
	skill, draft, editorActor, err := s.loadOwnedDraft(ctx, skillID, in.ActorID)
	if err != nil {
		return SkillWorkspaceView{}, err
	}
	// 空 baseline(直写)跳过乐观并发校验,与 API 直连一致;proposal 路径始终携带 draft hash。
	if expectedContentHash != "" && draft.ContentHash != expectedContentHash {
		return SkillWorkspaceView{}, domain.ErrSkillDraftStale
	}
	before := skillSafeProjection(skill, draft)
	if err := s.validateWorkspaceBindings(ctx, in.Requirements.KnowledgeWorkspaceIDs); err != nil {
		return SkillWorkspaceView{}, err
	}
	if err := applyDraftBundle(&skill, draft, in); err != nil {
		return SkillWorkspaceView{}, err
	}
	audit, err := newChangeAudit(ctx, auditdomain.ResourceKindSkill, skillID, auditdomain.ChangeOpUpdate, in.ActorID,
		before, skillSafeProjection(skill, draft))
	if err != nil {
		return SkillWorkspaceView{}, err
	}
	updated, err := s.repo.UpdateDraftBundle(ctx, skillID, expectedContentHash, skill, *draft, audit, editorActor)
	if err != nil {
		return SkillWorkspaceView{}, err
	}
	skill.DraftRevisionID = updated.ID
	return SkillWorkspaceView{Skill: skill, Draft: updated}, nil
}

// applyDraftBundle mutates skill+draft from the bundle input and recomputes
// the content hash; the hash failure aborts the write.
func applyDraftBundle(skill *port.SkillProductRow, draft *domain.SkillRevision, in UpdateDraftBundleInput) error {
	draft.Capability.Goal = strings.TrimSpace(in.Description)
	draft.Capability.WhenToUse = strings.TrimSpace(in.Description)
	draft.ActivationContract.Name = generatedActivationName(in.Name)
	draft.ActivationContract.Description = strings.TrimSpace(in.Description)
	draft.Instructions = strings.TrimSpace(in.Instructions)
	draft.Requirements = in.Requirements
	contentHash, err := draft.ComputeContentHash()
	if err != nil {
		return err
	}
	draft.ContentHash = contentHash
	skill.Name = strings.TrimSpace(in.Name)
	skill.Description = strings.TrimSpace(in.Description)
	return nil
}

var nonActivationName = regexp.MustCompile(`[^a-zA-Z0-9_]+`)

func generatedActivationName(name string) string {
	out := strings.ToLower(nonActivationName.ReplaceAllString(name, "_"))
	out = strings.Trim(out, "_")
	if out == "" || (out[0] >= '0' && out[0] <= '9') {
		out = "skill_" + out
	}
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}
