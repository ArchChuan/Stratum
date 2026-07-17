package pipeline

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"go.uber.org/zap"
)

func TestFactInjectionQueryFiltersAndRanksQuality(t *testing.T) {
	query := factInjectionQuery()
	for _, want := range []string{
		"status = 'active'",
		"similarity(content, $3) DESC",
		"confidence >= $4",
		"frecency_score DESC, confidence DESC, importance DESC",
		"LIMIT $5",
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
	for _, want := range []string{"user_id = $1", "agent_id = $2", "similarity(summary, $3) DESC", "status = 'active'", "LIMIT $4"} {
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
	got := inj.loadActiveSnapshot(context.Background(), InjectionContext{TenantID: snapshotTestTenant, UserID: "user", AgentID: "agent"})
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
