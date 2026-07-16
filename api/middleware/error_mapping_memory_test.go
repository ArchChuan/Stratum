package middleware

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
)

func TestMapErrorToStatus_MemorySentinels(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		status int
	}{
		{name: "fact not found", err: domain.ErrFactNotFound, status: http.StatusNotFound},
		{name: "entity not found", err: domain.ErrEntityNotFound, status: http.StatusNotFound},
		{name: "scope mismatch", err: domain.ErrScopeMismatch, status: http.StatusForbidden},
		{name: "agent disabled", err: domain.ErrAgentMemoryDisabled, status: http.StatusForbidden},
		{name: "quota", err: domain.ErrFactQuotaExceeded, status: http.StatusConflict},
		{name: "already deleted", err: domain.ErrFactAlreadyDeleted, status: http.StatusConflict},
		{name: "invalid status", err: domain.ErrInvalidStatus, status: http.StatusBadRequest},
		{name: "missing user", err: domain.ErrUserIDMismatch, status: http.StatusBadRequest},
		{name: "empty content", err: domain.ErrEmptyContent, status: http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MapErrorToStatus(fmt.Errorf("memory operation: %w", tt.err)); got != tt.status {
				t.Fatalf("MapErrorToStatus() = %d, want %d", got, tt.status)
			}
		})
	}
}
