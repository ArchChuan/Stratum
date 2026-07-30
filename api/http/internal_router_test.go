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

	"github.com/byteBuilderX/stratum/api/middleware"
	iamapp "github.com/byteBuilderX/stratum/internal/iam/application"
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
			router, err := NewInternalRouter(InternalRouterDeps{Exchange: exchange, Logger: zap.NewNop()})
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
	_, err := NewInternalRouter(InternalRouterDeps{Logger: zap.NewNop()})
	if err == nil {
		t.Fatal("expected missing token exchange dependency to fail")
	}
}

type internalExchangeFake struct {
	calls int
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
