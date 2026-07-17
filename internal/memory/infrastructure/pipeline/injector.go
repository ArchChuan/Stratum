package pipeline

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/internal/memory/infrastructure/persistence"
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
	snapshotRepo  port.ActiveSnapshotRepo
}

// NewMemoryInjector creates a MemoryInjector backed by the given pool.
// embedSvc and vectorDB are optional; if nil, long-term vector retrieval is skipped
// unless embedResolver is set (see SetEmbedResolver).
func NewMemoryInjector(pool *pgxpool.Pool, logger *zap.Logger, embedSvc EmbedClient, vectorDB *vector.VectorStore) *MemoryInjector {
	return &MemoryInjector{pool: pool, logger: logger, embedSvc: embedSvc, vectorDB: vectorDB, snapshotRepo: persistence.NewActiveSnapshotRepo(pool)}
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
	Query          string
	Scope          string // "user" or "agent"
}

// BuildContext fetches the latest conversation summary and top entities,
// returning a formatted string suitable for prepending to the system prompt.
// Returns ("", nil) when no memory context is available.
func (inj *MemoryInjector) BuildContext(ctx context.Context, ic InjectionContext) (string, error) {
	snapshot := inj.loadActiveSnapshot(ctx, ic)
	if inj.pool == nil {
		return renderMemoryContext(snapshot, "", nil, nil, nil, constants.MemoryInjectionCharBudget), nil
	}
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

	// Fetch top entities filtered by scope
	var rows pgx.Rows
	if ic.Scope == "agent" && ic.AgentID != "" {
		rows, err = tx.Query(ctx, `
			SELECT name FROM memory_entities
			WHERE user_id = $1 AND agent_id = $2 AND scope = 'agent' AND status = 'active'
			ORDER BY last_seen_at DESC
			LIMIT $3`,
			ic.UserID, ic.AgentID, constants.EnricherTopEntities)
	} else {
		rows, err = tx.Query(ctx, `
			SELECT name FROM memory_entities
			WHERE user_id = $1 AND scope = 'user' AND status = 'active'
			ORDER BY last_seen_at DESC
			LIMIT $2`,
			ic.UserID, constants.EnricherTopEntities)
	}
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
	rows.Close() // must close before issuing next query on same tx

	// Fetch top long-term facts ordered by frecency score
	inj.logger.Debug("buildcontext: querying facts", zap.String("user_id", ic.UserID), zap.String("agent_id", ic.AgentID), zap.String("tenant_id", ic.TenantID))
	var facts []factRow
	factCtx, cancelFacts := context.WithTimeout(ctx, constants.FactInjectionTimeout)
	defer cancelFacts()
	factRows, err := tx.Query(factCtx, factInjectionQuery(),
		ic.UserID, ic.AgentID, ic.Query, constants.FactInjectionConfidenceMin, constants.FactInjectionTopN)
	if err == nil {
		for factRows.Next() {
			var fact factRow
			if err := factRows.Scan(&fact.content, &fact.category); err == nil {
				facts = append(facts, fact)
			}
		}
		factRows.Close()
		inj.logger.Debug("buildcontext: facts loaded", zap.Int("count", len(facts)))
	} else {
		inj.logger.Warn("buildcontext: facts query failed", zap.Error(err))
	}

	var history []historyRow
	historyCtx, cancelHistory := context.WithTimeout(ctx, constants.HistoryReadTimeout)
	historyRows, historyErr := tx.Query(historyCtx, historyInjectionQuery(), ic.UserID, ic.AgentID, ic.Query, constants.HistoryInjectionTopN)
	if historyErr == nil {
		for historyRows.Next() {
			var row historyRow
			if err := historyRows.Scan(&row.summary, &row.tier); err == nil {
				history = append(history, row)
			}
		}
		historyRows.Close()
	} else {
		inj.logger.Warn("memory.injector.history_failed", zap.Error(historyErr))
	}
	cancelHistory()

	if snapshot == nil && summary == "" && len(entityNames) == 0 && len(facts) == 0 && len(history) == 0 {
		return "", nil
	}
	return renderMemoryContext(snapshot, summary, entityNames, facts, history, constants.MemoryInjectionCharBudget), nil
}

func (inj *MemoryInjector) loadActiveSnapshot(ctx context.Context, ic InjectionContext) *domain.ActiveSnapshot {
	if inj.snapshotRepo == nil || ic.TenantID == "" || ic.UserID == "" || ic.AgentID == "" {
		return nil
	}
	readCtx, cancel := context.WithTimeout(ctx, constants.ActiveSnapshotReadTimeout)
	defer cancel()
	snapshot, err := inj.snapshotRepo.Get(readCtx, ic.TenantID, ic.UserID, ic.AgentID)
	if err != nil {
		inj.logger.Warn("memory.injector.active_snapshot_failed", zap.Error(err))
		return nil
	}
	return snapshot
}

func renderMemoryContext(snapshot *domain.ActiveSnapshot, summary string, entities []string, facts []factRow, history []historyRow, budget int) string {
	if snapshot == nil && summary == "" && len(entities) == 0 && len(facts) == 0 && len(history) == 0 {
		return ""
	}
	var sb strings.Builder
	historyQuota := 0
	if len(history) > 0 && budget > 0 {
		historyQuota = min(constants.HistoryInjectionCharBudget, budget/3)
	}
	preHistoryBudget := budget
	if preHistoryBudget > 0 {
		preHistoryBudget -= historyQuota
	}
	appendBounded(&sb, "[Memory Context]\n", budget)
	appendBounded(&sb, formatActiveSnapshot(snapshot, constants.ActiveSnapshotInjectionBudget), preHistoryBudget)
	if len(facts) > 0 {
		appendBounded(&sb, "Long-term facts:\n", preHistoryBudget)
		for _, line := range formatFactLines(facts, constants.FactInjectionCharBudget) {
			appendBounded(&sb, line, preHistoryBudget)
		}
	}
	if len(history) > 0 {
		var historySection strings.Builder
		appendBounded(&historySection, "History:\n", historyQuota)
		for _, line := range formatHistoryLines(history, constants.HistoryInjectionCharBudget) {
			appendBounded(&historySection, line, historyQuota)
		}
		appendBounded(&sb, historySection.String(), budget)
	}
	if summary != "" {
		appendBounded(&sb, "Summary: "+summary+"\n", budget)
	}
	if len(entities) > 0 {
		appendBounded(&sb, "Key Entities: "+strings.Join(entities, ", ")+"\n", budget)
	}
	return sb.String()
}

func formatActiveSnapshot(snapshot *domain.ActiveSnapshot, budget int) string {
	if snapshot == nil {
		return ""
	}
	var sb strings.Builder
	appendBounded(&sb, "Active snapshot:\n", budget)
	for _, section := range []struct {
		label string
		items []string
	}{{"Work", snapshot.WorkContext}, {"Personal", snapshot.PersonalContext}, {"Top of mind", snapshot.TopOfMind}} {
		for _, item := range section.items {
			appendBounded(&sb, section.label+": "+item+"\n", budget)
		}
	}
	return sb.String()
}

func appendBounded(sb *strings.Builder, value string, budget int) {
	if value == "" || (budget > 0 && len([]rune(sb.String())) >= budget) {
		return
	}
	runes := []rune(value)
	if budget > 0 {
		remaining := budget - len([]rune(sb.String()))
		if len(runes) > remaining {
			runes = runes[:remaining]
		}
	}
	sb.WriteString(string(runes))
}

// factRow carries the fields needed to render a long-term fact for injection.
type factRow struct {
	content  string
	category string
}

type historyRow struct{ summary, tier string }

func historyInjectionQuery() string {
	return `
	SELECT summary, tier FROM memory_summaries
	WHERE user_id = $1 AND status = 'active' AND aggregation_key IS NOT NULL
	  AND (scope = 'user' OR (scope = 'agent' AND agent_id = $2))
	ORDER BY similarity(summary, $3) DESC, importance DESC, confidence DESC, period_end DESC
	LIMIT $4`
}

func formatHistoryLines(rows []historyRow, budget int) []string {
	var out []string
	remaining := budget
	for _, row := range rows {
		line := []rune(fmt.Sprintf("- [%s] %s\n", row.tier, row.summary))
		if budget > 0 && len(line) > remaining {
			if remaining > 0 {
				out = append(out, string(line[:remaining]))
			}
			break
		}
		out = append(out, string(line))
		if budget > 0 {
			remaining -= len(line)
		}
	}
	return out
}

func factInjectionQuery() string {
	return `
		SELECT content, category FROM memory_facts
		WHERE user_id = $1 AND status = 'active'
			AND (scope = 'user' OR (scope = 'agent' AND agent_id = $2))
			AND confidence >= $4
		ORDER BY similarity(content, $3) DESC, frecency_score DESC, confidence DESC, importance DESC
		LIMIT $5`
}

// formatFactLines renders facts as "- [category] content" lines, accumulating
// rendered length until charBudget is exhausted; facts that would overflow the
// budget are truncated so the injected block stays bounded regardless of how many
// facts the query returns. A non-positive budget means "no limit". Returns nil
// for empty input. category defaults to "other" when blank so the annotation is
// always well-formed.
func formatFactLines(facts []factRow, charBudget int) []string {
	var lines []string
	remaining := charBudget
	for _, fact := range facts {
		category := fact.category
		if category == "" {
			category = "other"
		}
		line := []rune(fmt.Sprintf("- [%s] %s\n", category, fact.content))
		if charBudget > 0 && len(line) > remaining {
			if remaining > 0 {
				lines = append(lines, string(line[:remaining]))
			}
			break
		}
		lines = append(lines, string(line))
		if charBudget > 0 {
			remaining -= len(line)
			if remaining == 0 {
				break
			}
		}
	}
	return lines
}

func (inj *MemoryInjector) EmbedResolver() EmbedServiceResolver { return inj.embedResolver }

func (inj *MemoryInjector) VectorDB() *vector.VectorStore { return inj.vectorDB }

func (inj *MemoryInjector) EmbedSvc() EmbedClient { return inj.embedSvc }
