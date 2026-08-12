package middleware

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agentapp "github.com/byteBuilderX/stratum/internal/agent/application"
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

// D7/D8：审批工作台与聊天页依赖可解释的中文终态/操作消息。approval sentinel
// 经 DescribePublicError 必须映射为固定中文（不泄 payload/内部 detail），status 由
// MapErrorToStatus 单独守卫（410/409 等）。
func TestPublicErrorDescribesApprovalSentinels(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"expired", agentapp.ErrApprovalExpired, "审批已过期"},
		{"policy_changed", agentdomain.ErrApprovalPolicyChanged, "权限策略已变更，请重新发起"},
		{"conversation_gone", agentdomain.ErrApprovalConversationGone, "会话已删除，审批已失效"},
		{"self_decision", agentdomain.ErrApprovalSelfDecision, "不能审批自己发起的请求"},
		{"role_denied", agentdomain.ErrApprovalRoleDenied, "需要管理员权限"},
		{"already_decided", agentdomain.ErrApprovalAlreadyDecided, "该审批已处理"},
		{"already_executed", agentdomain.ErrApprovalAlreadyExecuted, "该工具已执行"},
		{"invalidated", agentdomain.ErrApprovalInvalidated, "审批已失效"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := DescribePublicError(fmt.Errorf("approval: %w", tc.err), http.StatusConflict)
			if got.Message != tc.want {
				t.Fatalf("DescribePublicError(%v).Message = %q, want %q", tc.err, got.Message, tc.want)
			}
		})
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
