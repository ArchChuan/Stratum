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
	evalapp "github.com/byteBuilderX/stratum/internal/evaluation/application"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
	iamport "github.com/byteBuilderX/stratum/internal/iam/domain/port"
	iamtoken "github.com/byteBuilderX/stratum/internal/iam/infrastructure/token"
	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	"github.com/byteBuilderX/stratum/pkg/observability"
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
	// Force GitHub OAuth + JWT path so all auth-gated routes get registered.
	cfg.GitHubClientID = "contract-recorder"
	cfg.GitHubClientSecret = "contract-recorder"
	cfg.JWTPrivateKeyPEM = mustGeneratePEM()

	logger, _ := observability.NewLogger("test")
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
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

	routes := router.Routes()
	for _, route := range routes {
		safe := strings.NewReplacer("/", "_", ":", "_", "*", "_").Replace(route.Path)
		filename := fmt.Sprintf("%s%s.golden.json", strings.ToLower(route.Method), safe)
		recordRoute(router, route.Method, route.Path, filepath.Join(outDir, filename))
	}
	evaluationRoutes := 0
	evolutionRoutes := map[string]bool{
		"GET /evaluations/overview": true, "GET /evaluations/resources": true,
		"GET /evaluations/suites": true, "GET /evaluations/runs": true,
		"GET /evaluations/candidates": true, "GET /evaluations/experiments": true,
		"GET /evaluations/resources/:kind/:id/timeline": true,
		"POST /evaluations/candidates/:id/reject":       true,
		"POST /evaluations/experiments/:id/pause":       true,
		"POST /evaluations/experiments/:id/promote":     true,
		"POST /evaluations/experiments/:id/rollback":    true,
	}
	for _, route := range evaluationRouter.Routes() {
		if !evolutionRoutes[route.Method+" "+route.Path] {
			continue
		}
		safe := strings.NewReplacer("/", "_", ":", "_", "*", "_").Replace(route.Path)
		filename := fmt.Sprintf("%s%s.golden.json", strings.ToLower(route.Method), safe)
		recordEvaluationRoute(evaluationRouter, iamtoken.NewJWTService(key), route.Method, route.Path,
			filepath.Join(outDir, filename))
		evaluationRoutes++
	}
	fmt.Printf("recorded %d routes\n", len(routes)+evaluationRoutes)
}

func recordEvaluationRoute(router http.Handler, tokens iamport.TokenService, method, routePath, outPath string) {
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

// mustGeneratePEM returns a fresh PKCS1 RSA private key in PEM form so
// SetupRouter can parse it via parseRSAPrivateKey and enable auth routes.
func mustGeneratePEM() string {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(fmt.Errorf("generate rsa key: %w", err))
	}
	der := x509.MarshalPKCS1PrivateKey(key)
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: der}
	return string(pem.EncodeToMemory(block))
}
