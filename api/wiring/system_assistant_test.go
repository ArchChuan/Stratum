package wiring

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	skillapp "github.com/byteBuilderX/stratum/internal/skill/application"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/stretchr/testify/require"
)

type diagnosticRoleStub struct {
	role string
	err  error
}

func (s diagnosticRoleStub) ResolveTenantRole(context.Context, string, string) (string, error) {
	return s.role, s.err
}

func TestSystemAssistantDiagnosticSelfScopeFiltersExecutions(t *testing.T) {
	adapter := newSystemAssistantDiagnosticAdapter(diagnosticRoleStub{role: "member"}, map[domain.DiagnosticArea]diagnosticAreaCollector{
		domain.DiagnosticAreaAgent: func(_ context.Context, _ domain.DiagnosticRequest) ([]domain.DiagnosticFact, []domain.EvidenceGap, error) {
			facts := []domain.DiagnosticFact{
				{Area: domain.DiagnosticAreaAgent, ObjectID: "exec-mine", SubjectUserID: "user-1", Statement: "success"},
				{Area: domain.DiagnosticAreaAgent, ObjectID: "exec-other", SubjectUserID: "user-2", Statement: "success"},
				{Area: domain.DiagnosticAreaAgent, ObjectID: "exec-unattributed", Statement: "success"},
			}
			return facts, nil, nil
		},
	})
	got, err := adapter.Collect(context.Background(), domain.DiagnosticRequest{TenantID: "tenant-1", UserID: "user-1", Scope: domain.DiagnosticScopeTenant, Areas: []domain.DiagnosticArea{domain.DiagnosticAreaAgent}})
	require.NoError(t, err)
	require.Len(t, got.Facts, 1)
	require.Equal(t, "exec-mine", got.Facts[0].ObjectID)
}

type diagnosticSkillServiceStub struct {
	products []skillapp.SkillProduct
	tenantID string
}

func (s *diagnosticSkillServiceStub) ListSkills(ctx context.Context) ([]skillapp.SkillProduct, error) {
	tc, _ := postgres.FromContext(ctx)
	s.tenantID = tc.TenantID
	return s.products, nil
}

type diagnosticSkillEvaluationStub struct {
	status   skillEvaluationStatus
	err      error
	tenantID string
}

func (s *diagnosticSkillEvaluationStub) ResolveSkillEvaluation(_ context.Context, tenantID, _ string) (skillEvaluationStatus, error) {
	s.tenantID = tenantID
	return s.status, s.err
}

func TestSkillDiagnosticCollectorIncludesProductRevisionAndEvaluationStatus(t *testing.T) {
	skills := &diagnosticSkillServiceStub{
		products: []skillapp.SkillProduct{{ID: "skill-1", Status: "published", ActiveRevisionID: "rev-active", DraftRevisionID: "rev-draft"}},
	}
	evaluations := &diagnosticSkillEvaluationStub{status: skillEvaluationStatus{ExperimentID: "experiment-1", Status: "running"}}
	facts, gaps, err := skillDiagnosticCollector(skills, evaluations)(context.Background(), domain.DiagnosticRequest{
		TenantID: "tenant-1", UserID: "admin-1", Scope: domain.DiagnosticScopeTenant,
	})
	require.NoError(t, err)
	require.Empty(t, gaps)
	require.Equal(t, "tenant-1", skills.tenantID)
	require.Equal(t, "tenant-1", evaluations.tenantID)
	require.ElementsMatch(t, []string{
		"skill_status=published", "revision_status=active", "revision_status=draft", "evaluation_status=running",
	}, diagnosticStatements(facts))
}

func TestSkillDiagnosticCollectorKeepsSkillFactsWhenEvaluationUnavailable(t *testing.T) {
	skills := &diagnosticSkillServiceStub{products: []skillapp.SkillProduct{{ID: "skill-1", Status: "draft", DraftRevisionID: "rev-draft"}}}
	evaluations := &diagnosticSkillEvaluationStub{err: errors.New("raw evaluation response with bearer secret")}
	facts, gaps, err := skillDiagnosticCollector(skills, evaluations)(context.Background(), domain.DiagnosticRequest{
		TenantID: "tenant-1", UserID: "owner-1", Scope: domain.DiagnosticScopeTenant,
	})
	require.NoError(t, err)
	require.NotEmpty(t, facts)
	require.Equal(t, []domain.EvidenceGap{{Area: domain.DiagnosticAreaSkill, Code: domain.DiagnosticGapUnavailable}}, gaps)
	require.NotContains(t, gaps[0].Code, "raw evaluation")
}

func diagnosticStatements(facts []domain.DiagnosticFact) []string {
	out := make([]string, len(facts))
	for i := range facts {
		out[i] = facts[i].Statement
	}
	return out
}

func TestSystemAssistantDiagnosticTenantAndRoleIsolation(t *testing.T) {
	var called atomic.Bool
	denied := newSystemAssistantDiagnosticAdapter(diagnosticRoleStub{err: errors.New("membership backend raw")}, map[domain.DiagnosticArea]diagnosticAreaCollector{
		domain.DiagnosticAreaAgent: func(context.Context, domain.DiagnosticRequest) ([]domain.DiagnosticFact, []domain.EvidenceGap, error) {
			called.Store(true)
			return nil, nil, nil
		},
	})
	_, err := denied.Collect(context.Background(), domain.DiagnosticRequest{TenantID: "tenant-1", UserID: "user-1", Areas: []domain.DiagnosticArea{domain.DiagnosticAreaAgent}})
	require.ErrorIs(t, err, domain.ErrDiagnosticForbidden)
	require.False(t, called.Load())

	allowed := newSystemAssistantDiagnosticAdapter(diagnosticRoleStub{role: "owner"}, map[domain.DiagnosticArea]diagnosticAreaCollector{
		domain.DiagnosticAreaAgent: func(_ context.Context, req domain.DiagnosticRequest) ([]domain.DiagnosticFact, []domain.EvidenceGap, error) {
			require.Equal(t, "tenant-1", req.TenantID)
			return []domain.DiagnosticFact{{Area: domain.DiagnosticAreaAgent, ObjectID: req.TenantID, Statement: "isolated"}}, nil, nil
		},
	})
	got, err := allowed.Collect(context.Background(), domain.DiagnosticRequest{TenantID: "tenant-1", UserID: "owner-1", Areas: []domain.DiagnosticArea{domain.DiagnosticAreaAgent}})
	require.NoError(t, err)
	require.Equal(t, "tenant-1", got.Facts[0].ObjectID)
}

func TestSystemAssistantDiagnosticUsesSafeAreaGaps(t *testing.T) {
	adapter := newSystemAssistantDiagnosticAdapter(diagnosticRoleStub{role: "admin"}, map[domain.DiagnosticArea]diagnosticAreaCollector{
		domain.DiagnosticAreaMCP: func(context.Context, domain.DiagnosticRequest) ([]domain.DiagnosticFact, []domain.EvidenceGap, error) {
			return nil, nil, errors.New("Authorization: Bearer raw-mcp-secret")
		},
		domain.DiagnosticAreaKnowledge: func(context.Context, domain.DiagnosticRequest) ([]domain.DiagnosticFact, []domain.EvidenceGap, error) {
			return nil, nil, errors.New("raw knowledge upstream response")
		},
	})
	got, err := adapter.Collect(context.Background(), domain.DiagnosticRequest{TenantID: "tenant-1", UserID: "admin-1", Areas: []domain.DiagnosticArea{domain.DiagnosticAreaMCP, domain.DiagnosticAreaKnowledge}})
	require.NoError(t, err)
	require.Len(t, got.Gaps, 2)
	for _, gap := range got.Gaps {
		require.Equal(t, domain.DiagnosticGapUnavailable, gap.Code)
		require.NotContains(t, gap.Code, "raw")
	}
}

func TestSystemAssistantDiagnosticBoundsConcurrencyAndWaits(t *testing.T) {
	var active, maximum, finished atomic.Int32
	release := make(chan struct{})
	started := make(chan struct{}, diagnosticCollectorConcurrency)
	collectors := make(map[domain.DiagnosticArea]diagnosticAreaCollector)
	areas := []domain.DiagnosticArea{domain.DiagnosticAreaAgent, domain.DiagnosticAreaSkill, domain.DiagnosticAreaMCP, domain.DiagnosticAreaKnowledge, domain.DiagnosticAreaModel}
	for _, area := range areas {
		collectors[area] = func(ctx context.Context, _ domain.DiagnosticRequest) ([]domain.DiagnosticFact, []domain.EvidenceGap, error) {
			n := active.Add(1)
			for n > maximum.Load() && !maximum.CompareAndSwap(maximum.Load(), n) {
			}
			started <- struct{}{}
			select {
			case <-release:
			case <-ctx.Done():
			}
			active.Add(-1)
			finished.Add(1)
			return nil, nil, ctx.Err()
		}
	}
	adapter := newSystemAssistantDiagnosticAdapter(diagnosticRoleStub{role: "owner"}, collectors)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_, _ = adapter.Collect(ctx, domain.DiagnosticRequest{TenantID: "tenant-1", UserID: "owner", Areas: areas})
		close(done)
	}()
	for i := 0; i < diagnosticCollectorConcurrency; i++ {
		<-started
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("collect did not wait for goroutines")
	}
	require.LessOrEqual(t, maximum.Load(), int32(diagnosticCollectorConcurrency))
	require.Equal(t, int32(len(areas)), finished.Load())
	close(release)
}
