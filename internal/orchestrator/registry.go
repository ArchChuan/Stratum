// Package orchestrator provides task orchestration and coordination.
package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/byteBuilderX/stratum/internal/llmgateway"
	"github.com/byteBuilderX/stratum/internal/skill"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

type configurable interface {
	GetConfig() map[string]any
}

type Registry struct {
	skills    map[string]skill.Skill
	createdAt map[string]time.Time
	mu        sync.RWMutex
	pool      *pgxpool.Pool
}

func NewRegistry(pool *pgxpool.Pool) *Registry {
	return &Registry{
		skills:    make(map[string]skill.Skill),
		createdAt: make(map[string]time.Time),
		pool:      pool,
	}
}

// ErrNameConflict is returned by Register when a skill with the same name already exists in the tenant.
var ErrNameConflict = errors.New("skill name already exists")

func (r *Registry) Register(ctx context.Context, id string, s skill.Skill) error {
	now := time.Now()
	r.mu.Lock()
	r.skills[id] = s
	r.createdAt[id] = now
	r.mu.Unlock()

	if r.pool != nil {
		if err := tenantdb.ExecTenant(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
			cfg := []byte("{}")
			if c, ok := s.(configurable); ok {
				if b, err := json.Marshal(c.GetConfig()); err == nil {
					cfg = b
				}
			}
			_, err := tx.Exec(ctx, `
				INSERT INTO skills (id, name, description, type, config)
				VALUES ($1, $2, $3, $4, $5)
				ON CONFLICT (id) DO UPDATE SET name=$2, description=$3, type=$4, config=$5`,
				id, s.GetName(), s.GetDescription(), s.GetType(), cfg)
			return err
		}); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23505" {
				r.mu.Lock()
				delete(r.skills, id)
				delete(r.createdAt, id)
				r.mu.Unlock()
				return fmt.Errorf("%w: skill name %q", ErrNameConflict, s.GetName())
			}
			return fmt.Errorf("persist skill %s: %w", id, err)
		}
	}
	return nil
}

func (r *Registry) Get(id string) (skill.Skill, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.skills[id]
	return s, ok
}

// GetCreatedAt returns the registration time of a skill.
func (r *Registry) GetCreatedAt(id string) (time.Time, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.createdAt[id]
	return t, ok
}

func (r *Registry) GetAll() []skill.Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()
	skillList := make([]skill.Skill, 0, len(r.skills))
	for _, s := range r.skills {
		skillList = append(skillList, s)
	}
	return skillList
}

// ErrSkillInUse is returned when a skill cannot be deleted because it is still linked to agents.
var ErrSkillInUse = errors.New("skill still linked to agents")

func (r *Registry) Remove(ctx context.Context, id string) error {
	r.mu.RLock()
	_, ok := r.skills[id]
	r.mu.RUnlock()
	if !ok {
		return fmt.Errorf("skill not found: %s", id)
	}

	if r.pool != nil {
		if err := tenantdb.ExecTenant(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `DELETE FROM skills WHERE id=$1`, id)
			return err
		}); err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == "23503" {
				return fmt.Errorf("%w: unlink skill %s from all agents first", ErrSkillInUse, id)
			}
			return err
		}
	}

	r.mu.Lock()
	delete(r.skills, id)
	delete(r.createdAt, id)
	r.mu.Unlock()
	return nil
}

// LoadAll restores all skills persisted in the current tenant's schema into the in-memory registry.
// Must be called with a ctx that has a valid TenantContext.
func (r *Registry) LoadAll(ctx context.Context, gw *llmgateway.Gateway, logger *zap.Logger, codeExec *skill.CodeExecutor) error {
	if r.pool == nil {
		return nil
	}
	type row struct {
		id          string
		name        string
		description string
		skillType   string
		config      []byte
		createdAt   time.Time
	}
	var rows []row
	if err := tenantdb.ExecTenant(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		pgrows, err := tx.Query(ctx, `SELECT id, name, description, type, config, created_at FROM skills`)
		if err != nil {
			return err
		}
		defer pgrows.Close()
		for pgrows.Next() {
			var ro row
			if err := pgrows.Scan(&ro.id, &ro.name, &ro.description, &ro.skillType, &ro.config, &ro.createdAt); err != nil {
				return err
			}
			rows = append(rows, ro)
		}
		return pgrows.Err()
	}); err != nil {
		return fmt.Errorf("registry.LoadAll: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	for _, ro := range rows {
		var cfg map[string]any
		_ = json.Unmarshal(ro.config, &cfg)
		if cfg == nil {
			cfg = map[string]any{}
		}
		var s skill.Skill
		switch ro.skillType {
		case "http":
			headers := map[string]string{}
			if h, ok := cfg["headers"].(map[string]any); ok {
				for k, v := range h {
					if sv, ok := v.(string); ok {
						headers[k] = sv
					}
				}
			}
			hs, err := skill.NewHTTPSkill(
				ro.id, ro.name, ro.description,
				stringVal(cfg, "url"), stringVal(cfg, "method"),
				headers, stringVal(cfg, "body_template"), intVal(cfg, "timeout_sec"),
			)
			if err != nil {
				logger.Warn("LoadAll: skip invalid http skill", zap.String("id", ro.id), zap.Error(err))
				continue
			}
			s = hs
		case "llm":
			s = skill.NewLLMSkill(
				ro.id, ro.name, ro.description,
				stringVal(cfg, "system_prompt"), stringVal(cfg, "model"),
				float32Val(cfg, "temperature"), intVal(cfg, "max_tokens"),
				gw, logger,
			)
		case "code":
			s = skill.NewCodeSkillWithExecutor(
				ro.id, ro.name, ro.description,
				stringVal(cfg, "code"), stringVal(cfg, "language"),
				codeExec,
			)
		default:
			logger.Warn("LoadAll: unknown skill type", zap.String("id", ro.id), zap.String("type", ro.skillType))
			continue
		}
		r.skills[ro.id] = s
		r.createdAt[ro.id] = ro.createdAt
	}
	return nil
}

func stringVal(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func intVal(m map[string]any, key string) int {
	switch v := m[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}

func float32Val(m map[string]any, key string) float32 {
	if v, ok := m[key].(float64); ok {
		return float32(v)
	}
	return 0
}
