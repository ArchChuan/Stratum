package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/prompt/domain"
)

// PromptRepo manages prompt template CRUD and versioning.
type PromptRepo interface {
	Insert(ctx context.Context, tmpl domain.PromptTemplate) error
	GetByKey(ctx context.Context, key string, tenantID *string) ([]domain.PromptTemplate, error)
	GetVersion(ctx context.Context, key string, version int, tenantID *string) (*domain.PromptTemplate, error)
	GetLatestPublished(ctx context.Context, key string, tenantID *string) (*domain.PromptTemplate, error)
	UpdateStatus(ctx context.Context, key string, version int, tenantID *string, status domain.PromptStatus) error
	GetByHash(ctx context.Context, hash string) (*domain.PromptTemplate, error)
	// ListByKey returns the latest version row of every prompt key for a
	// tenant (nil = global), ordered by version descending, plus the total
	// distinct key count for pagination.
	ListByKey(ctx context.Context, tenantID *string, limit, offset int) ([]domain.PromptTemplate, int, error)
}

// BindingRepo manages prompt version bindings for tenant/agent scopes.
type BindingRepo interface {
	UpsertBinding(ctx context.Context, binding domain.PromptBinding) error
	GetBinding(ctx context.Context, key, scope string) (*domain.PromptBinding, error)
	ListBindings(ctx context.Context, scope string) ([]domain.PromptBinding, error)
	DeleteBinding(ctx context.Context, key, scope string) error
}
