package middleware

import (
	"net/http"
	"testing"

	skilldomain "github.com/byteBuilderX/stratum/internal/skill/domain"
)

func TestMapErrorToStatusDistinguishesMissingSkillDraft(t *testing.T) {
	if got := MapErrorToStatus(skilldomain.ErrSkillDraftNotFound); got != http.StatusConflict {
		t.Fatalf("missing draft status = %d, want %d", got, http.StatusConflict)
	}
	if got := MapErrorToStatus(skilldomain.ErrSkillNotFound); got != http.StatusNotFound {
		t.Fatalf("missing skill status = %d, want %d", got, http.StatusNotFound)
	}
}
