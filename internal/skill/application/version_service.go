package application

import (
	"context"
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
		ID:      draftID,
		SkillID: skillID,
		Status:  domain.VersionStatusDraft,
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
	return s.repo.UpdateDraftCapability(ctx, skillID, draft.Capability)
}

func (s *VersionService) UpdateContract(ctx context.Context, skillID string, in UpdateContractInput) (domain.SkillVersion, error) {
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
	return s.repo.UpdateDraftContract(ctx, skillID, contract)
}

func (s *VersionService) UpdateImplementation(ctx context.Context, skillID string, in UpdateImplementationInput) (domain.SkillVersion, error) {
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
	return s.repo.UpdateDraftImplementation(ctx, skillID, impl)
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
