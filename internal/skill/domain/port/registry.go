package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/skill/domain"
)

type SkillRepo interface {
	Get(ctx context.Context, id string) (*domain.Skill, error)
	List(ctx context.Context) ([]*domain.Skill, error)
	Save(ctx context.Context, s *domain.Skill) error
	Delete(ctx context.Context, id string) error
}
