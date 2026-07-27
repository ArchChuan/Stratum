package middleware

import (
	"net/http"
	"testing"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
)

func TestMapEvaluationEvolutionErrors(t *testing.T) {
	tests := []struct {
		err  error
		want int
	}{
		{domain.ErrInvalidCenterQuery, http.StatusBadRequest},
		{domain.ErrInvalidCandidateCommand, http.StatusBadRequest},
		{domain.ErrCenterResourceNotFound, http.StatusNotFound},
		{domain.ErrCandidateNotFound, http.StatusNotFound},
		{domain.ErrExperimentStateConflict, http.StatusConflict},
		{domain.ErrExperimentCommandConflict, http.StatusConflict},
		{domain.ErrExperimentDeploymentConflict, http.StatusConflict},
		{domain.ErrExperimentStableNotPublished, http.StatusConflict},
		{domain.ErrExperimentInvalidCandidate, http.StatusConflict},
		{domain.ErrExperimentSuiteNotPublished, http.StatusConflict},
		{domain.ErrExperimentOfflineRunRequired, http.StatusConflict},
		{domain.ErrCandidateStateConflict, http.StatusConflict},
		{domain.ErrOptimizationIdempotencyConflict, http.StatusConflict},
		{domain.ErrFeedbackIdempotencyConflict, http.StatusConflict},
		{domain.ErrFeedbackTraceForbidden, http.StatusForbidden},
	}
	for _, tc := range tests {
		if got := MapErrorToStatus(tc.err); got != tc.want {
			t.Errorf("MapErrorToStatus(%v)=%d want %d", tc.err, got, tc.want)
		}
	}
}
