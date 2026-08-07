package middleware

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agentdomain "github.com/byteBuilderX/stratum/internal/agent/domain"
	llmgatewaydomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
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

func TestPublicErrorHidesUpstreamBaseURL(t *testing.T) {
	// The wrapped error carries the internal provider BaseURL; the public
	// message must be a fixed string that never echoes it back.
	err := fmt.Errorf("anthropic: POST http://10.0.0.5:8080/v1/messages 返回 401，"+
		"请检查 API Key 与 Base URL 是否正确: %w", llmgatewaydomain.ErrUpstreamRequestFailed)

	got := DescribePublicError(err, http.StatusBadGateway)
	want := PublicErrorDescriptor{Message: "上游模型服务请求失败，请稍后重试"}
	if got != want {
		t.Fatalf("DescribePublicError() = %#v, want %#v", got, want)
	}
	if strings.Contains(got.Message, "10.0.0.5") || strings.Contains(got.Message, "http") {
		t.Fatalf("public message leaks internal URL: %q", got.Message)
	}
}

func TestErrorHandlerDoesNotLeakUpstreamBaseURL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(ErrorHandler(zap.NewNop()))
	router.GET("/complete", func(c *gin.Context) {
		_ = c.Error(fmt.Errorf("anthropic: POST http://10.0.0.5:8080/v1/messages 返回 401，"+
			"请检查 API Key 与 Base URL 是否正确: %w", llmgatewaydomain.ErrUpstreamRequestFailed))
	})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/complete", nil))

	if response.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusBadGateway)
	}
	wantBody := "{\"error\":\"上游模型服务请求失败，请稍后重试\"}"
	if response.Body.String() != wantBody {
		t.Fatalf("body = %q, want %q", response.Body.String(), wantBody)
	}
	if strings.Contains(response.Body.String(), "10.0.0.5") {
		t.Fatalf("response leaks internal BaseURL: %s", response.Body.String())
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
