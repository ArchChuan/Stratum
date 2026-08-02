package http

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/api/http/handler"
	"github.com/byteBuilderX/stratum/api/middleware"
	"github.com/byteBuilderX/stratum/internal/agent/domain"
	iamapp "github.com/byteBuilderX/stratum/internal/iam/application"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/byteBuilderX/stratum/pkg/platformmcp"
	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"
)

func TestInternalRouterTokenExchangeRequiresPlatformMCPIdentity(t *testing.T) {
	tests := []struct {
		name       string
		tlsState   *tls.ConnectionState
		wantStatus int
		wantCalls  int
	}{
		{name: "allows verified platform MCP", tlsState: platformMCPTestTLS(t), wantStatus: http.StatusOK, wantCalls: 1},
		{name: "rejects missing client certificate", wantStatus: http.StatusForbidden},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			exchange := &internalExchangeFake{}
			router, err := NewInternalRouter(internalRouterTestDeps(exchange))
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(
				http.MethodPost,
				"/internal/platform-mcp/token/exchange",
				strings.NewReader("{\"invocation_token\":\"invocation\"}"),
			)
			req.Header.Set("Content-Type", "application/json")
			req.TLS = tc.tlsState
			res := httptest.NewRecorder()

			router.ServeHTTP(res, req)

			if res.Code != tc.wantStatus || exchange.calls != tc.wantCalls {
				t.Fatalf("status=%d calls=%d, want status=%d calls=%d", res.Code, exchange.calls, tc.wantStatus, tc.wantCalls)
			}
		})
	}
}

func TestInternalRouterRejectsMissingTokenExchange(t *testing.T) {
	_, err := NewInternalRouter(InternalRouterDeps{Logger: zap.NewNop(), AuthMetrics: observability.NoopMetrics{}})
	if err == nil {
		t.Fatal("expected missing token exchange dependency to fail")
	}
}

func TestInternalRouterCapabilitiesRequireExactDelegatedScope(t *testing.T) {
	deps := internalRouterTestDeps(&internalExchangeFake{})
	deps.Tokens = internalTokensFake{claims: &platformmcp.APIDelegationClaims{
		TenantID: "tenant-1", Role: "member", HTTPMethod: http.MethodPost,
		PathTemplate:     "/internal/platform-assistant/docs/search",
		RegisteredClaims: jwt.RegisteredClaims{Subject: "user-1"},
	}}
	router, err := NewInternalRouter(deps)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, authorization string
		wantStatus          int
	}{
		{name: "allows exact delegation", authorization: "Bearer delegation", wantStatus: http.StatusOK},
		{name: "rejects missing delegation", wantStatus: http.StatusForbidden},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/internal/platform-assistant/docs/search",
				strings.NewReader(`{"query":"Agent"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", tc.authorization)
			req.TLS = platformMCPTestTLS(t)
			res := httptest.NewRecorder()
			router.ServeHTTP(res, req)
			if res.Code != tc.wantStatus {
				t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
			}
		})
	}
}

type internalExchangeFake struct {
	calls int
}

type internalTokensFake struct {
	claims *platformmcp.APIDelegationClaims
}

func (f internalTokensFake) VerifyAPIDelegation(string) (*platformmcp.APIDelegationClaims, error) {
	return f.claims, nil
}

type internalDocsFake struct{}

func (internalDocsFake) Search(context.Context, string) ([]domain.Citation, error) { return nil, nil }

type internalDiagnosticsFake struct{}

func (internalDiagnosticsFake) Collect(
	context.Context,
	domain.DiagnosticRequest,
) (domain.DiagnosticEvidence, error) {
	return domain.DiagnosticEvidence{}, nil
}

type internalProposalsFake struct{}

func (internalProposalsFake) Create(
	context.Context,
	handler.PlatformAssistantProposalInput,
) (domain.ResourceChangeProposalArtifact, error) {
	return domain.ResourceChangeProposalArtifact{}, nil
}

func internalRouterTestDeps(exchange internalTokenExchanger) InternalRouterDeps {
	return InternalRouterDeps{
		Exchange: exchange, Tokens: internalTokensFake{}, Logger: zap.NewNop(), AuthMetrics: observability.NoopMetrics{},
		Capabilities: handler.PlatformAssistantCapabilityDeps{
			Docs: internalDocsFake{}, Diagnostics: internalDiagnosticsFake{}, Proposals: internalProposalsFake{},
		},
	}
}

func (f *internalExchangeFake) Exchange(
	context.Context,
	iamapp.MCPTokenExchangeRequest,
) (string, error) {
	f.calls++
	return "delegation", nil
}

func platformMCPTestTLS(t *testing.T) *tls.ConnectionState {
	t.Helper()
	uri, err := url.Parse(middleware.PlatformMCPWorkloadURI)
	if err != nil {
		t.Fatal(err)
	}
	cert := &x509.Certificate{URIs: []*url.URL{uri}}
	return &tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{cert},
		VerifiedChains:   [][]*x509.Certificate{{cert}},
	}
}
