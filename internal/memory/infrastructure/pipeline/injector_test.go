package pipeline

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/pashagolub/pgxmock/v2"
	"go.uber.org/zap"
)

func TestMemoryInjectorBuildContextEntityScopeMatrix(t *testing.T) {
	tests := []struct {
		name             string
		scope            string
		agentID          string
		includeUser      bool
		includeAgent     bool
		returnedEntities []string
		wantEntities     []string
	}{
		{name: "user_scope", scope: "user", agentID: "agent-1", includeUser: true, includeAgent: true, returnedEntities: []string{"shared entity", "private entity"}, wantEntities: []string{"shared entity", "private entity"}},
		{name: "agent_scope", scope: "agent", agentID: "agent-1", includeAgent: true, returnedEntities: []string{"private entity"}, wantEntities: []string{"private entity"}},
		{name: "invalid_scope", scope: "invalid", agentID: "agent-1"},
		{name: "empty_scope", agentID: "agent-1"},
		{name: "user_scope_without_agent", scope: "user", includeUser: true, includeAgent: true, returnedEntities: []string{"shared entity"}, wantEntities: []string{"shared entity"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool, err := pgxmock.NewPool()
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			pool.ExpectBegin()
			pool.ExpectExec("SET LOCAL search_path").WillReturnResult(pgxmock.NewResult("SET", 0))
			pool.ExpectQuery(regexp.QuoteMeta("SELECT summary FROM memory_summaries")).
				WithArgs("conversation-1", tt.includeUser, tt.agentID, tt.includeAgent).
				WillReturnRows(pgxmock.NewRows([]string{"summary"}))
			entityRows := pgxmock.NewRows([]string{"name"})
			for _, entity := range tt.returnedEntities {
				entityRows.AddRow(entity)
			}
			pool.ExpectQuery(`(?s)SELECT name FROM memory_entities.*scope = 'user' AND \$2 = true.*scope = 'agent' AND agent_id = \$3 AND \$4 = true`).
				WithArgs("user-1", tt.includeUser, tt.agentID, tt.includeAgent, constants.EnricherTopEntities).
				WillReturnRows(entityRows)
			pool.ExpectQuery(regexp.QuoteMeta(strings.TrimSpace(factInjectionQuery()))).
				WithArgs("user-1", tt.agentID, "query", constants.FactInjectionConfidenceMin, constants.FactInjectionTopN, tt.includeUser, tt.includeAgent).
				WillReturnRows(pgxmock.NewRows([]string{"content", "category"}))
			pool.ExpectQuery(regexp.QuoteMeta(strings.TrimSpace(historyInjectionQuery()))).
				WithArgs("user-1", tt.agentID, "query", constants.HistoryInjectionTopN, tt.includeUser, tt.includeAgent).
				WillReturnRows(pgxmock.NewRows([]string{"summary", "tier"}))
			pool.ExpectRollback()

			inj := &MemoryInjector{txBeginner: pool, logger: zap.NewNop()}
			got, err := inj.BuildContext(context.Background(), InjectionContext{
				TenantID: snapshotTestTenant, UserID: "user-1", AgentID: tt.agentID,
				ConversationID: "conversation-1", Query: "query", Scope: tt.scope,
			})
			if err != nil {
				t.Fatalf("BuildContext: %v", err)
			}
			for _, entity := range tt.wantEntities {
				if !strings.Contains(got, entity) {
					t.Errorf("context %q missing entity %q", got, entity)
				}
			}
			if len(tt.wantEntities) == 0 && strings.Contains(got, "Key Entities:") {
				t.Errorf("scope %q injected scoped entities: %q", tt.scope, got)
			}
			if err := pool.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMemoryInjectorBuildContextActiveSnapshotScopeMatrix(t *testing.T) {
	tests := []struct {
		name      string
		scope     string
		agentID   string
		wantCalls int
		wantText  bool
	}{
		{name: "agent_scope", scope: "agent", agentID: "agent-1", wantCalls: 1, wantText: true},
		{name: "user_scope", scope: "user", agentID: "agent-1", wantCalls: 1, wantText: true},
		{name: "invalid_scope", scope: "invalid", agentID: "agent-1"},
		{name: "empty_scope", agentID: "agent-1"},
		{name: "agent_scope_without_agent", scope: "agent"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &stubActiveSnapshotRepo{snapshot: &domain.ActiveSnapshot{WorkContext: []string{"private snapshot"}}}
			inj := &MemoryInjector{logger: zap.NewNop(), snapshotRepo: repo}
			got, err := inj.BuildContext(context.Background(), InjectionContext{
				TenantID: snapshotTestTenant, UserID: "user-1", AgentID: tt.agentID, Scope: tt.scope,
			})
			if err != nil {
				t.Fatal(err)
			}
			if repo.getCalls != tt.wantCalls {
				t.Errorf("snapshot repo calls = %d, want %d", repo.getCalls, tt.wantCalls)
			}
			if strings.Contains(got, "private snapshot") != tt.wantText {
				t.Errorf("snapshot context = %q, want text=%v", got, tt.wantText)
			}
		})
	}
}

func TestFactInjectionQueryFiltersAndRanksQuality(t *testing.T) {
	query := factInjectionQuery()
	for _, want := range []string{
		"status = 'active'",
		"similarity(content, $3) DESC",
		"confidence >= $4",
		"frecency_score DESC, confidence DESC, importance DESC",
		"LIMIT $5",
		"scope = 'user' AND $6 = true",
		"scope = 'agent' AND agent_id = $2 AND $7 = true",
	} {
		if !strings.Contains(query, want) {
			t.Fatalf("fact injection query missing %q", want)
		}
	}
}

func TestRenderMemoryContextPrioritizesSnapshotUnderSharedBudget(t *testing.T) {
	snapshot := &domain.ActiveSnapshot{WorkContext: []string{"release phase one"}, TopOfMind: []string{"tenant isolation"}}
	facts := []factRow{{content: strings.Repeat("fact", 100), category: "state"}}
	budget := 90
	got := renderMemoryContext(snapshot, "old summary", []string{"entity"}, facts, nil, budget)
	if !strings.Contains(got, "Active snapshot:") || !strings.Contains(got, "release phase one") {
		t.Fatalf("snapshot was not prioritized: %q", got)
	}
	if len([]rune(got)) > budget {
		t.Fatalf("shared budget exceeded: got %d want <= %d", len([]rune(got)), budget)
	}
}

func TestHistoryInjectionQueryIsScopedRelevantAndBounded(t *testing.T) {
	query := historyInjectionQuery()
	for _, want := range []string{"user_id = $1", "agent_id = $2", "similarity(summary, $3) DESC", "status = 'active'", "LIMIT $4", "scope = 'user' AND $5 = true", "scope = 'agent' AND agent_id = $2 AND $6 = true"} {
		if !strings.Contains(query, want) {
			t.Fatalf("history query missing %q", want)
		}
	}
}

func TestRenderMemoryContextPlacesHistoryAfterFactsUnderSharedBudget(t *testing.T) {
	snapshot := &domain.ActiveSnapshot{WorkContext: []string{"current work"}}
	facts := []factRow{{content: "stable fact", category: "state"}}
	history := []historyRow{{summary: strings.Repeat("earlier context ", 20), tier: domain.HistoryTierEarlier}}
	got := renderMemoryContext(snapshot, "conversation", nil, facts, history, 120)
	if len([]rune(got)) > 120 {
		t.Fatalf("shared budget exceeded: %d", len([]rune(got)))
	}
	if strings.Index(got, "Long-term facts:") > strings.Index(got, "History:") {
		t.Fatalf("facts must precede History: %q", got)
	}
}

func TestRenderMemoryContextReservesHistoryQuotaUnderSaturatedBudget(t *testing.T) {
	snapshot := &domain.ActiveSnapshot{WorkContext: []string{strings.Repeat("snapshot ", 100)}}
	facts := []factRow{{content: strings.Repeat("fact ", 100), category: "state"}}
	history := []historyRow{{summary: "history survives", tier: domain.HistoryTierEarlier}}
	const budget = 180

	got := renderMemoryContext(snapshot, "", nil, facts, history, budget)
	if !strings.Contains(got, "History:") || !strings.Contains(got, "history survives") {
		t.Fatalf("History quota was consumed by higher layers: %q", got)
	}
	if len([]rune(got)) > budget {
		t.Fatalf("shared budget exceeded: got %d want <= %d", len([]rune(got)), budget)
	}
}

func TestInjectorSnapshotReadFailureDegradesToEmpty(t *testing.T) {
	inj := &MemoryInjector{logger: zap.NewNop(), snapshotRepo: &stubActiveSnapshotRepo{err: errors.New("db unavailable")}}
	ic := InjectionContext{TenantID: snapshotTestTenant, UserID: "user", AgentID: "agent", Scope: "agent"}
	got := inj.loadActiveSnapshot(context.Background(), ic, domain.BuildScopeFilter(ic.TenantID, ic.UserID, ic.AgentID, ic.Scope))
	if got != nil {
		t.Fatalf("snapshot failure should degrade to nil: %#v", got)
	}
}

func TestFormatActiveSnapshotHonorsSectionAndBudget(t *testing.T) {
	snapshot := &domain.ActiveSnapshot{WorkContext: []string{"work"}, PersonalContext: []string{"personal"}, TopOfMind: []string{"focus"}, ExpiresAt: time.Now().Add(time.Hour)}
	got := formatActiveSnapshot(snapshot, 35)
	if len([]rune(got)) > 35 || !strings.Contains(got, "Work: work") {
		t.Fatalf("unexpected bounded snapshot: %q", got)
	}
}

func TestFormatFactLinesUsesCategoryAndBoundedBudget(t *testing.T) {
	facts := []factRow{{content: "uses Go", category: "skill"}, {content: "prefers dark mode", category: "preference"}}
	first := "- [skill] uses Go\n"
	lines := formatFactLines(facts, len(first))
	if len(lines) != 1 || lines[0] != first {
		t.Fatalf("unexpected bounded fact lines: %#v", lines)
	}
	if got := formatFactLines([]factRow{{content: "x"}}, constants.FactInjectionCharBudget); got[0] != "- [other] x\n" {
		t.Fatalf("blank category fallback missing: %#v", got)
	}
}
