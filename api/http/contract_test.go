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
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/api"
	apihttp "github.com/byteBuilderX/stratum/api/http"
	"github.com/byteBuilderX/stratum/api/wiring"
	"github.com/byteBuilderX/stratum/config"
	evalapp "github.com/byteBuilderX/stratum/internal/evaluation/application"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
	iamport "github.com/byteBuilderX/stratum/internal/iam/domain/port"
	iamtoken "github.com/byteBuilderX/stratum/internal/iam/infrastructure/token"
	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	"github.com/byteBuilderX/stratum/pkg/observability"
)

type contractCase struct {
	Name       string            `json:"name"`
	Method     string            `json:"method"`
	Path       string            `json:"path"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       json.RawMessage   `json:"body,omitempty"`
	WantBody   json.RawMessage   `json:"want_body,omitempty"`
	WantStatus int               `json:"want_status"`
}

func TestContracts(t *testing.T) {
	cfg, err := config.Load()
	if err != nil {
		t.Skipf("config load failed: %v", err)
	}
	// Mirror record-contracts: force auth-gated routes to register so the
	// replayed router exposes the same surface as the recorder.
	cfg.GitHubClientID = "contract-recorder"
	cfg.GitHubClientSecret = "contract-recorder"
	cfg.JWTPrivateKeyPEM = mustGeneratePEM(t)

	logger, _ := observability.NewLogger("test")
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	metrics := observability.NewPrometheusMetrics(logger)
	gateway := llmgateway.NewGateway().WithLogger(logger)
	router := api.SetupRouter(cfg, logger, gateway, nil, nil, nil, nil)
	evaluationRouter := apihttp.NewRouter(&wiring.Container{
		Config: cfg, Logger: logger, Platform: &wiring.Platform{JWTService: iamtoken.NewJWTService(key), Metrics: metrics},
		LLMGateway: &wiring.LLMGateway{}, Skill: &wiring.Skill{}, Agent: &wiring.Agent{}, Workflow: &wiring.Workflow{},
		Knowledge: &wiring.Knowledge{}, MCP: &wiring.MCP{}, Memory: &wiring.Memory{},
		Evaluation: &wiring.Evaluation{
			SuiteService: evalapp.NewSuiteService(nil), JobService: evalapp.NewJobService(nil, nil),
			QueryService:      evalapp.NewQueryService(contractQueryRepo{}),
			ExperimentService: evalapp.NewExperimentService(contractExperimentRepo{}),
			CandidateService:  evalapp.NewCandidateCommandService(contractCandidateRepo{}),
		},
	})

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
				if strings.HasPrefix(c.Path, "/evaluations/") {
					token, signErr := iamtoken.NewJWTService(key).Sign(iamport.TokenClaims{
						Sub: "contract-admin", TenantID: "contract-tenant", Role: "admin",
					}, time.Hour)
					if signErr != nil {
						t.Fatal(signErr)
					}
					req.Header.Set("Authorization", "Bearer "+token)
				}
				for k, v := range c.Headers {
					req.Header.Set(k, v)
				}
				rec := httptest.NewRecorder()
				if strings.HasPrefix(c.Path, "/evaluations/") {
					evaluationRouter.ServeHTTP(rec, req)
				} else {
					router.ServeHTTP(rec, req)
				}
				if rec.Code != c.WantStatus {
					t.Errorf("%s %s: got status %d, want %d", c.Method, c.Path, rec.Code, c.WantStatus)
				}
				if len(c.WantBody) > 0 && !jsonEquivalent(rec.Body.Bytes(), c.WantBody) {
					t.Errorf("%s %s: body=%s want=%s", c.Method, c.Path, rec.Body.String(), c.WantBody)
				}
			}
		})
	}
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
	return domain.ExperimentPage{Items: []domain.ExperimentSummary{{
		ID: "experiment-contract", ResourceID: "agent-contract", StableRevisionID: "stable-contract",
		CanaryRevisionID: "canary-contract", Status: "running", Recommendation: "promote",
		ResourceKind: domain.ResourceKindAgent, StagePercent: 100, StateVersion: 2,
		PromotionEvidence: domain.PromotionEvidence{Eligible: true, Gates: domain.PromotionGates{
			Quality: domain.GatePassed, Cost: domain.GatePassed, Latency: domain.GatePassed,
			ErrorRate: domain.GatePassed, Security: domain.GatePassed,
		}, Blockers: []domain.PromotionBlocker{}},
	}}}, nil
}
func (contractQueryRepo) Timeline(context.Context, string, port.CenterFilter) (domain.TimelinePage, error) {
	return domain.TimelinePage{Items: []domain.TimelineEvent{}}, nil
}

type contractExperimentRepo struct{}

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

type contractCandidateRepo struct{}

func (contractCandidateRepo) Reject(context.Context, string, string, domain.CandidateCommand) (domain.CandidateSummary, error) {
	return domain.CandidateSummary{}, domain.ErrCandidateCommandConflict
}

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
