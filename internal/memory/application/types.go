// Package application provides agent memory management orchestration.
package application

import (
	"context"
	"errors"

	memdomain "github.com/byteBuilderX/stratum/internal/memory/domain"
)

// ErrNotFound aliases domain.ErrEntryNotFound to keep middleware/error_mapping
// route source-compatible during refactor.
var ErrNotFound = memdomain.ErrEntryNotFound

// ErrMemoryEmbeddingUnavailable 表示嵌入模型未配置或调用失败（spec §5：嵌入
// 失败 fail-closed 返回 502，不写任何数据）。errors.Is 可用于 handle 路径。
var ErrMemoryEmbeddingUnavailable = errors.New("memory embedding unavailable")

// tenantIDKey is the context key for tenant ID.
type tenantIDKey struct{}

// Re-exports of domain types/values so existing consumers (handler, agent
// application, wiring, tests) remain source-compatible while the canonical
// definitions live in domain/.
type (
	MemoryType          = memdomain.MemoryType
	MemoryEntry         = memdomain.MemoryEntry
	TenantContext       = memdomain.TenantContext
	UserContext         = memdomain.UserContext
	SessionContext      = memdomain.SessionContext
	TimeRange           = memdomain.TimeRange
	MemorySearchRequest = memdomain.MemorySearchRequest
	MemorySearchResult  = memdomain.MemorySearchResult
	MemoryStats         = memdomain.MemoryStats
	Entity              = memdomain.Entity
	MemoryEvent         = memdomain.MemoryEvent
)

const (
	ShortTermMemory  = memdomain.ShortTermMemory
	LongTermMemory   = memdomain.LongTermMemory
	EntityTypeMemory = memdomain.EntityTypeMemory
	SummaryMemory    = memdomain.SummaryMemory
)

// WithTenantContext injects tenantID into ctx for MemoryManager methods.
func WithTenantContext(ctx context.Context, tenantID string) context.Context {
	return context.WithValue(ctx, tenantIDKey{}, tenantID)
}
