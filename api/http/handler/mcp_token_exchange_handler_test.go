package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/byteBuilderX/stratum/api/middleware"
	iamapp "github.com/byteBuilderX/stratum/internal/iam/application"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func TestMCPTokenExchangeHandlerReturnsDelegationToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fake := &mcpTokenExchangerFake{token: "delegation"}
	handler := NewMCPTokenExchangeHandler(fake)
	router := gin.New()
	router.Use(middleware.ErrorHandler(zap.NewNop()))
	router.POST("/exchange", handler.Exchange)

	recorder := httptest.NewRecorder()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "/exchange", bytes.NewBufferString(
		`{"invocation_token":"invocation","resource_id":"resource-1"}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK || fake.req.ResourceID != "resource-1" {
		t.Fatalf("status=%d body=%s request=%+v", recorder.Code, recorder.Body.String(), fake.req)
	}
}

func TestMCPTokenExchangeHandlerRecordsSecurityDenials(t *testing.T) {
	tests := []struct {
		name         string
		exchangeErr  error
		wantReplay   int
		wantContract int
	}{
		{name: "replay", exchangeErr: iamapp.ErrPlatformMCPInvocationReplayed, wantReplay: 1},
		{name: "contract mismatch", exchangeErr: iamapp.ErrPlatformMCPContractInvalid, wantContract: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			metrics := &tokenExchangeMetricsFake{}
			handler := NewObservedMCPTokenExchangeHandler(
				&mcpTokenExchangerFake{err: tc.exchangeErr}, metrics,
			)
			router := gin.New()
			router.Use(middleware.ErrorHandler(zap.NewNop()))
			router.POST("/exchange", handler.Exchange)
			req := httptest.NewRequest(http.MethodPost, "/exchange", bytes.NewBufferString(
				`{"invocation_token":"invocation"}`,
			))
			req.Header.Set("Content-Type", "application/json")

			router.ServeHTTP(httptest.NewRecorder(), req)

			if metrics.replay != tc.wantReplay || metrics.contract != tc.wantContract {
				t.Fatalf("metrics=%+v", metrics)
			}
		})
	}
}

type mcpTokenExchangerFake struct {
	token string
	err   error
	req   iamapp.MCPTokenExchangeRequest
}

type tokenExchangeMetricsFake struct {
	replay, contract int
}

func (f *tokenExchangeMetricsFake) IncPlatformMCPReplayDenial(_ string) { f.replay++ }
func (f *tokenExchangeMetricsFake) IncPlatformMCPContractMismatch(_ string) {
	f.contract++
}

func (f *mcpTokenExchangerFake) Exchange(
	_ context.Context,
	req iamapp.MCPTokenExchangeRequest,
) (string, error) {
	f.req = req
	return f.token, f.err
}
