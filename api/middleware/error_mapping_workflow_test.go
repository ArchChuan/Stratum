package middleware

import (
	"net/http"
	"testing"

	workflowdomain "github.com/byteBuilderX/stratum/internal/workflow/domain"
	"github.com/stretchr/testify/require"
)

func TestWorkflowErrorStatusMapping(t *testing.T) {
	tests := []struct {
		err    error
		status int
	}{
		{err: workflowdomain.ErrNotFound, status: http.StatusNotFound},
		{err: workflowdomain.ErrRevisionConflict, status: http.StatusConflict},
		{err: workflowdomain.ErrIdempotencyConflict, status: http.StatusConflict},
		{err: workflowdomain.ErrInvalidSpec, status: http.StatusBadRequest},
		{err: workflowdomain.ErrInvalidTransition, status: http.StatusConflict},
		{err: workflowdomain.ErrGenerationConflict, status: http.StatusConflict},
		{err: workflowdomain.ErrFenceConflict, status: http.StatusConflict},
	}
	for _, tt := range tests {
		require.Equal(t, tt.status, MapErrorToStatus(tt.err))
	}
}
