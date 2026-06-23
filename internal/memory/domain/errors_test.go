package domain_test

import (
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
)

func TestErrorSentinels(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{"ErrFactNotFound", domain.ErrFactNotFound},
		{"ErrEntityNotFound", domain.ErrEntityNotFound},
		{"ErrAgentMemoryDisabled", domain.ErrAgentMemoryDisabled},
		{"ErrScopeMismatch", domain.ErrScopeMismatch},
		{"ErrFactQuotaExceeded", domain.ErrFactQuotaExceeded},
		{"ErrFactAlreadyDeleted", domain.ErrFactAlreadyDeleted},
		{"ErrInvalidStatus", domain.ErrInvalidStatus},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err == nil {
				t.Errorf("%s should not be nil", tt.name)
			}
			if tt.err.Error() == "" {
				t.Errorf("%s should have non-empty error message", tt.name)
			}
		})
	}
}

func TestErrorWrapping(t *testing.T) {
	wrapped := errors.Join(domain.ErrFactNotFound, errors.New("additional context"))
	if !errors.Is(wrapped, domain.ErrFactNotFound) {
		t.Error("wrapped error should match ErrFactNotFound")
	}
}
