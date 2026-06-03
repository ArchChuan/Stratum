// Package orchestrator provides task orchestration and coordination.
package orchestrator

import (
	"context"
	"fmt"
	"sync"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/skill"
	"github.com/byteBuilderX/ClawHermes-AI-Go/pkg/tenantdb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Registry struct {
	skills map[string]skill.Skill
	mu     sync.RWMutex
	pool   *pgxpool.Pool
}

func NewRegistry(pool *pgxpool.Pool) *Registry {
	return &Registry{
		skills: make(map[string]skill.Skill),
		pool:   pool,
	}
}

func (r *Registry) Register(ctx context.Context, id string, s skill.Skill) {
	r.mu.Lock()
	r.skills[id] = s
	r.mu.Unlock()

	if r.pool != nil {
		_ = tenantdb.ExecTenant(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `
				INSERT INTO skills (id, name, description, type)
				VALUES ($1, $2, $3, $4)
				ON CONFLICT (id) DO UPDATE SET name=$2, description=$3, type=$4`,
				id, s.GetName(), s.GetDescription(), s.GetType())
			return err
		})
	}
}

func (r *Registry) Get(id string) (skill.Skill, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.skills[id]
	return s, ok
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

func (r *Registry) Remove(ctx context.Context, id string) error {
	r.mu.Lock()
	_, ok := r.skills[id]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("skill not found: %s", id)
	}
	delete(r.skills, id)
	r.mu.Unlock()

	if r.pool != nil {
		_ = tenantdb.ExecTenant(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
			_, err := tx.Exec(ctx, `DELETE FROM skills WHERE id=$1`, id)
			return err
		})
	}
	return nil
}
