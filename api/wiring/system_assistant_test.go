package wiring

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
	auditport "github.com/byteBuilderX/stratum/internal/audit/domain/port"
	iamdomain "github.com/byteBuilderX/stratum/internal/iam/domain"
	llmdomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	skillapp "github.com/byteBuilderX/stratum/internal/skill/application"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type diagnosticRoleStub struct {
	role string
	err  error
}

type diagnosticTraceProviderStub struct {
	opts     domain.ListOptions
	tenantID string
}

func (s *diagnosticTraceProviderStub) ListExecutions(_ context.Context, tenantID string, opts domain.ListOptions) ([]domain.ExecutionRecord, int64, error) {
	s.tenantID, s.opts = tenantID, opts
	if opts.UserID == "user-1" {
		return []domain.ExecutionRecord{{ID: "mine", UserID: "user-1"}}, 1, nil
	}
	rows := make([]domain.ExecutionRecord, 20)
	for i := range rows {
		rows[i] = domain.ExecutionRecord{ID: "other", UserID: "user-2"}
	}
	return rows, 20, nil
}
func (*diagnosticTraceProviderStub) ToolObservations(context.Context, string, string) ([]domain.ToolObservation, error) {
	return nil, nil
}
func (*diagnosticTraceProviderStub) TraceEvents(context.Context, string, string) ([]domain.AgentTraceEvent, error) {
	return nil, nil
}
func (*diagnosticTraceProviderStub) Resolve(context.Context, string, string) (domain.TraceEvidence, error) {
	return domain.TraceEvidence{}, nil
}
func (*diagnosticTraceProviderStub) ResolveBatch(context.Context, string, []string) (map[string]domain.TraceEvidence, error) {
	return nil, nil
}

func TestAgentDiagnosticCollectorFiltersUpstreamBeforeLimit(t *testing.T) {
	provider := &diagnosticTraceProviderStub{}
	facts, _, err := agentDiagnosticCollector(provider)(context.Background(), domain.DiagnosticRequest{
		TenantID: "tenant-1", UserID: "user-1", Scope: domain.DiagnosticScopeSelf,
	})
	require.NoError(t, err)
	require.Equal(t, "tenant-1", provider.tenantID)
	require.Equal(t, "user-1", provider.opts.UserID)
	require.Len(t, facts, 1)
	require.Equal(t, "mine", facts[0].ObjectID)
}

type failureAuditQueryStub struct {
	lastFilter auditport.ResourceChangeAuditFilter
	rows       map[string][]auditport.ResourceChangeAuditRow
}

func (f *failureAuditQueryStub) List(_ context.Context, _ string, filter auditport.ResourceChangeAuditFilter) ([]auditport.ResourceChangeAuditRow, int, error) {
	f.lastFilter = filter
	return f.rows[filter.ResourceKind], len(f.rows[filter.ResourceKind]), nil
}

func (*failureAuditQueryStub) GetByID(context.Context, string, string) (*auditport.ResourceChangeAuditRow, error) {
	return nil, nil
}

func failureRow(id, op, actor, code string) auditport.ResourceChangeAuditRow {
	after, _ := json.Marshal(map[string]string{"status": "failed", "error_code": code})
	return auditport.ResourceChangeAuditRow{
		ID: id, ResourceKind: "mcp", ResourceID: id, Operation: op,
		ActorID: actor, CreatedAt: time.Now().UTC(), After: after,
	}
}

func TestFailureOperationParsing(t *testing.T) {
	op, code := failureOperation(failureRow("srv-1", "connect_failed", "u-1", "transport"))
	require.Equal(t, "connect", op)
	require.Equal(t, "transport", code)

	op, code = failureOperation(failureRow("srv-2", "create", "u-1", "transport"))
	require.Empty(t, op)
	require.Empty(t, code)
}

func TestCollectResourceFailureFactsSelfScopeFiltersByActor(t *testing.T) {
	query := &failureAuditQueryStub{rows: map[string][]auditport.ResourceChangeAuditRow{
		"mcp": {failureRow("srv-1", "connect_failed", "u-1", "transport")},
	}}
	adapter := newSystemAssistantDiagnosticAdapter(diagnosticRoleStub{role: "member"}, nil)
	adapter.SetFailureAuditQuery(query)

	evidence := domain.DiagnosticEvidence{}
	adapter.collectResourceFailureFacts(context.Background(), domain.DiagnosticRequest{
		TenantID: "t-1", UserID: "u-1", Scope: domain.DiagnosticScopeSelf,
		Areas: []domain.DiagnosticArea{domain.DiagnosticAreaMCP},
	}, &evidence)

	require.Equal(t, "u-1", query.lastFilter.ActorID)
	require.Equal(t, "mcp", query.lastFilter.ResourceKind)
	require.Len(t, evidence.Facts, 1)
	require.Equal(t, "failed_connect=transport", evidence.Facts[0].Statement)
	require.Equal(t, "srv-1", evidence.Facts[0].ObjectID)
}

func TestCollectResourceFailureFactsTenantScopeNoActorFilter(t *testing.T) {
	query := &failureAuditQueryStub{rows: map[string][]auditport.ResourceChangeAuditRow{
		"workflow": {
			failureRow("wf-1", "publish_failed", "u-1", "unknown"),
			failureRow("wf-2", "create", "u-2", "unknown"), // 非失败操作应被跳过
		},
	}}
	adapter := newSystemAssistantDiagnosticAdapter(diagnosticRoleStub{role: "admin"}, nil)
	adapter.SetFailureAuditQuery(query)

	evidence := domain.DiagnosticEvidence{}
	adapter.collectResourceFailureFacts(context.Background(), domain.DiagnosticRequest{
		TenantID: "t-1", UserID: "admin-1", Scope: domain.DiagnosticScopeTenant,
		Areas: []domain.DiagnosticArea{domain.DiagnosticAreaWorkflow},
	}, &evidence)

	require.Empty(t, query.lastFilter.ActorID)
	require.Len(t, evidence.Facts, 1)
	require.Equal(t, "failed_publish=unknown", evidence.Facts[0].Statement)
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

type diagnosticMemberBindingsStub struct {
	bindings memberResourceBindings
	err      error
}

func (s diagnosticMemberBindingsStub) ResolveMemberBindings(context.Context, domain.DiagnosticRequest) (memberResourceBindings, error) {
	return s.bindings, s.err
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

func TestSkillDiagnosticCollectorMemberReceivesOnlyPublicStatusProjection(t *testing.T) {
	skills := &diagnosticSkillServiceStub{products: []skillapp.SkillProduct{
		{ID: "skill-bound", Name: "secret-name", Description: "secret-description", Status: "published", ActiveRevisionID: "rev-1"},
		{ID: "skill-draft", Status: "draft", DraftRevisionID: "rev-draft"},
		{ID: "skill-unbound", Status: "published", ActiveRevisionID: "rev-other"},
	}}
	bindings := diagnosticMemberBindingsStub{bindings: memberResourceBindings{SkillIDs: map[string]struct{}{"skill-bound": {}, "skill-draft": {}}}}
	facts, _, err := skillDiagnosticCollector(skills, &diagnosticSkillEvaluationStub{}, bindings)(context.Background(), domain.DiagnosticRequest{
		TenantID: "tenant-1", UserID: "member-1", Scope: domain.DiagnosticScopeSelf,
	})
	require.NoError(t, err)
	raw := fmt.Sprintf("%v", facts)
	require.Contains(t, raw, "skill_status=published")
	require.NotContains(t, raw, "secret-name")
	require.NotContains(t, raw, "secret-description")
	require.NotContains(t, raw, "rev-1")
	require.NotContains(t, raw, "evaluation")
	require.NotContains(t, raw, "skill-draft")
	require.NotContains(t, raw, "skill-unbound")
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
	require.Len(t, got.AreaResults, 2)
	for _, result := range got.AreaResults {
		require.Equal(t, "gap", result.Outcome)
	}
}

func TestSystemAssistantDiagnosticRecordsIndependentAreaOutcomes(t *testing.T) {
	adapter := newSystemAssistantDiagnosticAdapter(diagnosticRoleStub{role: "admin"}, map[domain.DiagnosticArea]diagnosticAreaCollector{
		domain.DiagnosticAreaAgent: func(context.Context, domain.DiagnosticRequest) ([]domain.DiagnosticFact, []domain.EvidenceGap, error) {
			time.Sleep(time.Millisecond)
			return []domain.DiagnosticFact{{Area: domain.DiagnosticAreaAgent, ObjectID: "ok"}}, nil, nil
		},
		domain.DiagnosticAreaMCP: func(context.Context, domain.DiagnosticRequest) ([]domain.DiagnosticFact, []domain.EvidenceGap, error) {
			time.Sleep(5 * time.Millisecond)
			return nil, []domain.EvidenceGap{{Area: domain.DiagnosticAreaMCP, Code: domain.DiagnosticGapUnavailable}}, nil
		},
	})
	got, err := adapter.Collect(context.Background(), domain.DiagnosticRequest{TenantID: "tenant-1", UserID: "admin-1", Areas: []domain.DiagnosticArea{domain.DiagnosticAreaAgent, domain.DiagnosticAreaMCP}})
	require.NoError(t, err)
	require.Equal(t, domain.DiagnosticAreaAgent, got.AreaResults[0].Area)
	require.Equal(t, "success", got.AreaResults[0].Outcome)
	require.Equal(t, domain.DiagnosticAreaMCP, got.AreaResults[1].Area)
	require.Equal(t, "gap", got.AreaResults[1].Outcome)
	require.Less(t, got.AreaResults[0].DurationMs, got.AreaResults[1].DurationMs)
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

func TestModelDiagnosticCollectorReportsCatalogStatistics(t *testing.T) {
	resolver := &tenantCapabilityResolver{registry: newResolverRegistry([]llmdomain.Model{
		{ID: "m-1", ProviderID: "p-1", Name: "chat-a", Enabled: true, Capabilities: []llmdomain.ModelCapability{llmdomain.CapChat}},
		{ID: "m-2", ProviderID: "p-1", Name: "chat-b", Enabled: true, Capabilities: []llmdomain.ModelCapability{llmdomain.CapChat}},
		{ID: "m-3", ProviderID: "p-1", Name: "retired", Enabled: false, Capabilities: []llmdomain.ModelCapability{llmdomain.CapChat}},
		{ID: "m-4", ProviderID: "p-1", Name: "embed", Enabled: true, Capabilities: []llmdomain.ModelCapability{llmdomain.CapEmbedding}},
	}, map[string]*llmdomain.Provider{
		"p-1": {ID: "p-1", Kind: llmdomain.ProviderOpenAICompat, Enabled: true},
	}), logger: zap.NewNop()}
	facts, gaps, err := modelDiagnosticCollector(resolver)(context.Background(), domain.DiagnosticRequest{
		TenantID: "tenant-1", UserID: "admin-1", Scope: domain.DiagnosticScopeTenant,
	})
	require.NoError(t, err)
	require.Empty(t, gaps)
	require.Equal(t, "catalog", facts[0].ObjectID)
	require.ElementsMatch(t, []string{
		"catalog_total=4 enabled=3 disabled=1 chat=3 embedding=1",
	}, diagnosticStatements(facts))
}

func TestModelDiagnosticCollectorPropagatesUnavailabilityAsSafeGap(t *testing.T) {
	resolver := &tenantCapabilityResolver{registry: llmgateway.NewModelRegistry(
		&resolverModelRepo{err: errors.New("raw catalog response with bearer secret")},
		&resolverProviderRepo{}, nil, nil, time.Minute,
	), logger: zap.NewNop()}
	facts, gaps, err := modelDiagnosticCollector(resolver)(context.Background(), domain.DiagnosticRequest{
		TenantID: "tenant-1", UserID: "admin-1", Scope: domain.DiagnosticScopeTenant,
	})
	require.NoError(t, err)
	require.Empty(t, facts)
	require.Equal(t, []domain.EvidenceGap{{Area: domain.DiagnosticAreaModel,
		Source: "managed_model_configuration", Code: domain.DiagnosticGapUnavailable}}, gaps)
	require.NotContains(t, gaps[0].Code, "raw catalog")
}

func TestSystemAssistantDiagnosticDispatchesDuplicateAreaOnce(t *testing.T) {
	var calls atomic.Int32
	adapter := newSystemAssistantDiagnosticAdapter(diagnosticRoleStub{role: "owner"}, map[domain.DiagnosticArea]diagnosticAreaCollector{
		domain.DiagnosticAreaAgent: func(context.Context, domain.DiagnosticRequest) ([]domain.DiagnosticFact, []domain.EvidenceGap, error) {
			calls.Add(1)
			return []domain.DiagnosticFact{{Area: domain.DiagnosticAreaAgent, ObjectID: "one"}}, nil, nil
		},
	})
	areas := make([]domain.DiagnosticArea, 100)
	for i := range areas {
		areas[i] = domain.DiagnosticAreaAgent
	}
	got, err := adapter.Collect(context.Background(), domain.DiagnosticRequest{TenantID: "tenant-1", UserID: "owner", Areas: areas})
	require.NoError(t, err)
	require.Equal(t, int32(1), calls.Load())
	require.Len(t, got.Facts, 1)
}

type fakeMemberRoleService struct {
	role string
	err  error
}

func (f fakeMemberRoleService) GetMemberRole(context.Context, string, string) (string, error) {
	return f.role, f.err
}

type fakeGlobalRoleReader struct {
	role string
	err  error
}

func (f fakeGlobalRoleReader) GetGlobalRole(context.Context, string) (string, error) {
	return f.role, f.err
}

func TestTenantRoleAdapter_PlatformAdminNonMemberElevated(t *testing.T) {
	a := tenantRoleAdapter{
		service: fakeMemberRoleService{err: iamdomain.ErrMemberNotFound},
		global:  fakeGlobalRoleReader{role: string(iamdomain.GlobalRoleSystemAdmin)},
	}
	got, err := a.ResolveTenantRole(context.Background(), "t1", "u1")
	if err != nil {
		t.Fatalf("ResolveTenantRole: %v", err)
	}
	if got != "admin" {
		t.Fatalf("ResolveTenantRole = %q, want admin", got)
	}
}

func TestTenantRoleAdapter_MemberPlatformAdminElevated(t *testing.T) {
	a := tenantRoleAdapter{
		service: fakeMemberRoleService{role: "member"},
		global:  fakeGlobalRoleReader{role: string(iamdomain.GlobalRoleSystemAdmin)},
	}
	got, err := a.ResolveTenantRole(context.Background(), "t1", "u1")
	if err != nil {
		t.Fatalf("ResolveTenantRole: %v", err)
	}
	if got != "admin" {
		t.Fatalf("ResolveTenantRole = %q, want admin", got)
	}
}

func TestTenantRoleAdapter_OwnerPlatformAdminKept(t *testing.T) {
	a := tenantRoleAdapter{
		service: fakeMemberRoleService{role: "owner"},
		global:  fakeGlobalRoleReader{role: string(iamdomain.GlobalRoleSystemAdmin)},
	}
	got, err := a.ResolveTenantRole(context.Background(), "t1", "u1")
	if err != nil {
		t.Fatalf("ResolveTenantRole: %v", err)
	}
	if got != "owner" {
		t.Fatalf("ResolveTenantRole = %q, want owner", got)
	}
}

func TestTenantRoleAdapter_OrdinaryMemberUnchanged(t *testing.T) {
	a := tenantRoleAdapter{
		service: fakeMemberRoleService{role: "member"},
		global:  fakeGlobalRoleReader{role: string(iamdomain.GlobalRoleUser)},
	}
	got, err := a.ResolveTenantRole(context.Background(), "t1", "u1")
	if err != nil {
		t.Fatalf("ResolveTenantRole: %v", err)
	}
	if got != "member" {
		t.Fatalf("ResolveTenantRole = %q, want member", got)
	}
}

func TestTenantRoleAdapter_GlobalRoleLookupFailureFailsClosed(t *testing.T) {
	a := tenantRoleAdapter{
		service: fakeMemberRoleService{role: "member"},
		global:  fakeGlobalRoleReader{err: errors.New("db down")},
	}
	if _, err := a.ResolveTenantRole(context.Background(), "t1", "u1"); err == nil {
		t.Fatal("expected error (fail closed) when global role lookup fails")
	}
}
