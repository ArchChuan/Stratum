package middleware

import (
	"fmt"
	"net/http"
	"testing"

	memoryapp "github.com/byteBuilderX/stratum/internal/memory/application"
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
		{name: "migration invalid tenant", err: domain.ErrMigrationInvalidTenant, status: http.StatusBadRequest},
		{name: "migration empty model", err: domain.ErrMigrationEmptyModel, status: http.StatusBadRequest},
		{name: "migration same model", err: domain.ErrMigrationSameModel, status: http.StatusBadRequest},
		{name: "migration not found", err: domain.ErrMigrationNotFound, status: http.StatusNotFound},
		{name: "migration already active", err: domain.ErrMigrationAlreadyActive, status: http.StatusConflict},
		{name: "migration not active", err: domain.ErrMigrationNotActive, status: http.StatusConflict},
		{name: "migration progress regressed", err: domain.ErrMigrationProgressRegressed, status: http.StatusConflict},
		{name: "migration not retryable", err: domain.ErrMigrationNotRetryable, status: http.StatusConflict},
		{name: "invalid category", err: domain.ErrInvalidCategory, status: http.StatusBadRequest},
		{name: "confidence out of range", err: domain.ErrConfidenceOutOfRange, status: http.StatusBadRequest},
		{name: "importance out of range", err: domain.ErrImportanceOutOfRange, status: http.StatusBadRequest},
		{name: "empty fact patch", err: domain.ErrEmptyFactPatch, status: http.StatusBadRequest},
		{name: "fact not editable", err: domain.ErrFactNotEditable, status: http.StatusConflict},
		{name: "summary not found", err: domain.ErrSummaryNotFound, status: http.StatusNotFound},
		{name: "snapshot not found", err: domain.ErrSnapshotNotFound, status: http.StatusNotFound},
		{name: "embedding unavailable", err: memoryapp.ErrMemoryEmbeddingUnavailable, status: http.StatusBadGateway},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MapErrorToStatus(fmt.Errorf("memory operation: %w", tt.err)); got != tt.status {
				t.Fatalf("MapErrorToStatus() = %d, want %d", got, tt.status)
			}
		})
	}
}
