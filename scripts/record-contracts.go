//go:build contracts

// Package main records HTTP contract golden files by replaying canonical
// requests against the current SetupRouter and writing JSON snapshots.
package main

import (
	"bytes"
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

	"github.com/byteBuilderX/stratum/api"
	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	"github.com/byteBuilderX/stratum/internal/platform/config"
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
	gateway := llmgateway.NewGateway().WithLogger(logger)

	// db / rdb / capGW / skillAdapter / memPipeline left nil — auth routes still
	// register because cfg.GitHubClientID + JWT key are set; tenant/admin groups
	// gated on db!=nil are intentionally skipped (no DB available offline).
	router := api.SetupRouter(cfg, logger, gateway, nil, nil, nil, nil, nil)

	routes := router.Routes()
	for _, route := range routes {
		safe := strings.NewReplacer("/", "_", ":", "_", "*", "_").Replace(route.Path)
		filename := fmt.Sprintf("%s%s.golden.json", strings.ToLower(route.Method), safe)
		recordRoute(router, route.Method, route.Path, filepath.Join(outDir, filename))
	}
	fmt.Printf("recorded %d routes\n", len(routes))
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
