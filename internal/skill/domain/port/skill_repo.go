package port

import (
	"context"
	"time"
)

// SkillRow captures the persistent shape of a stored skill, agnostic of execution type.
type SkillRow struct {
	ID          string
	Name        string
	Description string
	Type        string
	Config      map[string]any
	CreatedAt   time.Time
}

// SkillRepo persists skill rows in the tenant schema.
type SkillRepo interface {
	Insert(ctx context.Context, row SkillRow) (time.Time, error)
	Get(ctx context.Context, id string) (SkillRow, bool, error)
	List(ctx context.Context) ([]SkillRow, error)
	GetType(ctx context.Context, id string) (string, error)
	Update(ctx context.Context, row SkillRow) (time.Time, error)
	Delete(ctx context.Context, id string) error
	GetTypeAndConfig(ctx context.Context, id string) (typ string, cfg map[string]any, err error)
}
