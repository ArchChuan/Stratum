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

	llmdomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
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

func viewFromRow(r port.SkillRow) SkillView {
	return SkillView{
		ID: r.ID, Name: r.Name, Description: r.Description,
		Type: r.Type, Config: r.Config, CreatedAt: r.CreatedAt,
	}
}

// SkillService orchestrates skill CRUD and execution.
type SkillService struct {
	repo      port.SkillRepo
	completer llmdomain.LLMCompleter
	factory   port.SkillFactory
	logger    *zap.Logger
}

// NewSkillService wires the repo + LLM completer + skill factory.
func NewSkillService(
	repo port.SkillRepo,
	completer llmdomain.LLMCompleter,
	factory port.SkillFactory,
	logger *zap.Logger,
) *SkillService {
	return &SkillService{
		repo:      repo,
		completer: completer,
		factory:   factory,
		logger:    logger,
	}
}

// Create validates, persists, and returns the new skill view.
func (s *SkillService) Create(ctx context.Context, in SkillInput) (SkillView, error) {
	id := uuid.New().String()
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

// Run executes a code skill on demand. Non-code skills return ErrNotCodeSkill.
func (s *SkillService) Run(ctx context.Context, id, tenantID string, input map[string]any) (any, error) {
	typ, cfg, err := s.repo.GetTypeAndConfig(ctx, id)
	if err != nil {
		return nil, err
	}
	if typ != "code" {
		return nil, domain.ErrNotCodeSkill
	}

	codeIn := port.SkillInput{
		Type:     "code",
		Code:     stringCfgVal(cfg, "code"),
		Language: stringCfgVal(cfg, "language"),
	}
	sk, err := s.buildSkill(id, codeIn)
	if err != nil {
		return nil, err
	}
	type codeExecutor interface {
		Execute(ctx context.Context, input map[string]any) (map[string]any, error)
	}
	ex, ok := sk.(codeExecutor)
	if !ok {
		return nil, fmt.Errorf("skill %s does not support direct execution", id)
	}

	if input == nil {
		input = make(map[string]any)
	}
	if tenantID != "" {
		input["__tenant_id"] = tenantID
	}

	start := time.Now()
	out, err := ex.Execute(ctx, input)
	if err != nil {
		return nil, err
	}
	s.logger.Info("skill executed",
		zap.String("id", id),
		zap.Int64("latency_ms", time.Since(start).Milliseconds()),
	)
	return out, nil
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

func stringCfgVal(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
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
