// Package orchestrator provides task orchestration and coordination.
package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/skill"
	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/tenantdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
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
