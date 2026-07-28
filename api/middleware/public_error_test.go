package middleware

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	agentdomain "github.com/byteBuilderX/stratum/internal/agent/domain"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func TestPublicErrorDescribesWrappedAssistantModelUnavailable(t *testing.T) {
	err := fmt.Errorf("resolve platform assistant: %w", agentdomain.ErrAssistantModelUnavailable)

	got := DescribePublicError(err, http.StatusServiceUnavailable)
	want := PublicErrorDescriptor{
		Message: "租户尚未配置平台助手模型",
		Code:    CodeSystemAssistantModelUnavailable,
	}
	if got != want {
		t.Fatalf("DescribePublicError() = %#v, want %#v", got, want)
	}
}

func TestPublicErrorHidesUnknownServerError(t *testing.T) {
	got := DescribePublicError(errors.New("provider secret=hidden"), http.StatusInternalServerError)
	want := PublicErrorDescriptor{Message: "internal server error"}
	if got != want {
		t.Fatalf("DescribePublicError() = %#v, want %#v", got, want)
	}
}

func TestPublicErrorRetainsNonServerError(t *testing.T) {
	got := DescribePublicError(errors.New("invalid request"), http.StatusBadRequest)
	want := PublicErrorDescriptor{Message: "invalid request"}
	if got != want {
		t.Fatalf("DescribePublicError() = %#v, want %#v", got, want)
	}
}

func TestErrorHandlerReturnsAssistantModelUnavailableContract(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(ErrorHandler(zap.NewNop()))
	router.GET("/assistant", func(c *gin.Context) {
		_ = c.Error(fmt.Errorf("resolve platform assistant: %w", agentdomain.ErrAssistantModelUnavailable))
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/assistant", nil))

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	wantBody := "{\"code\":\"SYSTEM_ASSISTANT_MODEL_UNAVAILABLE\",\"error\":\"租户尚未配置平台助手模型\"}"
	if response.Body.String() != wantBody {
		t.Fatalf("body = %q, want %q", response.Body.String(), wantBody)
	}
}

func TestErrorHandlerDoesNotLeakUnknownServerError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(ErrorHandler(zap.NewNop()))
	router.GET("/provider", func(c *gin.Context) {
		_ = c.Error(errors.New("provider secret=hidden"))
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/provider", nil))

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusInternalServerError)
	}
	wantBody := "{\"error\":\"internal server error\"}"
	if response.Body.String() != wantBody {
		t.Fatalf("body = %q, want %q", response.Body.String(), wantBody)
	}
}
