// Package application — skill use-case orchestration. Owns the create/get/list/
// update/delete/run workflows; depends only on domain/port + domain entities,
// never on pgx/redis/gin/http.
package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/byteBuilderX/stratum/internal/skill/domain"
	"github.com/byteBuilderX/stratum/internal/skill/domain/port"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// SkillInput is a type alias to port.SkillInput so callers can use either package.
type SkillInput = port.SkillInput

// SkillView is the application-layer view of a stored skill row.
type SkillView struct {
	ID          string
	Name        string
	Description string
	Type        string
	Config      map[string]any
	CreatedAt   time.Time
}

// SkillTestResult is the application-layer result for a manual skill test run.
type SkillTestResult struct {
	TraceID  string
	SkillID  string
	Output   any
	Duration time.Duration
}

// SkillRunner executes a stored skill through the runtime gateway.
type SkillRunner interface {
	RunSkill(ctx context.Context, skillID string, input any, traceID string) (SkillTestResult, error)
}

func viewFromRow(r port.SkillRow) SkillView {
	return SkillView{
		ID: r.ID, Name: r.Name, Description: r.Description,
		Type: r.Type, Config: r.Config, CreatedAt: r.CreatedAt,
	}
}

// SkillService orchestrates skill CRUD and execution.
type SkillService struct {
	repo    port.SkillRepo
	factory port.SkillFactory
	runner  SkillRunner
	logger  *zap.Logger
}

// NewSkillService wires the repo + skill factory.
func NewSkillService(
	repo port.SkillRepo,
	factory port.SkillFactory,
	logger *zap.Logger,
	runner ...SkillRunner,
) *SkillService {
	var r SkillRunner
	if len(runner) > 0 {
		r = runner[0]
	}
	return &SkillService{
		repo:    repo,
		factory: factory,
		runner:  r,
		logger:  logger,
	}
}

// Create validates, persists, and returns the new skill view.
func (s *SkillService) Create(ctx context.Context, in SkillInput) (SkillView, error) {
	id := uuid.Must(uuid.NewV7()).String()
	skill, err := s.buildSkill(id, in)
	if err != nil {
		return SkillView{}, err
	}
	cfg := skillConfig(skill)
	row := port.SkillRow{
		ID:          id,
		Name:        skill.GetName(),
		Description: skill.GetDescription(),
		Type:        skill.GetType(),
		Config:      cfg,
	}
	createdAt, err := s.repo.Insert(ctx, row)
	if err != nil {
		return SkillView{}, err
	}
	row.CreatedAt = createdAt
	s.logger.Info("skill created", zap.String("id", id), zap.String("name", in.Name))
	return viewFromRow(row), nil
}

// Get fetches a skill view by id.
func (s *SkillService) Get(ctx context.Context, id string) (SkillView, error) {
	row, ok, err := s.repo.Get(ctx, id)
	if err != nil {
		return SkillView{}, err
	}
	if !ok {
		return SkillView{}, domain.ErrSkillNotFound
	}
	return viewFromRow(row), nil
}

// List returns all skills for the tenant.
func (s *SkillService) List(ctx context.Context) ([]SkillView, error) {
	rows, err := s.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]SkillView, 0, len(rows))
	for _, r := range rows {
		out = append(out, viewFromRow(r))
	}
	return out, nil
}

// Update validates type immutability and overwrites config/name/description.
func (s *SkillService) Update(ctx context.Context, id string, in SkillInput) (SkillView, error) {
	existingType, err := s.repo.GetType(ctx, id)
	if err != nil {
		return SkillView{}, err
	}
	if existingType != in.Type {
		return SkillView{}, domain.ErrSkillTypeImmutable
	}

	skill, err := s.buildSkill(id, in)
	if err != nil {
		return SkillView{}, err
	}
	cfg := skillConfig(skill)
	row := port.SkillRow{
		ID:          id,
		Name:        skill.GetName(),
		Description: skill.GetDescription(),
		Type:        skill.GetType(),
		Config:      cfg,
	}
	createdAt, err := s.repo.Update(ctx, row)
	if err != nil {
		return SkillView{}, err
	}
	row.CreatedAt = createdAt
	s.logger.Info("skill updated", zap.String("id", id))
	return viewFromRow(row), nil
}

// Delete removes a skill row by id.
func (s *SkillService) Delete(ctx context.Context, id string) error {
	if err := s.repo.Delete(ctx, id); err != nil {
		return err
	}
	s.logger.Info("skill deleted", zap.String("id", id))
	return nil
}

// RunSkillTest executes a persisted skill with caller-provided input.
func (s *SkillService) RunSkillTest(ctx context.Context, skillID string, input any, traceID string) (SkillTestResult, error) {
	if s.runner == nil {
		return SkillTestResult{}, fmt.Errorf("skill runner not configured")
	}
	return s.runner.RunSkill(ctx, skillID, normalizeSkillTestInput(input), traceID)
}

// RunDraftSkill builds and executes a skill without persisting it.
func (s *SkillService) RunDraftSkill(ctx context.Context, in SkillInput, input any, traceID string) (SkillTestResult, error) {
	id := "draft-" + uuid.Must(uuid.NewV7()).String()
	skill, err := s.buildSkill(id, in)
	if err != nil {
		return SkillTestResult{}, err
	}
	executor, ok := skill.(domain.SkillExecutor)
	if !ok {
		return SkillTestResult{}, fmt.Errorf("skill is not executable: %s", id)
	}

	start := time.Now()
	output, err := executor.Execute(ctx, normalizeSkillTestInput(input))
	if err != nil {
		return SkillTestResult{}, err
	}
	return SkillTestResult{
		TraceID:  traceID,
		SkillID:  id,
		Output:   output,
		Duration: time.Since(start),
	}, nil
}

func normalizeSkillTestInput(input any) any {
	switch v := input.(type) {
	case string:
		return map[string]any{"prompt": v, "input": v}
	case map[string]any:
		if _, ok := v["prompt"]; ok {
			if _, hasInput := v["input"]; !hasInput {
				v["input"] = v["prompt"]
			}
			return v
		}
		if raw, ok := v["input"]; ok {
			v["prompt"] = raw
			return v
		}
		if raw, ok := v["text"]; ok {
			v["prompt"] = raw
			v["input"] = raw
			return v
		}
		return v
	default:
		return input
	}
}

// buildSkill delegates to the injected factory.
func (s *SkillService) buildSkill(id string, in SkillInput) (domain.Skill, error) {
	if s.factory == nil {
		return nil, fmt.Errorf("%w: %s", domain.ErrSkillUnsupportedType, in.Type)
	}
	return s.factory.Build(id, in)
}

// configurable identifies skills that expose a config map.
type configurable interface {
	GetConfig() map[string]any
}

func skillConfig(sk domain.Skill) map[string]any {
	if c, ok := sk.(configurable); ok {
		return c.GetConfig()
	}
	return map[string]any{}
}

// MarshalConfig is a tiny helper for callers that want JSON of a config map.
func MarshalConfig(cfg map[string]any) ([]byte, error) {
	return json.Marshal(cfg)
}

// IsAnalysisError reports whether err is a domain.AnalysisError and returns its reasons.
func IsAnalysisError(err error) ([]string, bool) {
	var aErr *domain.AnalysisError
	if errors.As(err, &aErr) {
		return aErr.Reasons, true
	}
	return nil, false
}
