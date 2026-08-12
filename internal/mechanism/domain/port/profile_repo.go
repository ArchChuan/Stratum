// Package port 定义 mechanism context 的消费方接口。
package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/mechanism/domain"
)

// ProfileRepo 持久化 model_profiles（public schema，global 共享，无租户维度）。
type ProfileRepo interface {
	GetByFamilyKey(ctx context.Context, familyKey string) (domain.Profile, bool, error)
	List(ctx context.Context) ([]domain.Profile, error)
	Upsert(ctx context.Context, p domain.Profile) error
}
