package middleware

import (
	"net/http"
	"testing"

	mcpdomain "github.com/byteBuilderX/stratum/internal/mcp/domain"
)

func TestPlatformMCPErrorsMapToTenantSafeStatuses(t *testing.T) {
	tests := []struct {
		err  error
		want int
	}{
		{err: mcpdomain.ErrPlatformManagedServer, want: http.StatusConflict},
		// stdio 全链禁用 + 安全护栏：这三类拒绝是客户端输入问题，映射 400
		// 而非 5xx（防止监控误报为服务端故障，也是"stdio 写→400"验收锚点）。
		{err: mcpdomain.ErrUnsupportedTransport, want: http.StatusBadRequest},
		{err: mcpdomain.ErrInvalidServerURL, want: http.StatusBadRequest},
		{err: mcpdomain.ErrUnsupportedAuth, want: http.StatusBadRequest},
	}
	for _, tt := range tests {
		if got := MapErrorToStatus(tt.err); got != tt.want {
			t.Fatalf("MapErrorToStatus(%v) = %d, want %d", tt.err, got, tt.want)
		}
	}
}
