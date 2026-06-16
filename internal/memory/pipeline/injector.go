package pipeline

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/vector"
)

// EmbedServiceResolver resolves an embedding client for a given tenant at call time.
// Returns nil if the tenant has no embedding capability configured.
type EmbedServiceResolver func(ctx context.Context, tenantID string) EmbedClient

// MemoryInjector fetches memory context (summaries, entities, long-term vectors)
// and formats it for injection into the agent's system prompt.
type MemoryInjector struct {
	pool          *pgxpool.Pool
	logger        *zap.Logger
	embedSvc      EmbedClient
	embedResolver EmbedServiceResolver
	vectorDB      *vector.VectorStore
}

// NewMemoryInjector creates a MemoryInjector backed by the given pool.
// embedSvc and vectorDB are optional; if nil, long-term vector retrieval is skipped
// unless embedResolver is set (see SetEmbedResolver).
func NewMemoryInjector(pool *pgxpool.Pool, logger *zap.Logger, embedSvc EmbedClient, vectorDB *vector.VectorStore) *MemoryInjector {
	return &MemoryInjector{pool: pool, logger: logger, embedSvc: embedSvc, vectorDB: vectorDB}
}

// SetEmbedResolver sets a per-tenant embedding resolver used when the global embedSvc is nil.
func (inj *MemoryInjector) SetEmbedResolver(r EmbedServiceResolver) {
	inj.embedResolver = r
}

// Pool returns the underlying connection pool (used by RecallHandler).
func (inj *MemoryInjector) Pool() *pgxpool.Pool {
	return inj.pool
}

// InjectionContext carries the identifiers needed to look up relevant memory.
type InjectionContext struct {
	TenantID       string
	UserID         string
	AgentID        string
	ConversationID string
	Query          string // user input text; used for long-term vector search
}

// BuildContext fetches the latest conversation summary and top entities,
// returning a formatted string suitable for prepending to the system prompt.
// Returns ("", nil) when no memory context is available.
func (inj *MemoryInjector) BuildContext(ctx context.Context, ic InjectionContext) (string, error) {
	schema := "tenant_" + ic.TenantID

	tx, err := inj.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, fmt.Sprintf("SET LOCAL search_path = %s, public", pgx.Identifier{schema}.Sanitize())); err != nil {
		return "", fmt.Errorf("set schema: %w", err)
	}

	// Fetch latest summary for this conversation
	var summary string
	err = tx.QueryRow(ctx,
		"SELECT summary FROM memory_summaries WHERE conversation_id = $1 ORDER BY created_at DESC LIMIT 1",
		ic.ConversationID).Scan(&summary)
	if err != nil && err != pgx.ErrNoRows {
		return "", fmt.Errorf("fetch summary: %w", err)
	}

	// Fetch top entities ordered by last_seen
	rows, err := tx.Query(ctx, `
		SELECT name FROM entities
		WHERE user_id = $1
		ORDER BY last_seen DESC
		LIMIT $2`,
		ic.UserID, constants.EnricherTopEntities)
	if err != nil {
		return "", fmt.Errorf("fetch entities: %w", err)
	}
	defer rows.Close()

	var entityNames []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			continue
		}
		entityNames = append(entityNames, name)
	}

	if summary == "" && len(entityNames) == 0 {
		return "", nil
	}

	var sb strings.Builder
	sb.WriteString("[Memory Context]\n")
	if summary != "" {
		sb.WriteString("Summary: ")
		sb.WriteString(summary)
		sb.WriteString("\n")
	}
	if len(entityNames) > 0 {
		sb.WriteString("Key Entities: ")
		sb.WriteString(strings.Join(entityNames, ", "))
		sb.WriteString("\n")
	}

	return sb.String(), nil
}

func (inj *MemoryInjector) EmbedResolver() EmbedServiceResolver { return inj.embedResolver }

func (inj *MemoryInjector) VectorDB() *vector.VectorStore { return inj.vectorDB }

func (inj *MemoryInjector) EmbedSvc() EmbedClient { return inj.embedSvc }
