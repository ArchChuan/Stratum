// Package http_test replays recorded HTTP contract goldens to detect
// backward-incompatible changes during the DDD refactor.
package http_test

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/api"
	apihttp "github.com/byteBuilderX/stratum/api/http"
	"github.com/byteBuilderX/stratum/api/wiring"
	"github.com/byteBuilderX/stratum/config"
	evalapp "github.com/byteBuilderX/stratum/internal/evaluation/application"
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
			QueryService: evalapp.NewQueryService(nil),
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
			}
		})
	}
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
