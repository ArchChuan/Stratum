package middleware

import (
	"net/http"
	"testing"

	knowledgedomain "github.com/byteBuilderX/stratum/internal/knowledge/domain"
)

func TestMapKnowledgeRerankModelErrors(t *testing.T) {
	cases := []struct {
		err      error
		wantCode int
	}{
		{knowledgedomain.ErrRerankModelRequired, http.StatusBadRequest},
		{knowledgedomain.ErrInvalidRerankModel, http.StatusBadRequest},
		{knowledgedomain.ErrInvalidJudgeModel, http.StatusBadRequest},
	}
	for _, tc := range cases {
		if got := MapErrorToStatus(tc.err); got != tc.wantCode {
			t.Errorf("MapErrorToStatus(%v) = %d, want %d", tc.err, got, tc.wantCode)
		}
	}
}
