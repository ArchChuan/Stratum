package middleware

import (
	"net/http"
	"testing"

	agentapp "github.com/byteBuilderX/stratum/internal/agent/application"
	agentdomain "github.com/byteBuilderX/stratum/internal/agent/domain"
)

// D9 恢复层终结错误映射：会话已删除 → 410 Gone（对齐过期语义），策略变更 → 409
// Conflict（对齐 invalidated/decided 等终态冲突族）。两者都与既有 approval sentinel
// 一同受 MapErrorToStatus 守卫。
func TestMapAgentValidationErrors(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want int
	}{
		{agentdomain.ErrInvalidMaxIterations, http.StatusBadRequest},
		{agentdomain.ErrInvalidSamplingParameters, http.StatusBadRequest},
	} {
		if got := MapErrorToStatus(tc.err); got != tc.want {
			t.Errorf("MapErrorToStatus(%v)=%d want %d", tc.err, got, tc.want)
		}
	}
}

func TestMapAgentApprovalErrors(t *testing.T) {
	tests := []struct {
		err  error
		want int
	}{
		// D9 新增
		{agentdomain.ErrApprovalConversationGone, http.StatusGone},
		{agentdomain.ErrApprovalPolicyChanged, http.StatusConflict},
		// 既有 approval sentinel 回归
		{agentdomain.ErrApprovalNotFound, http.StatusNotFound},
		{agentdomain.ErrApprovalRoleDenied, http.StatusForbidden},
		{agentdomain.ErrApprovalSelfDecision, http.StatusConflict},
		{agentdomain.ErrApprovalInvalidated, http.StatusConflict},
		{agentdomain.ErrApprovalAlreadyDecided, http.StatusConflict},
		{agentdomain.ErrApprovalAlreadyExecuted, http.StatusConflict},
		{agentapp.ErrApprovalExpired, http.StatusGone},
		{agentapp.ErrApprovalNotApproved, http.StatusConflict},
		{agentdomain.ErrTooManyPendingApprovals, http.StatusTooManyRequests},
	}
	for _, tc := range tests {
		if got := MapErrorToStatus(tc.err); got != tc.want {
			t.Errorf("MapErrorToStatus(%v)=%d want %d", tc.err, got, tc.want)
		}
	}
}
