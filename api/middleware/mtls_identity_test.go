package middleware

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRequirePlatformMCPIdentityRejectsUnexpectedWorkloads(t *testing.T) {
	tests := []struct {
		name       string
		peer       *x509.Certificate
		wantStatus int
	}{
		{name: "allows platform MCP workload", peer: certificateWithURI(PlatformMCPWorkloadURI), wantStatus: http.StatusNoContent},
		{name: "rejects same CA wrong workload", peer: certificateWithURI("spiffe://stratum.local/ns/stratum/sa/other-service"), wantStatus: http.StatusForbidden},
		{name: "rejects certificate without URI", peer: &x509.Certificate{}, wantStatus: http.StatusForbidden},
		{name: "rejects missing client certificate", wantStatus: http.StatusForbidden},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			router.GET("/internal", RequirePlatformMCPIdentity(), func(c *gin.Context) {
				c.Status(http.StatusNoContent)
			})
			req := httptest.NewRequest(http.MethodGet, "/internal", nil)
			if tc.peer != nil {
				req.TLS = &tls.ConnectionState{
					PeerCertificates: []*x509.Certificate{tc.peer},
					VerifiedChains:   [][]*x509.Certificate{{tc.peer}},
				}
			}
			res := httptest.NewRecorder()

			router.ServeHTTP(res, req)

			if res.Code != tc.wantStatus {
				t.Fatalf("status=%d, want %d", res.Code, tc.wantStatus)
			}
		})
	}
}

func certificateWithURI(rawURI string) *x509.Certificate {
	uri, err := url.Parse(rawURI)
	if err != nil {
		panic(err)
	}
	return &x509.Certificate{URIs: []*url.URL{uri}}
}
