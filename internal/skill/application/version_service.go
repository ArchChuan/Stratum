package application

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/byteBuilderX/stratum/internal/skill/domain"
	"github.com/byteBuilderX/stratum/internal/skill/domain/port"
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
}

type SkillWorkspaceView struct {
	Skill SkillProduct
	Draft domain.SkillRevision
}

type UpdateCapabilityInput struct {
	Goal       string
	WhenToUse  string
	InputSpec  string
	OutputSpec string
}

type UpdateActivationInput struct {
	Name         string
	Description  string
	InputSchema  map[string]any
	OutputSchema map[string]any
	Confirmed    bool
}

type UpdateInstructionBundleInput struct {
	Instructions string
	Requirements domain.Requirements
}

type CandidateInput struct {
	Source             string
	PromptPatch        map[string]any
	GenerationMetadata map[string]any
}

type VersionService struct {
	repo   port.VersionRepo
	logger *zap.Logger
}

func NewVersionService(repo port.VersionRepo, logger *zap.Logger) *VersionService {
	return &VersionService{repo: repo, logger: logger}
}

func (s *VersionService) CreateSkillDraft(ctx context.Context, in CreateSkillDraftInput) (SkillWorkspaceView, error) {
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
	}
	if err := s.repo.InsertSkillWithDraft(ctx, skill, draft); err != nil {
		return SkillWorkspaceView{}, err
	}
	s.logger.Info("skill draft created", zap.String("skill_id", skillID), zap.String("draft_revision_id", draftID))
	return SkillWorkspaceView{Skill: skill, Draft: draft}, nil
}

func (s *VersionService) PublishDraft(ctx context.Context, skillID string) (domain.SkillRevision, error) {
	draft, ok, err := s.repo.GetDraftRevision(ctx, skillID)
	if err != nil {
		return domain.SkillRevision{}, err
	}
	if !ok {
		return domain.SkillRevision{}, domain.ErrSkillNotFound
	}
	if err := draft.ValidatePublishable(0); err != nil {
		return domain.SkillRevision{}, err
	}
	next, err := s.repo.NextRevisionNo(ctx, skillID)
	if err != nil {
		return domain.SkillRevision{}, err
	}
	checks := map[string]any{"capability_examples": len(draft.Capability.Examples)}
	return s.repo.PublishDraft(ctx, skillID, draft.ID, next, checks)
}

func (s *VersionService) GetWorkspace(ctx context.Context, skillID string) (SkillWorkspaceView, error) {
	skill, ok, err := s.repo.GetSkill(ctx, skillID)
	if err != nil {
		return SkillWorkspaceView{}, err
	}
	if !ok {
		return SkillWorkspaceView{}, domain.ErrSkillNotFound
	}
	draft, ok, err := s.repo.GetDraftRevision(ctx, skillID)
	if err != nil {
		return SkillWorkspaceView{}, err
	}
	if ok {
		return SkillWorkspaceView{Skill: skill, Draft: draft}, nil
	}
	active, ok, err := s.repo.GetActiveRevision(ctx, skillID)
	if err != nil {
		return SkillWorkspaceView{}, err
	}
	if !ok {
		return SkillWorkspaceView{}, domain.ErrSkillNotFound
	}
	return SkillWorkspaceView{Skill: skill, Draft: active}, nil
}

func (s *VersionService) ListSkills(ctx context.Context) ([]SkillProduct, error) {
	return s.repo.ListSkills(ctx)
}

func (s *VersionService) DeleteSkill(ctx context.Context, skillID string) error {
	return s.repo.DeleteSkill(ctx, skillID)
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

func (s *VersionService) PublishedRevisionSafeSummary(
	ctx context.Context, skillID, revisionID string,
) (map[string]any, error) {
	revision, err := s.ResolvePublishedRevision(ctx, skillID, revisionID)
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
	if err := s.repo.InsertCandidate(ctx, candidate); err != nil {
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
	draft, ok, err := s.repo.GetDraftRevision(ctx, skillID)
	if err != nil {
		return domain.SkillRevision{}, err
	}
	if !ok {
		return domain.SkillRevision{}, domain.ErrSkillNotFound
	}
	draft.Capability.Goal = in.Goal
	draft.Capability.WhenToUse = in.WhenToUse
	draft.Capability.InputSpec = in.InputSpec
	draft.Capability.OutputSpec = in.OutputSpec
	contentHash, err := draft.ComputeContentHash()
	if err != nil {
		return domain.SkillRevision{}, err
	}
	return s.repo.UpdateDraftCapability(ctx, skillID, draft.Capability, contentHash)
}

func (s *VersionService) UpdateActivation(ctx context.Context, skillID string, in UpdateActivationInput) (domain.SkillRevision, error) {
	draft, ok, err := s.repo.GetDraftRevision(ctx, skillID)
	if err != nil {
		return domain.SkillRevision{}, err
	}
	if !ok {
		return domain.SkillRevision{}, domain.ErrSkillNotFound
	}
	contract := domain.ActivationContract{
		Name: in.Name, Description: in.Description, InputSchema: in.InputSchema,
		OutputSchema: in.OutputSchema, Confirmed: in.Confirmed,
	}
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
	return s.repo.UpdateDraftActivation(ctx, skillID, contract, contentHash)
}

func (s *VersionService) UpdateInstructionBundle(
	ctx context.Context, skillID string, in UpdateInstructionBundleInput,
) (domain.SkillRevision, error) {
	draft, ok, err := s.repo.GetDraftRevision(ctx, skillID)
	if err != nil {
		return domain.SkillRevision{}, err
	}
	if !ok {
		return domain.SkillRevision{}, domain.ErrSkillNotFound
	}
	draft.Instructions = in.Instructions
	draft.Requirements = in.Requirements
	contentHash, err := draft.ComputeContentHash()
	if err != nil {
		return domain.SkillRevision{}, err
	}
	return s.repo.UpdateDraftInstructions(ctx, skillID, in.Instructions, in.Requirements, contentHash)
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
