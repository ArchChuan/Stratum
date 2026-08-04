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
	}
	for _, tt := range tests {
		if got := MapErrorToStatus(tt.err); got != tt.want {
			t.Fatalf("MapErrorToStatus(%v) = %d, want %d", tt.err, got, tt.want)
		}
	}
}
