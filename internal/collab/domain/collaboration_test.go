package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/collab/domain"
	"github.com/stretchr/testify/require"
)

var fixedNow = time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

func TestCollaborationStateMachine(t *testing.T) {
	t.Run("start stamps started_at", func(t *testing.T) {
		c := &domain.Collaboration{Status: domain.CollabCreated}
		require.NoError(t, c.Start(fixedNow))
		require.Equal(t, domain.CollabRunning, c.Status)
		require.NotNil(t, c.StartedAt)
		require.Equal(t, fixedNow, *c.StartedAt)
	})

	t.Run("complete from running stamps completed_at", func(t *testing.T) {
		c := &domain.Collaboration{Status: domain.CollabRunning}
		require.NoError(t, c.Complete(fixedNow))
		require.Equal(t, domain.CollabCompleted, c.Status)
		require.NotNil(t, c.CompletedAt)
		require.Equal(t, fixedNow, *c.CompletedAt)
	})

	t.Run("fail from running stamps completed_at", func(t *testing.T) {
		c := &domain.Collaboration{Status: domain.CollabRunning}
		require.NoError(t, c.Fail(fixedNow))
		require.Equal(t, domain.CollabFailed, c.Status)
		require.NotNil(t, c.CompletedAt)
	})

	t.Run("cancel from created and running", func(t *testing.T) {
		for _, status := range []domain.CollabStatus{domain.CollabCreated, domain.CollabRunning} {
			c := &domain.Collaboration{Status: status}
			require.NoError(t, c.Cancel(fixedNow))
			require.Equal(t, domain.CollabCanceled, c.Status)
			require.NotNil(t, c.CompletedAt)
		}
	})

	t.Run("illegal transitions rejected", func(t *testing.T) {
		tests := []struct {
			name   string
			status domain.CollabStatus
			apply  func(*domain.Collaboration) error
		}{
			{"complete from created", domain.CollabCreated, func(c *domain.Collaboration) error { return c.Complete(fixedNow) }},
			{"complete from completed", domain.CollabCompleted, func(c *domain.Collaboration) error { return c.Complete(fixedNow) }},
			{"fail from created", domain.CollabCreated, func(c *domain.Collaboration) error { return c.Fail(fixedNow) }},
			{"fail from failed", domain.CollabFailed, func(c *domain.Collaboration) error { return c.Fail(fixedNow) }},
			{"start from running", domain.CollabRunning, func(c *domain.Collaboration) error { return c.Start(fixedNow) }},
			{"start from completed", domain.CollabCompleted, func(c *domain.Collaboration) error { return c.Start(fixedNow) }},
			{"cancel from completed", domain.CollabCompleted, func(c *domain.Collaboration) error { return c.Cancel(fixedNow) }},
			{"cancel from canceled", domain.CollabCanceled, func(c *domain.Collaboration) error { return c.Cancel(fixedNow) }},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				c := &domain.Collaboration{Status: tc.status}
				err := tc.apply(c)
				require.True(t, errors.Is(err, domain.ErrCollabInvalidTransition))
				require.Equal(t, tc.status, c.Status, "state must not move on failed transition")
			})
		}
	})
}
