// Package http_test replays recorded HTTP contract goldens to detect
// backward-incompatible changes during the DDD refactor.
package http_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/api"
	apihttp "github.com/byteBuilderX/stratum/api/http"
	"github.com/byteBuilderX/stratum/api/wiring"
	"github.com/byteBuilderX/stratum/config"
	agentapp "github.com/byteBuilderX/stratum/internal/agent/application"
	agentdomain "github.com/byteBuilderX/stratum/internal/agent/domain"
	agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
	auditapp "github.com/byteBuilderX/stratum/internal/audit/application"
	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	evalapp "github.com/byteBuilderX/stratum/internal/evaluation/application"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
	iamapp "github.com/byteBuilderX/stratum/internal/iam/application"
	iamdomain "github.com/byteBuilderX/stratum/internal/iam/domain"
	iamport "github.com/byteBuilderX/stratum/internal/iam/domain/port"
	iamtoken "github.com/byteBuilderX/stratum/internal/iam/infrastructure/token"
	llmapp "github.com/byteBuilderX/stratum/internal/llmgateway/application"
	llmdomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	llmport "github.com/byteBuilderX/stratum/internal/llmgateway/domain/port"
	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	platformapp "github.com/byteBuilderX/stratum/internal/platform/application"
	platformdomain "github.com/byteBuilderX/stratum/internal/platform/domain"
	workflowapp "github.com/byteBuilderX/stratum/internal/workflow/application"
	workflowdomain "github.com/byteBuilderX/stratum/internal/workflow/domain"
	workflowport "github.com/byteBuilderX/stratum/internal/workflow/domain/port"
	"github.com/byteBuilderX/stratum/pkg/observability"
)

type contractCase struct {
	Name       string            `json:"name"`
	Method     string            `json:"method"`
	Path       string            `json:"path"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       json.RawMessage   `json:"body,omitempty"`
	WantBody   json.RawMessage   `json:"want_body,omitempty"`
	WantBodyRE string            `json:"want_body_regex,omitempty"`
	WantStatus int               `json:"want_status"`
}

func TestContracts(t *testing.T) {
	cfg, err := config.Load()
	if err != nil {
		t.Skipf("config load failed: %v", err)
	}
	cfg.GitHubClientID = "contract-recorder"
	cfg.GitHubClientSecret = "contract-recorder"
	cfg.JWTPrivateKeyPEM = mustGeneratePEM(t)

	logger, _ := observability.NewLogger("test")
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	metrics := observability.NewPrometheusMetrics(logger)
	gateway := llmgateway.NewGateway(nil, nil, nil).WithLogger(logger)

	// ── Legacy router (auth, health, models catalogue, etc.) ──────────────
	router := api.SetupRouter(cfg, logger, gateway, nil, nil, nil, nil)

	// ── DDD router with full stub population ──────────────────────────────
	var idCounter atomic.Int64
	nextID := func() string { return fmt.Sprintf("contract-%d", idCounter.Add(1)) }

	contractProvRepo := contractProviderRepo{}
	contractModRepo := contractModelRepo{}
	contractDefRepo := contractDefRepo{}
	contractVerRepo := contractVersionRepo{}
	contractRunStore := contractRunStore{}
	contractCtrlRepo := contractControlRepo{}
	contractAgtExec := contractAgentExecutor{}
	contractAdminTR := contractAdminTenantRepo{}
	contractTenantR := contractTenantRepo{}
	contractInvR := contractInvitationRepo{}
	contractAuditRepo := contractAuditRepo{}

	dddRouter := apihttp.NewRouter(&wiring.Container{
		Config: cfg, Logger: logger,
		Platform: &wiring.Platform{
			JWTService: iamtoken.NewJWTService(key), Metrics: metrics,
			DashboardService: platformapp.NewDashboardService(contractDashboardRepo{}),
		},
		LLMGateway: &wiring.LLMGateway{
			ProviderService:  llmapp.NewProviderService(contractProvRepo, contractModRepo, contractProviderRuntime{}),
			ModelMgmtService: llmapp.NewModelMgmtService(contractModRepo),
		},
		Skill: &wiring.Skill{}, MCP: &wiring.MCP{}, Memory: &wiring.Memory{},
		Agent: func() *wiring.Agent {
			gate := agentapp.NewOperationGateService(
				contractOpPropRepo{}, contractOpUsageRepo{}, metrics,
			)
			svc := agentapp.NewAgentService(agentapp.AgentServiceDeps{
				Registry: agentapp.NewRegistry(contractAgentRepo{}, nil, logger),
				Logger:   logger,
				Metrics:  metrics,
			})
			svc.SetOperationGate(gate)
			return &wiring.Agent{
				ProposalService: agentapp.NewResourceChangeProposalService(
					contractProposalRepo{}, contractProposalAuthorizer{}, nil, nil, metrics,
				),
				OperationGateService: gate,
				OperationProposalSvc: agentapp.NewOperationProposalService(
					contractOpPropRepo{}, contractTenantRole{}, metrics,
				),
				Service: svc,
			}
		}(),
		Workflow: &wiring.Workflow{
			DefinitionService: workflowapp.NewDefinitionService(contractDefRepo, contractVerRepo, nextID),
			RunService:        workflowapp.NewRunService(contractVerRepo, contractRunStore, contractAgtExec, nextID),
			ControlService:    workflowapp.NewControlService(contractCtrlRepo, nextID),
		},
		Knowledge: &wiring.Knowledge{},
		Evaluation: &wiring.Evaluation{
			SuiteService: evalapp.NewSuiteService(nil), JobService: evalapp.NewJobService(nil, nil),
			QueryService:      evalapp.NewQueryService(contractQueryRepo{}),
			ExperimentService: evalapp.NewExperimentService(contractExperimentRepo{}),
			CandidateService:  evalapp.NewCandidateCommandService(contractCandidateRepo{}),
		},
		IAM: &wiring.IAM{
			AdminService:      iamapp.NewAdminService(contractAdminTR),
			TenantService:     iamapp.NewTenantService(contractTenantR, logger),
			InvitationService: iamapp.NewInvitationService(contractInvR),
		},
		Audit: &wiring.Audit{
			Recorder:     auditapp.NewAuditService(contractAuditRepo, observability.NoopMetrics{}, logger),
			QueryService: auditapp.NewAuditService(contractAuditRepo, observability.NoopMetrics{}, logger),
		},
	})

	jwtSvc := iamtoken.NewJWTService(key)

	// ── Route dispatch: paths handled by the DDD router ───────────────────
	dddPrefixes := []string{
		"/evaluations/", "/dashboard/", "/resource-change-proposals/",
		"/admin/providers", "/admin/models", "/admin/tenants",
		"/tenant/", "/workflows", "/workflow-runs", "/workflow-approvals",
		"/operation-proposals",
	}

	files, err := filepath.Glob("testdata/contracts/*.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Skip("no golden files: run `make record-contracts` first")
	}
	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			data, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			var cases []contractCase
			if err := json.Unmarshal(data, &cases); err != nil {
				t.Fatal(err)
			}
			for _, c := range cases {
				req := httptest.NewRequest(c.Method, c.Path, bytes.NewReader(c.Body))

				useDDD := strings.Contains(c.Path, "/self-modify")
				for _, prefix := range dddPrefixes {
					if strings.HasPrefix(c.Path, prefix) {
						useDDD = true
						break
					}
				}

				if useDDD {
					var claims iamport.TokenClaims
					switch {
					case strings.HasPrefix(c.Path, "/admin/tenants"):
						claims = iamport.TokenClaims{Sub: "contract-admin", GlobalRole: "global_admin"}
					default:
						claims = iamport.TokenClaims{Sub: "contract-admin", TenantID: "contract-tenant", Role: "admin"}
					}
					token, signErr := jwtSvc.Sign(claims, time.Hour)
					if signErr != nil {
						t.Fatal(signErr)
					}
					req.Header.Set("Authorization", "Bearer "+token)
				}

				for k, v := range c.Headers {
					req.Header.Set(k, v)
				}
				rec := httptest.NewRecorder()
				if useDDD {
					dddRouter.ServeHTTP(rec, req)
				} else {
					router.ServeHTTP(rec, req)
				}

				if rec.Code != c.WantStatus {
					t.Errorf("%s %s: got status %d, want %d", c.Method, c.Path, rec.Code, c.WantStatus)
				}
				if len(c.WantBodyRE) > 0 {
					if !regexp.MustCompile(c.WantBodyRE).Match(rec.Body.Bytes()) {
						t.Errorf("%s %s: body=%s does not match %s", c.Method, c.Path, rec.Body.String(), c.WantBodyRE)
					}
				} else if len(c.WantBody) > 0 && !jsonEquivalent(rec.Body.Bytes(), c.WantBody) {
					t.Errorf("%s %s: body=%s want=%s", c.Method, c.Path, rec.Body.String(), c.WantBody)
				}
			}
		})
	}
}

// ── Stub repositories ──────────────────────────────────────────────────────

var errStubNotFound = errors.New("stub: not found")

type contractProviderRepo struct{}

func (contractProviderRepo) Create(_ context.Context, _ string, _ *llmdomain.Provider) error {
	return nil
}
func (contractProviderRepo) Get(_ context.Context, _ string, _ string) (*llmdomain.Provider, error) {
	return &llmdomain.Provider{
		ID: "contract-provider", Name: "stub", Kind: llmdomain.ProviderOpenAICompat,
		BaseURL: "https://stub.example.com/v1", Enabled: true,
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}, nil
}
func (contractProviderRepo) List(_ context.Context, _ string) ([]llmdomain.Provider, error) {
	return nil, nil
}
func (contractProviderRepo) Update(_ context.Context, _ string, _ *llmdomain.Provider) error {
	return nil
}
func (contractProviderRepo) Delete(_ context.Context, _ string, _ string) error { return nil }

type contractModelRepo struct{}

func (contractModelRepo) Create(_ context.Context, _ string, _ *llmdomain.Model) error { return nil }
func (contractModelRepo) Get(_ context.Context, _ string, _ string) (*llmdomain.Model, error) {
	return nil, errStubNotFound
}
func (contractModelRepo) List(_ context.Context, _ string, _ llmport.ModelFilter) ([]llmdomain.Model, error) {
	return nil, nil
}
func (contractModelRepo) Update(_ context.Context, _ string, _ *llmdomain.Model) error { return nil }
func (contractModelRepo) UpsertDiscovered(_ context.Context, _ string, _ string, _ []llmdomain.Model) ([]llmdomain.Model, error) {
	return nil, nil
}
func (contractModelRepo) Delete(_ context.Context, _ string, _ string) error         { return nil }
func (contractModelRepo) Toggle(_ context.Context, _ string, _ string, _ bool) error { return nil }

type contractProviderRuntime struct{}

func (contractProviderRuntime) ListModels(_ context.Context, _ llmdomain.Provider) ([]llmport.DiscoveredModel, error) {
	return []llmport.DiscoveredModel{{Name: "mock-model-1"}, {Name: "mock-model-2"}}, nil
}
func (contractProviderRuntime) Health(_ context.Context, _ llmdomain.Provider) error { return nil }

// ── Workflow stubs ─────────────────────────────────────────────────────────

type contractDefRepo struct{}

func (contractDefRepo) CreateDefinition(_ context.Context, _ string, _ *workflowdomain.Definition) error {
	return nil
}
func (contractDefRepo) GetDefinition(_ context.Context, _ string, _ string) (*workflowdomain.Definition, error) {
	return &workflowdomain.Definition{
		ID: "contract-def", Name: "stub-workflow", Description: "contract stub",
		Revision: 1, Spec: workflowdomain.Spec{Nodes: []workflowdomain.Node{}, Edges: []workflowdomain.Edge{}},
		InputSchema: workflowdomain.InputSchema{Fields: []workflowdomain.InputField{}},
		CreatedAt:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt:   time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}, nil
}
func (contractDefRepo) UpdateDefinition(_ context.Context, _ string, _ *workflowdomain.Definition, _ int64) error {
	return nil
}
func (contractDefRepo) DeleteDefinition(_ context.Context, _ string, _ string) error { return nil }
func (contractDefRepo) ListDefinitions(_ context.Context, _ string, _ workflowport.DefinitionListQuery) ([]workflowdomain.Definition, int, error) {
	return nil, 0, nil
}

type contractVersionRepo struct{}

func (contractVersionRepo) CreateVersion(_ context.Context, _ string, _ *workflowdomain.Version) error {
	return nil
}
func (contractVersionRepo) GetVersion(_ context.Context, _ string, _ string) (*workflowdomain.Version, error) {
	return nil, workflowdomain.ErrNotFound
}
func (contractVersionRepo) NextVersionNumber(_ context.Context, _ string, _ string) (int64, error) {
	return 1, nil
}
func (contractVersionRepo) ListVersions(_ context.Context, _ string, _ string, _ workflowport.VersionListQuery) ([]workflowdomain.Version, int, error) {
	return nil, 0, nil
}

type contractRunStore struct{}

func (contractRunStore) FindRunByIdempotency(_ context.Context, _ string, _ string) (*workflowdomain.Run, error) {
	return nil, workflowdomain.ErrNotFound
}
func (contractRunStore) CreateRun(_ context.Context, _ string, _ *workflowdomain.Run) error {
	return nil
}
func (contractRunStore) GetRun(_ context.Context, _ string, _ string) (*workflowdomain.Run, error) {
	return nil, workflowdomain.ErrNotFound
}
func (contractRunStore) UpdateRun(_ context.Context, _ string, _ *workflowdomain.Run) error {
	return nil
}
func (contractRunStore) SaveAttempt(_ context.Context, _ string, _ workflowdomain.NodeAttempt) error {
	return nil
}
func (contractRunStore) ListAttempts(_ context.Context, _ string, _ string) ([]workflowdomain.NodeAttempt, error) {
	return nil, nil
}
func (contractRunStore) ListRuns(_ context.Context, _ string, _ workflowport.RunListQuery) ([]workflowdomain.Run, int, error) {
	return nil, 0, nil
}

type contractControlRepo struct{}

func (contractControlRepo) GetRun(_ context.Context, _ string, _ string) (*workflowdomain.Run, error) {
	return nil, workflowdomain.ErrNotFound
}
func (contractControlRepo) ControlRun(_ context.Context, _ string, _ string, _ int64, _ workflowdomain.RunStatus, _ string, _ workflowdomain.Event) error {
	return nil
}
func (contractControlRepo) ListApprovals(_ context.Context, _ string, _ string, _ bool) ([]workflowdomain.Approval, error) {
	return nil, nil
}
func (contractControlRepo) DecideApproval(_ context.Context, _ string, _ string, _ int64, _ string, _ workflowdomain.ApprovalDecision, _ string, _ string, _ workflowdomain.Event) error {
	return nil
}
func (contractControlRepo) ListEffectIntents(_ context.Context, _ string, _ string) ([]workflowdomain.EffectIntent, error) {
	return nil, nil
}
func (contractControlRepo) ResolveEffect(_ context.Context, _ string, _ string, _ int64, _ workflowdomain.ManualAction, _ string, _ string, _ workflowdomain.Event) error {
	return nil
}

type contractAgentExecutor struct{}

func (contractAgentExecutor) ExecuteAgent(_ context.Context, _ string, _ string, _ string) (string, string, error) {
	return "", "", errors.New("stub: agent execution unavailable")
}

// ── IAM stubs ──────────────────────────────────────────────────────────────

type contractAdminTenantRepo struct{}

func (contractAdminTenantRepo) Count(_ context.Context, _ iamdomain.TenantFilter) (int, error) {
	return 0, nil
}
func (contractAdminTenantRepo) List(_ context.Context, _ iamdomain.TenantFilter) ([]iamdomain.Tenant, error) {
	return nil, nil
}
func (contractAdminTenantRepo) Get(_ context.Context, _ string) (*iamdomain.Tenant, error) {
	return nil, errStubNotFound
}
func (contractAdminTenantRepo) Create(_ context.Context, _ iamdomain.Tenant) error { return nil }
func (contractAdminTenantRepo) UpdatePatch(_ context.Context, _ string, _ iamdomain.TenantPatch) error {
	return nil
}
func (contractAdminTenantRepo) HardDelete(_ context.Context, _ string) error { return nil }
func (contractAdminTenantRepo) ProvisionSchema(_ context.Context, _ string) error {
	return nil
}

type contractTenantRepo struct{}

func (contractTenantRepo) CountMembers(_ context.Context, _ string) (int, error) { return 0, nil }
func (contractTenantRepo) ListMembers(_ context.Context, _ string, _ int, _ int) ([]iamdomain.Member, error) {
	return nil, nil
}
func (contractTenantRepo) GetMemberRole(_ context.Context, _ string, _ string) (string, error) {
	return "member", nil
}
func (contractTenantRepo) UpdateMemberRole(_ context.Context, _ string, _ string, _ string) error {
	return nil
}
func (contractTenantRepo) DeleteMember(_ context.Context, _ string, _ string) error { return nil }
func (contractTenantRepo) GetTenantSettings(_ context.Context, _ string) (string, bool, []byte, error) {
	return "stub-tenant", false, []byte(`{}`), nil
}
func (contractTenantRepo) UpdateTenantName(_ context.Context, _ string, _ string) error { return nil }
func (contractTenantRepo) UpdateTenantSettings(_ context.Context, _ string, _ []byte) error {
	return nil
}
func (contractTenantRepo) ListUserTenants(_ context.Context, _ string) ([]iamdomain.UserTenantInfo, error) {
	return nil, nil
}

type contractInvitationRepo struct{}

func (contractInvitationRepo) Create(_ context.Context, _ iamdomain.TenantInvitation) error {
	return nil
}
func (contractInvitationRepo) ConsumeAndJoin(_ context.Context, _ iamdomain.InvitationJoinInput) (*iamdomain.InvitationJoinResult, error) {
	return nil, errStubNotFound
}
func (contractInvitationRepo) ConsumeAndJoinExisting(_ context.Context, _ iamdomain.ExistingInvitationJoinInput) (*iamdomain.InvitationJoinResult, error) {
	return nil, errStubNotFound
}

// ── Existing stubs ─────────────────────────────────────────────────────────

type contractDashboardRepo struct{}

func (contractDashboardRepo) Overview(context.Context, string) (platformdomain.DashboardOverview, error) {
	return platformdomain.DashboardOverview{}, nil
}

type contractAgentRepo struct{}

func (contractAgentRepo) Register(context.Context, *agentdomain.AgentConfig) error {
	return nil
}
func (contractAgentRepo) Get(context.Context, string) (*agentdomain.AgentConfig, bool, error) {
	return nil, false, nil
}
func (contractAgentRepo) GetSystemAssistant(context.Context) (*agentdomain.AgentConfig, bool, error) {
	return nil, false, nil
}
func (contractAgentRepo) GetAll(context.Context) ([]*agentdomain.AgentConfig, error) {
	return nil, nil
}
func (contractAgentRepo) Remove(context.Context, string) error { return nil }
func (contractAgentRepo) Update(context.Context, *agentdomain.AgentConfig) error {
	return nil
}
func (contractAgentRepo) UpdateSystemAssistantModel(
	context.Context, string, string, bool, int, int,
) (*agentdomain.AgentConfig, error) {
	return nil, nil
}
func (contractAgentRepo) UpdateSystemAssistantBindings(
	context.Context, []string, []string, []string,
) (*agentdomain.AgentConfig, error) {
	return nil, nil
}

// Operation gate stubs: self-modify always lands as a pending proposal, so
// the recorded response is the deterministic 202 pending_approval shape.
type contractOpPropRepo struct{}

func (contractOpPropRepo) Insert(context.Context, agentdomain.OperationProposal) error { return nil }
func (contractOpPropRepo) GetByID(context.Context, string, string) (*agentdomain.OperationProposal, error) {
	return nil, agentdomain.ErrOperationProposalNotFound
}
func (contractOpPropRepo) ListPending(context.Context, string) ([]agentdomain.OperationProposal, error) {
	return nil, nil
}
func (contractOpPropRepo) UpdateStatus(
	context.Context, string, string, agentdomain.OpProposalStatus, string, string,
) error {
	return nil
}
func (contractOpPropRepo) HasPending(context.Context, string, string) (bool, error) {
	return false, nil
}
func (contractOpPropRepo) ConsumeApproved(context.Context, string, string, string) (bool, error) {
	return false, nil
}

type contractOpUsageRepo struct{}

func (contractOpUsageRepo) AddUsage(
	context.Context, string, string, agentport.OperationType, time.Time, float64, int,
) error {
	return nil
}
func (contractOpUsageRepo) DailyUsage(
	context.Context, string, string, agentport.OperationType, time.Time,
) (agentport.DailyOperationUsage, error) {
	return agentport.DailyOperationUsage{}, nil
}

type contractTenantRole struct{}

func (contractTenantRole) ResolveTenantRole(context.Context, string, string) (string, error) {
	return "admin", nil
}

type contractProposalRepo struct{}

func (contractProposalRepo) Create(context.Context, agentdomain.ResourceChangeProposal, agentdomain.ProposalEvent) error {
	return nil
}
func (contractProposalRepo) Get(context.Context, string) (agentdomain.ResourceChangeProposal, error) {
	return agentdomain.ResourceChangeProposal{}, agentdomain.ErrProposalNotFound
}
func (contractProposalRepo) UpdateDraft(
	context.Context, agentdomain.ResourceChangeProposal, agentdomain.ProposalEvent,
) error {
	return nil
}
func (contractProposalRepo) Cancel(context.Context, string, string, time.Time) error  { return nil }
func (contractProposalRepo) Confirm(context.Context, string, string, time.Time) error { return nil }
func (contractProposalRepo) ClaimApplying(
	context.Context, string, string, time.Time,
) (agentdomain.ResourceChangeProposal, error) {
	return agentdomain.ResourceChangeProposal{}, agentdomain.ErrProposalNotFound
}
func (contractProposalRepo) Finish(
	context.Context, string, agentdomain.ProposalStatus, agentdomain.ApplyResult, agentdomain.ProposalEvent,
) error {
	return nil
}
func (contractProposalRepo) ListEvents(context.Context, string) ([]agentdomain.ProposalEvent, error) {
	return nil, agentdomain.ErrProposalNotFound
}

type contractProposalAuthorizer struct{}

func (contractProposalAuthorizer) AuthorizeProposal(
	context.Context, string, string, agentdomain.ResourceKind, agentdomain.ProposalOperation,
) error {
	return nil
}

func jsonEquivalent(got, want []byte) bool {
	var g, w any
	return json.Unmarshal(got, &g) == nil && json.Unmarshal(want, &w) == nil && reflect.DeepEqual(g, w)
}

type contractQueryRepo struct{}

func (contractQueryRepo) Overview(context.Context, string) (domain.CenterOverview, error) {
	return domain.CenterOverview{}, nil
}
func (contractQueryRepo) ListResources(context.Context, string, port.CenterFilter) (domain.ResourcePage, error) {
	return domain.ResourcePage{Items: []domain.ResourceSummary{}}, nil
}
func (contractQueryRepo) ListSuites(context.Context, string, port.CenterFilter) (domain.SuitePage, error) {
	return domain.SuitePage{Items: []domain.SuiteSummary{}}, nil
}
func (contractQueryRepo) ListRuns(context.Context, string, port.CenterFilter) (domain.RunPage, error) {
	return domain.RunPage{Items: []domain.RunSummary{}}, nil
}
func (contractQueryRepo) ListCandidates(context.Context, string, port.CenterFilter) (domain.CandidatePage, error) {
	return domain.CandidatePage{Items: []domain.CandidateSummary{}}, nil
}
func (contractQueryRepo) ListExperiments(context.Context, string, port.CenterFilter) (domain.ExperimentPage, error) {
	return domain.ExperimentPage{Items: []domain.ExperimentSummary{}}, nil
}
func (contractQueryRepo) Timeline(context.Context, string, port.CenterFilter) (domain.TimelinePage, error) {
	return domain.TimelinePage{Items: []domain.TimelineEvent{}}, nil
}

type contractExperimentRepo struct{}

func (contractExperimentRepo) ValidatePrerequisites(context.Context, string, domain.ResourceRef,
	domain.ResourceRef, string) error {
	return nil
}
func (contractExperimentRepo) Create(context.Context, string, domain.Experiment, domain.Deployment) error {
	return nil
}
func (contractExperimentRepo) Get(context.Context, string, string) (domain.Experiment, bool, error) {
	return domain.Experiment{}, false, nil
}
func (contractExperimentRepo) SaveDecision(context.Context, string, domain.Experiment, domain.Decision, domain.StageMetrics, string, string) (domain.Experiment, domain.Decision, error) {
	return domain.Experiment{}, domain.DecisionHold, nil
}
func (contractExperimentRepo) ApplyCommand(context.Context, string, string, domain.ExperimentCommandAction, domain.ExperimentCommand) (domain.Experiment, error) {
	return domain.Experiment{}, domain.ErrExperimentStateConflict
}
func (contractExperimentRepo) ResolveDeployment(context.Context, string, string, string) (domain.Deployment, bool, error) {
	return domain.Deployment{}, false, nil
}
func (contractExperimentRepo) HasRunningExperiment(context.Context, string, string, string) (bool, error) {
	return false, nil
}
func (contractExperimentRepo) ListPendingExperiments(context.Context, string, string, string) ([]domain.Experiment, error) {
	return nil, nil
}
func (contractExperimentRepo) ListRunningExperiments(context.Context, string) ([]domain.Experiment, error) {
	return nil, nil
}

type contractCandidateRepo struct{}

func (contractCandidateRepo) Reject(context.Context, string, string, domain.CandidateCommand) (domain.CandidateSummary, error) {
	return domain.CandidateSummary{}, domain.ErrCandidateCommandConflict
}

// ── Audit stub ─────────────────────────────────────────────────────────────

type contractAuditRepo struct{}

func (contractAuditRepo) InsertBatch(_ context.Context, _ []auditdomain.AuditEvent) error {
	return nil
}
func (contractAuditRepo) Query(_ context.Context, _ auditdomain.AuditFilter) ([]auditdomain.AuditEvent, error) {
	return nil, nil
}
func (contractAuditRepo) GetByID(_ context.Context, _ string) (*auditdomain.AuditEvent, error) {
	return nil, nil
}
func (contractAuditRepo) DeleteOlderThan(_ context.Context, _ time.Time) error { return nil }

func mustGeneratePEM(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	der := x509.MarshalPKCS1PrivateKey(key)
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: der}
	return string(pem.EncodeToMemory(block))
}
