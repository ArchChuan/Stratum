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
type SkillTestCase = port.SkillTestCaseRow
type SkillVersion = domain.SkillVersion

type CreateSkillDraftInput struct {
	Name           string
	Goal           string
	WhenToUse      string
	SampleInput    any
	ExpectedOutput any
}

type SkillWorkspaceView struct {
	Skill SkillProduct
	Draft domain.SkillVersion
}

type UpdateCapabilityInput struct {
	Goal       string
	WhenToUse  string
	InputSpec  string
	OutputSpec string
}

type UpdateContractInput struct {
	ToolName        string
	Description     string
	InputSchema     map[string]any
	OutputSchema    map[string]any
	CallingGuidance string
	Confirmed       bool
}

type UpdateImplementationInput struct {
	Mode        string
	Source      map[string]any
	Runtime     map[string]any
	Permissions map[string]any
	SecretRefs  []string
}

type CandidateInput struct {
	Source             string
	ParameterPatch     map[string]any
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
	toolName := generatedToolName(in.Name)
	draft := domain.SkillVersion{
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
		ToolContract: domain.ToolContract{
			ToolName:     toolName,
			Description:  strings.TrimSpace(in.WhenToUse + "，" + in.Goal),
			InputSchema:  map[string]any{"type": "object"},
			OutputSchema: map[string]any{"type": "object"},
			Confirmed:    false,
		},
		Implementation: domain.Implementation{
			Mode: "prompt",
			Source: map[string]any{
				"promptTemplate": in.Goal + "\n\n输入：{{.input}}",
			},
		},
	}
	contentHash, err := draft.ComputeContentHash()
	if err != nil {
		return SkillWorkspaceView{}, err
	}
	draft.ContentHash = contentHash
	skill := port.SkillProductRow{
		ID:             skillID,
		Name:           in.Name,
		Description:    in.Goal,
		Status:         "draft",
		DraftVersionID: draftID,
	}
	firstCase := port.SkillTestCaseRow{
		ID:             uuid.Must(uuid.NewV7()).String(),
		SkillID:        skillID,
		Name:           "初始样例",
		Input:          in.SampleInput,
		ExpectedOutput: in.ExpectedOutput,
		AssertionMode:  "contains",
		Enabled:        true,
	}
	if err := s.repo.InsertSkillWithDraft(ctx, skill, draft, firstCase); err != nil {
		return SkillWorkspaceView{}, err
	}
	s.logger.Info("skill draft created", zap.String("skill_id", skillID), zap.String("draft_version_id", draftID))
	return SkillWorkspaceView{Skill: skill, Draft: draft}, nil
}

func (s *VersionService) PublishDraft(ctx context.Context, skillID string) (domain.SkillVersion, error) {
	draft, ok, err := s.repo.GetDraftVersion(ctx, skillID)
	if err != nil {
		return domain.SkillVersion{}, err
	}
	if !ok {
		return domain.SkillVersion{}, domain.ErrSkillNotFound
	}
	testCount, err := s.repo.CountEnabledTestCases(ctx, skillID)
	if err != nil {
		return domain.SkillVersion{}, err
	}
	if err := draft.ValidatePublishable(testCount); err != nil {
		return domain.SkillVersion{}, err
	}
	next, err := s.repo.NextVersionNo(ctx, skillID)
	if err != nil {
		return domain.SkillVersion{}, err
	}
	return s.repo.PublishDraft(ctx, skillID, draft.ID, next, map[string]any{"enabled_test_count": testCount})
}

func (s *VersionService) GetWorkspace(ctx context.Context, skillID string) (SkillWorkspaceView, error) {
	skill, ok, err := s.repo.GetSkill(ctx, skillID)
	if err != nil {
		return SkillWorkspaceView{}, err
	}
	if !ok {
		return SkillWorkspaceView{}, domain.ErrSkillNotFound
	}
	draft, ok, err := s.repo.GetDraftVersion(ctx, skillID)
	if err != nil {
		return SkillWorkspaceView{}, err
	}
	if ok {
		return SkillWorkspaceView{Skill: skill, Draft: draft}, nil
	}
	active, ok, err := s.repo.GetActiveVersion(ctx, skillID)
	if err != nil {
		return SkillWorkspaceView{}, err
	}
	if !ok {
		return SkillWorkspaceView{}, domain.ErrSkillNotFound
	}
	return SkillWorkspaceView{Skill: skill, Draft: active}, nil
}

func (s *VersionService) GetVersion(ctx context.Context, skillID, versionID string) (domain.SkillVersion, error) {
	version, ok, err := s.repo.GetVersion(ctx, skillID, versionID)
	if err != nil {
		return domain.SkillVersion{}, err
	}
	if !ok {
		return domain.SkillVersion{}, domain.ErrSkillNotFound
	}
	return version, nil
}

func (s *VersionService) CreateCandidate(
	ctx context.Context,
	skillID, baselineVersionID string,
	in CandidateInput,
) (domain.SkillVersion, error) {
	baseline, ok, err := s.repo.GetVersion(ctx, skillID, baselineVersionID)
	if err != nil {
		return domain.SkillVersion{}, err
	}
	if !ok {
		return domain.SkillVersion{}, domain.ErrSkillNotFound
	}
	if in.Source != "parameter_search" && in.Source != "llm_rewrite" {
		return domain.SkillVersion{}, fmt.Errorf("unsupported candidate source: %s", in.Source)
	}
	candidate := baseline
	candidate.ID = uuid.Must(uuid.NewV7()).String()
	candidate.ParentVersionID = baseline.ID
	candidate.VersionNo = 0
	candidate.Status = domain.VersionStatusCandidate
	candidate.Source = in.Source
	candidate.GenerationMetadata = cloneMap(in.GenerationMetadata)
	candidate.Implementation.Source = cloneMap(baseline.Implementation.Source)
	candidate.Implementation.Runtime = cloneMap(baseline.Implementation.Runtime)
	for key, value := range in.ParameterPatch {
		switch key {
		case "model", "temperature", "maxTokens":
			candidate.Implementation.Runtime[key] = value
		default:
			return domain.SkillVersion{}, fmt.Errorf("parameter field is not optimizable: %s", key)
		}
	}
	for key, value := range in.PromptPatch {
		if key != "promptTemplate" && key != "systemPrompt" {
			return domain.SkillVersion{}, fmt.Errorf("prompt field is not optimizable: %s", key)
		}
		text, ok := value.(string)
		if !ok || strings.TrimSpace(text) == "" {
			return domain.SkillVersion{}, fmt.Errorf("prompt field %s must be a non-empty string", key)
		}
		candidate.Implementation.Source[key] = text
	}
	candidate.ContentHash, err = candidate.ComputeContentHash()
	if err != nil {
		return domain.SkillVersion{}, err
	}
	if err := s.repo.InsertCandidate(ctx, candidate); err != nil {
		return domain.SkillVersion{}, err
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

func (s *VersionService) UpdateCapability(ctx context.Context, skillID string, in UpdateCapabilityInput) (domain.SkillVersion, error) {
	draft, ok, err := s.repo.GetDraftVersion(ctx, skillID)
	if err != nil {
		return domain.SkillVersion{}, err
	}
	if !ok {
		return domain.SkillVersion{}, domain.ErrSkillNotFound
	}
	draft.Capability.Goal = in.Goal
	draft.Capability.WhenToUse = in.WhenToUse
	draft.Capability.InputSpec = in.InputSpec
	draft.Capability.OutputSpec = in.OutputSpec
	contentHash, err := draft.ComputeContentHash()
	if err != nil {
		return domain.SkillVersion{}, err
	}
	return s.repo.UpdateDraftCapability(ctx, skillID, draft.Capability, contentHash)
}

func (s *VersionService) UpdateContract(ctx context.Context, skillID string, in UpdateContractInput) (domain.SkillVersion, error) {
	draft, ok, err := s.repo.GetDraftVersion(ctx, skillID)
	if err != nil {
		return domain.SkillVersion{}, err
	}
	if !ok {
		return domain.SkillVersion{}, domain.ErrSkillNotFound
	}
	contract := domain.ToolContract{
		ToolName:        in.ToolName,
		Description:     in.Description,
		InputSchema:     in.InputSchema,
		OutputSchema:    in.OutputSchema,
		CallingGuidance: in.CallingGuidance,
		Confirmed:       in.Confirmed,
	}
	if contract.InputSchema == nil {
		contract.InputSchema = map[string]any{"type": "object"}
	}
	if contract.OutputSchema == nil {
		contract.OutputSchema = map[string]any{"type": "object"}
	}
	draft.ToolContract = contract
	contentHash, err := draft.ComputeContentHash()
	if err != nil {
		return domain.SkillVersion{}, err
	}
	return s.repo.UpdateDraftContract(ctx, skillID, contract, contentHash)
}

func (s *VersionService) UpdateImplementation(ctx context.Context, skillID string, in UpdateImplementationInput) (domain.SkillVersion, error) {
	draft, ok, err := s.repo.GetDraftVersion(ctx, skillID)
	if err != nil {
		return domain.SkillVersion{}, err
	}
	if !ok {
		return domain.SkillVersion{}, domain.ErrSkillNotFound
	}
	impl := domain.Implementation{
		Mode:        in.Mode,
		Source:      in.Source,
		Runtime:     in.Runtime,
		Permissions: in.Permissions,
		SecretRefs:  in.SecretRefs,
	}
	if impl.Source == nil {
		impl.Source = map[string]any{}
	}
	draft.Implementation = impl
	contentHash, err := draft.ComputeContentHash()
	if err != nil {
		return domain.SkillVersion{}, err
	}
	return s.repo.UpdateDraftImplementation(ctx, skillID, impl, contentHash)
}

var nonToolName = regexp.MustCompile(`[^a-zA-Z0-9_]+`)

func generatedToolName(name string) string {
	out := strings.ToLower(nonToolName.ReplaceAllString(name, "_"))
	out = strings.Trim(out, "_")
	if out == "" || (out[0] >= '0' && out[0] <= '9') {
		out = "skill_" + out
	}
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}
