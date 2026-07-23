package persistence

import (
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
)

func TestMapRevisionRepositoryErrorMapsOnlyUnknownCommitOutcome(t *testing.T) {
	cause := errors.New("connection lost")
	mapped := mapRevisionRepositoryError(errors.Join(postgres.ErrCommitOutcomeUnknown, cause))
	if !errors.Is(mapped, port.ErrRevisionCommitUnknown) || !errors.Is(mapped, cause) {
		t.Fatalf("unknown commit mapping lost classification or cause: %v", mapped)
	}
	ordinary := errors.New("constraint failed")
	if got := mapRevisionRepositoryError(ordinary); !errors.Is(got, ordinary) || errors.Is(got, port.ErrRevisionCommitUnknown) {
		t.Fatalf("ordinary failure misclassified: %v", got)
	}
}
