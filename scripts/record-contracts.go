//go:build contracts

// Package main records HTTP contract golden files by replaying canonical
// requests against the current SetupRouter and writing JSON snapshots.
package main

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
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/byteBuilderX/stratum/api"
	apihttp "github.com/byteBuilderX/stratum/api/http"
	"github.com/byteBuilderX/stratum/api/wiring"
	"github.com/byteBuilderX/stratum/config"
	agentapp "github.com/byteBuilderX/stratum/internal/agent/application"
	agentdomain "github.com/byteBuilderX/stratum/internal/agent/domain"
	agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
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
	"go.uber.org/zap"
)

// Case represents a single recorded request/response snapshot.
type Case struct {
	Name       string            `json:"name"`
	Method     string            `json:"method"`
	Path       string            `json:"path"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       json.RawMessage   `json:"body,omitempty"`
	WantStatus int               `json:"want_status"`
	WantBody   json.RawMessage   `json:"want_body,omitempty"`
	WantBodyRE string            `json:"want_body_regex,omitempty"`
}

var errStubNotFound = errors.New("stub: not found")

func buildDDDContainer(cfg *config.Config, key *rsa.PrivateKey, logger *zap.Logger, metrics *observability.PrometheusMetrics) *wiring.Container {
	idCounter := 0
	nextID := func() string { idCounter++; return fmt.Sprintf("contract-%d", idCounter) }
	return &wiring.Container{
		Config: cfg, Logger: logger,
		Platform: &wiring.Platform{
			JWTService: iamtoken.NewJWTService(key), Metrics: metrics,
			DashboardService: platformapp.NewDashboardService(contractDashboardRepo{}),
		},
		LLMGateway: &wiring.LLMGateway{
			ProviderService:  llmapp.NewProviderService(contractProvRepo{}, contractModRepo{}, contractProvRuntime{}),
			ModelMgmtService: llmapp.NewModelMgmtService(contractModRepo{}),
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
					contractPropRepo{}, contractPropAuthorizer{}, nil, nil, metrics,
				),
				OperationGateService: gate,
				OperationProposalSvc: agentapp.NewOperationProposalService(
					contractOpPropRepo{}, contractTenantRole{}, metrics,
				),
				Service: svc,
			}
		}(),
		Workflow: &wiring.Workflow{
			DefinitionService: workflowapp.NewDefinitionService(contractDefRepo{}, contractVerRepo{}, nextID),
			RunService:        workflowapp.NewRunService(contractVerRepo{}, contractRunStore{}, contractAgtExec{}, nextID),
			ControlService:    workflowapp.NewControlService(contractCtrlRepo{}, nextID),
		},
		Knowledge: &wiring.Knowledge{},
		Evaluation: &wiring.Evaluation{
			SuiteService: evalapp.NewSuiteService(nil), JobService: evalapp.NewJobService(nil, nil),
			QueryService:      evalapp.NewQueryService(contractQueryRepo{}),
			ExperimentService: evalapp.NewExperimentService(contractExpRepo{}),
			CandidateService:  evalapp.NewCandidateCommandService(contractCandRepo{}),
		},
		IAM: &wiring.IAM{
			AdminService:      iamapp.NewAdminService(contractAdminTR{}),
			TenantService:     iamapp.NewTenantService(contractTenantR{}, logger),
			InvitationService: iamapp.NewInvitationService(contractInvR{}),
		},
	}
}

func isDDDAuthOverride(routePath string) (bool, iamport.TokenClaims) {
	adminClaims := iamport.TokenClaims{Sub: "contract-admin", TenantID: "contract-tenant", Role: "admin"}
	globalAdmin := iamport.TokenClaims{Sub: "contract-admin", GlobalRole: "global_admin"}
	switch {
	case strings.HasPrefix(routePath, "/admin/tenants"):
		return true, globalAdmin
	case strings.HasPrefix(routePath, "/admin/providers"), strings.HasPrefix(routePath, "/admin/models"):
		return true, adminClaims
	case strings.HasPrefix(routePath, "/tenant/"), strings.HasPrefix(routePath, "/workflows"),
		strings.HasPrefix(routePath, "/workflow-runs"), strings.HasPrefix(routePath, "/workflow-approvals"),
		strings.HasPrefix(routePath, "/operation-proposals"):
		return true, adminClaims
	default:
		return false, iamport.TokenClaims{}
	}
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: record-contracts <out-dir>")
		os.Exit(2)
	}
	outDir := os.Args[1]
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		panic(err)
	}

	cfg, err := config.Load()
	if err != nil {
		panic(err)
	}
	cfg.GitHubClientID = "contract-recorder"
	cfg.GitHubClientSecret = "contract-recorder"
	cfg.JWTPrivateKeyPEM = mustGeneratePEM()

	logger, _ := observability.NewLogger("test")
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	metrics := observability.NewPrometheusMetrics(logger)
	gateway := llmgateway.NewGateway(nil, nil, nil).WithLogger(logger)
	router := api.SetupRouter(cfg, logger, gateway, nil, nil, nil, nil)

	ddRouter := apihttp.NewRouter(buildDDDContainer(cfg, key, logger, metrics))
	jwtSvc := iamtoken.NewJWTService(key)

	// Phase 1: legacy router records ALL routes (unauth baseline).
	for _, route := range router.Routes() {
		filename := goldenName(route.Method, route.Path)
		recordRoute(router, route.Method, route.Path, filepath.Join(outDir, filename))
	}

	// Phase 2: DDD router overwrites selected routes with auth responses.
	evalWhitelist := map[string]bool{
		"GET /evaluations/overview": true, "GET /evaluations/resources": true,
		"GET /evaluations/suites": true, "GET /evaluations/runs": true,
		"GET /evaluations/candidates": true, "GET /evaluations/experiments": true,
		"GET /evaluations/resources/:kind/:id/timeline": true,
		"POST /evaluations/candidates/:id/reject":       true,
		"POST /evaluations/experiments/:id/pause":       true,
		"POST /evaluations/experiments/:id/promote":     true,
		"POST /evaluations/experiments/:id/rollback":    true,
	}
	for _, route := range ddRouter.Routes() {
		filename := filepath.Join(outDir, goldenName(route.Method, route.Path))
		routeKey := route.Method + " " + route.Path
		switch {
		case evalWhitelist[routeKey]:
			recordEvalRoute(ddRouter, jwtSvc, route.Method, route.Path, filename)
		case routeKey == "POST /agents/:id/self-modify":
			// Proposal ID is a random UUID: record a regex assertion instead
			// of a byte-exact body so replay is deterministic.
			recordSelfModifyRoute(ddRouter, jwtSvc, route.Path, filename)
		default:
			if ok, claims := isDDDAuthOverride(route.Path); ok {
				recordAuthRoute(ddRouter, jwtSvc, claims, route.Method, route.Path, nil, filename)
			}
		}
	}

	fmt.Printf("done recording\n")
}

func goldenName(method, path string) string {
	safe := strings.NewReplacer("/", "_", ":", "_", "*", "_").Replace(path)
	return fmt.Sprintf("%s%s.golden.json", strings.ToLower(method), safe)
}

func recordAuthRoute(router http.Handler, tokens iamport.TokenService, claims iamport.TokenClaims,
	method, routePath string, body json.RawMessage, outPath string,
) {
	path := resolvePath(routePath, method, body)
	token, err := tokens.Sign(claims, time.Hour)
	if err != nil {
		panic(err)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	c := Case{Name: "authenticated-success", Method: method, Path: path, WantStatus: rec.Code}
	if body != nil {
		c.Body = body
	}
	if json.Valid(rec.Body.Bytes()) {
		c.WantBody = json.RawMessage(rec.Body.Bytes())
	}
	out, _ := json.MarshalIndent([]Case{c}, "", "  ")
	if err := os.WriteFile(outPath, out, 0o644); err != nil {
		panic(err)
	}
}

func recordEvalRoute(router http.Handler, tokens iamport.TokenService, method, routePath, outPath string) {
	path := strings.ReplaceAll(routePath, ":kind", "skill")
	path = strings.ReplaceAll(path, ":id", "resource-1")
	if method == http.MethodPost {
		path = strings.ReplaceAll(routePath, ":id", "experiment-1")
		if strings.Contains(routePath, "/candidates/") {
			path = strings.ReplaceAll(routePath, ":id", "candidate-1")
		}
	}
	c := Case{Name: "authenticated-success", Method: method, Path: path}
	if method == http.MethodPost {
		c.Name = "authenticated-conflict"
		c.Body = json.RawMessage(`{"reason":"reviewed","idempotency_key":"contract-request","expected_state_version":1}`)
	}
	token, err := tokens.Sign(iamport.TokenClaims{Sub: "contract-admin", TenantID: "contract-tenant", Role: "admin"}, time.Hour)
	if err != nil {
		panic(err)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(c.Body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	c.WantStatus = rec.Code
	if json.Valid(rec.Body.Bytes()) {
		c.WantBody = json.RawMessage(rec.Body.Bytes())
	}
	out, _ := json.MarshalIndent([]Case{c}, "", "  ")
	if err := os.WriteFile(outPath, out, 0o644); err != nil {
		panic(err)
	}
}

func recordSelfModifyRoute(router http.Handler, tokens iamport.TokenService, routePath, outPath string) {
	path := strings.ReplaceAll(routePath, ":id", "contract-id")
	body := json.RawMessage(`{"name":"contract-renamed","description":"contract","systemPrompt":"prompt",
"llmModel":"qwen-plus","maxIterations":10,"maxContextTokens":8000,"allowedSkills":[],
"mcpToolIds":[],"knowledgeWorkspaceIds":[],"memoryScope":"user","checkpointEnabled":false}`)
	c := Case{
		Name: "authenticated-pending", Method: http.MethodPost, Path: path, Body: body,
		WantStatus: http.StatusAccepted,
		// proposalId is a random UUID at record time; assert shape only.
		// Response is a gin.H map: encoding/json sorts map keys alphabetically.
		WantBodyRE: `\{"proposalId":"[0-9a-f-]+","reason":"pending_approval","status":"pending_approval"\}`,
	}
	token, err := tokens.Sign(iamport.TokenClaims{Sub: "contract-admin", TenantID: "contract-tenant", Role: "admin"}, time.Hour)
	if err != nil {
		panic(err)
	}
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(c.Body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != c.WantStatus {
		panic(fmt.Sprintf("self-modify: got status %d, want %d: %s", rec.Code, c.WantStatus, rec.Body.String()))
	}
	out, _ := json.MarshalIndent([]Case{c}, "", "  ")
	if err := os.WriteFile(outPath, out, 0o644); err != nil {
		panic(err)
	}
}

// resolvePath replaces path params with placeholder IDs.
func resolvePath(routePath, method string, body json.RawMessage) string {
	p := routePath
	p = strings.ReplaceAll(p, ":provider_id", "contract-provider")
	p = strings.ReplaceAll(p, ":model_id", "contract-model")
	p = strings.ReplaceAll(p, ":tenant_id", "contract-tenant-id")
	p = strings.ReplaceAll(p, ":member_id", "contract-member")
	p = strings.ReplaceAll(p, ":workflowId", "contract-workflow")
	p = strings.ReplaceAll(p, ":runId", "contract-run")
	p = strings.ReplaceAll(p, ":id", "contract-id")
	return p
}

// ── Legacy router recording (unauth baseline) ──────────────────────────

func recordRoute(router http.Handler, method, path, outPath string) {
	cases := []Case{{
		Name:       "default-unauth",
		Method:     method,
		Path:       path,
		WantStatus: 0,
	}}
	for i := range cases {
		req := httptest.NewRequest(cases[i].Method, cases[i].Path, bytes.NewReader(cases[i].Body))
		for k, v := range cases[i].Headers {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		cases[i].WantStatus = rec.Code
		body, _ := io.ReadAll(rec.Body)
		if json.Valid(body) {
			cases[i].Body = json.RawMessage(body)
		}
	}
	out, _ := json.MarshalIndent(cases, "", "  ")
	_ = os.WriteFile(outPath, out, 0o644)
}

// ── Stub repos (matching contract_test.go) ──────────────────────────────

type contractProvRepo struct{}

func (contractProvRepo) Create(_ context.Context, _ string, _ *llmdomain.Provider) error { return nil }
func (contractProvRepo) Get(_ context.Context, _ string, _ string) (*llmdomain.Provider, error) {
	return &llmdomain.Provider{
		ID: "contract-provider", Name: "stub", Kind: llmdomain.ProviderOpenAICompat,
		BaseURL: "https://stub.example.com/v1", Enabled: true,
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}, nil
}
func (contractProvRepo) List(_ context.Context, _ string) ([]llmdomain.Provider, error) {
	return nil, nil
}
func (contractProvRepo) Update(_ context.Context, _ string, _ *llmdomain.Provider) error { return nil }
func (contractProvRepo) Delete(_ context.Context, _ string, _ string) error              { return nil }

type contractModRepo struct{}

func (contractModRepo) Create(_ context.Context, _ string, _ *llmdomain.Model) error { return nil }
func (contractModRepo) Get(_ context.Context, _ string, _ string) (*llmdomain.Model, error) {
	return nil, errStubNotFound
}
func (contractModRepo) List(_ context.Context, _ string, _ llmport.ModelFilter) ([]llmdomain.Model, error) {
	return nil, nil
}
func (contractModRepo) Update(_ context.Context, _ string, _ *llmdomain.Model) error { return nil }
func (contractModRepo) UpsertDiscovered(_ context.Context, _ string, _ string, _ []llmdomain.Model) ([]llmdomain.Model, error) {
	return nil, nil
}
func (contractModRepo) Delete(_ context.Context, _ string, _ string) error         { return nil }
func (contractModRepo) Toggle(_ context.Context, _ string, _ string, _ bool) error { return nil }

type contractProvRuntime struct{}

func (contractProvRuntime) ListModels(_ context.Context, _ llmdomain.Provider) ([]llmport.DiscoveredModel, error) {
	return []llmport.DiscoveredModel{{Name: "mock-model-1"}, {Name: "mock-model-2"}}, nil
}
func (contractProvRuntime) Health(_ context.Context, _ llmdomain.Provider) error { return nil }

// ── Workflow stubs ──────────────────────────────────────────────────────

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

type contractVerRepo struct{}

func (contractVerRepo) CreateVersion(_ context.Context, _ string, _ *workflowdomain.Version) error {
	return nil
}
func (contractVerRepo) GetVersion(_ context.Context, _ string, _ string) (*workflowdomain.Version, error) {
	return nil, workflowdomain.ErrNotFound
}
func (contractVerRepo) NextVersionNumber(_ context.Context, _ string, _ string) (int64, error) {
	return 1, nil
}
func (contractVerRepo) ListVersions(_ context.Context, _ string, _ string, _ workflowport.VersionListQuery) ([]workflowdomain.Version, int, error) {
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

type contractCtrlRepo struct{}

func (contractCtrlRepo) GetRun(_ context.Context, _ string, _ string) (*workflowdomain.Run, error) {
	return nil, workflowdomain.ErrNotFound
}
func (contractCtrlRepo) ControlRun(_ context.Context, _ string, _ string, _ int64, _ workflowdomain.RunStatus, _ string, _ workflowdomain.Event) error {
	return nil
}
func (contractCtrlRepo) ListApprovals(_ context.Context, _ string, _ string, _ bool) ([]workflowdomain.Approval, error) {
	return nil, nil
}
func (contractCtrlRepo) DecideApproval(_ context.Context, _ string, _ string, _ int64, _ string, _ workflowdomain.ApprovalDecision, _ string, _ string, _ workflowdomain.Event) error {
	return nil
}
func (contractCtrlRepo) ListEffectIntents(_ context.Context, _ string, _ string) ([]workflowdomain.EffectIntent, error) {
	return nil, nil
}
func (contractCtrlRepo) ResolveEffect(_ context.Context, _ string, _ string, _ int64, _ workflowdomain.ManualAction, _ string, _ string, _ workflowdomain.Event) error {
	return nil
}

type contractAgtExec struct{}

func (contractAgtExec) ExecuteAgent(_ context.Context, _ string, _ string, _ string) (string, string, error) {
	return "", "", errors.New("stub: agent execution unavailable")
}

// ── IAM stubs ───────────────────────────────────────────────────────────

type contractAdminTR struct{}

func (contractAdminTR) Count(_ context.Context, _ iamdomain.TenantFilter) (int, error) { return 0, nil }
func (contractAdminTR) List(_ context.Context, _ iamdomain.TenantFilter) ([]iamdomain.Tenant, error) {
	return nil, nil
}
func (contractAdminTR) Get(_ context.Context, _ string) (*iamdomain.Tenant, error) {
	return nil, errStubNotFound
}
func (contractAdminTR) Create(_ context.Context, _ iamdomain.Tenant) error { return nil }
func (contractAdminTR) UpdatePatch(_ context.Context, _ string, _ iamdomain.TenantPatch) error {
	return nil
}
func (contractAdminTR) HardDelete(_ context.Context, _ string) error      { return nil }
func (contractAdminTR) ProvisionSchema(_ context.Context, _ string) error { return nil }

type contractTenantR struct{}

func (contractTenantR) CountMembers(_ context.Context, _ string) (int, error) { return 0, nil }
func (contractTenantR) ListMembers(_ context.Context, _ string, _ int, _ int) ([]iamdomain.Member, error) {
	return nil, nil
}
func (contractTenantR) GetMemberRole(_ context.Context, _ string, _ string) (string, error) {
	return "member", nil
}
func (contractTenantR) UpdateMemberRole(_ context.Context, _ string, _ string, _ string) error {
	return nil
}
func (contractTenantR) DeleteMember(_ context.Context, _ string, _ string) error { return nil }
func (contractTenantR) GetTenantSettings(_ context.Context, _ string) (string, bool, []byte, error) {
	return "stub-tenant", false, []byte(`{}`), nil
}
func (contractTenantR) UpdateTenantName(_ context.Context, _ string, _ string) error { return nil }
func (contractTenantR) UpdateTenantSettings(_ context.Context, _ string, _ []byte) error {
	return nil
}
func (contractTenantR) ListUserTenants(_ context.Context, _ string) ([]iamdomain.UserTenantInfo, error) {
	return nil, nil
}

type contractInvR struct{}

func (contractInvR) Create(_ context.Context, _ iamdomain.TenantInvitation) error { return nil }
func (contractInvR) ConsumeAndJoin(_ context.Context, _ iamdomain.InvitationJoinInput) (*iamdomain.InvitationJoinResult, error) {
	return nil, errStubNotFound
}
func (contractInvR) ConsumeAndJoinExisting(_ context.Context, _ iamdomain.ExistingInvitationJoinInput) (*iamdomain.InvitationJoinResult, error) {
	return nil, errStubNotFound
}

// ── Existing evaluation stubs ───────────────────────────────────────────

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

type contractExpRepo struct{}

func (contractExpRepo) ValidatePrerequisites(context.Context, string, domain.ResourceRef,
	domain.ResourceRef, string) error {
	return nil
}
func (contractExpRepo) Create(context.Context, string, domain.Experiment, domain.Deployment) error {
	return nil
}
func (contractExpRepo) Get(context.Context, string, string) (domain.Experiment, bool, error) {
	return domain.Experiment{}, false, nil
}
func (contractExpRepo) SaveDecision(context.Context, string, domain.Experiment, domain.Decision, domain.StageMetrics, string, string) (domain.Experiment, domain.Decision, error) {
	return domain.Experiment{}, domain.DecisionHold, nil
}
func (contractExpRepo) ApplyCommand(context.Context, string, string, domain.ExperimentCommandAction, domain.ExperimentCommand) (domain.Experiment, error) {
	return domain.Experiment{}, domain.ErrExperimentStateConflict
}
func (contractExpRepo) ResolveDeployment(context.Context, string, string, string) (domain.Deployment, bool, error) {
	return domain.Deployment{}, false, nil
}

type contractCandRepo struct{}

func (contractCandRepo) Reject(context.Context, string, string, domain.CandidateCommand) (domain.CandidateSummary, error) {
	return domain.CandidateSummary{}, domain.ErrCandidateCommandConflict
}

// ── Agent stubs ─────────────────────────────────────────────────────────

type contractPropRepo struct{}

func (contractPropRepo) Create(context.Context, agentdomain.ResourceChangeProposal, agentdomain.ProposalEvent) error {
	return nil
}
func (contractPropRepo) Get(context.Context, string) (agentdomain.ResourceChangeProposal, error) {
	return agentdomain.ResourceChangeProposal{}, agentdomain.ErrProposalNotFound
}
func (contractPropRepo) UpdateDraft(
	context.Context, agentdomain.ResourceChangeProposal, agentdomain.ProposalEvent,
) error {
	return nil
}
func (contractPropRepo) Cancel(context.Context, string, string, time.Time) error  { return nil }
func (contractPropRepo) Confirm(context.Context, string, string, time.Time) error { return nil }
func (contractPropRepo) ClaimApplying(
	context.Context, string, string, time.Time,
) (agentdomain.ResourceChangeProposal, error) {
	return agentdomain.ResourceChangeProposal{}, agentdomain.ErrProposalNotFound
}
func (contractPropRepo) Finish(
	context.Context, string, agentdomain.ProposalStatus, agentdomain.ApplyResult, agentdomain.ProposalEvent,
) error {
	return nil
}
func (contractPropRepo) ListEvents(context.Context, string) ([]agentdomain.ProposalEvent, error) {
	return nil, agentdomain.ErrProposalNotFound
}

type contractPropAuthorizer struct{}

func (contractPropAuthorizer) AuthorizeProposal(
	context.Context, string, string, agentdomain.ResourceKind, agentdomain.ProposalOperation,
) error {
	return nil
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

type contractDashboardRepo struct{}

func (contractDashboardRepo) Overview(context.Context, string) (platformdomain.DashboardOverview, error) {
	return platformdomain.DashboardOverview{}, nil
}

// ── Helpers ─────────────────────────────────────────────────────────────

func mustGeneratePEM() string {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(fmt.Errorf("generate rsa key: %w", err))
	}
	der := x509.MarshalPKCS1PrivateKey(key)
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: der}
	return string(pem.EncodeToMemory(block))
}
