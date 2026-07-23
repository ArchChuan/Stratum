package application

import (
	"testing"

	"github.com/byteBuilderX/stratum/internal/workflow/domain"
	"github.com/stretchr/testify/require"
)

func TestAuthorizeRunMemberCanReadEventsAndCancelOwnRun(t *testing.T) {
	run := &domain.Run{CreatedBy: "user-a"}
	actor := Actor{UserID: "user-a", Role: "member"}
	for _, action := range []RunAction{RunActionRead, RunActionEvents, RunActionCancel} {
		require.NoError(t, authorizeRun(run, actor, action))
	}
}

func TestAuthorizeRunMemberCannotReadAnotherUsersRun(t *testing.T) {
	run := &domain.Run{CreatedBy: "user-a"}
	actor := Actor{UserID: "user-b", Role: "member"}
	for _, action := range []RunAction{RunActionRead, RunActionEvents, RunActionCancel} {
		require.ErrorIs(t, authorizeRun(run, actor, action), domain.ErrNotFound)
	}
}

func TestAuthorizeRunMemberCannotAdministerOwnRun(t *testing.T) {
	run := &domain.Run{CreatedBy: "user-a"}
	actor := Actor{UserID: "user-a", Role: "member"}
	for _, action := range []RunAction{RunActionPause, RunActionResume, RunActionApprove, RunActionResolveManual} {
		require.ErrorIs(t, authorizeRun(run, actor, action), domain.ErrForbidden)
	}
}

func TestAuthorizeRunAdminAndOwnerCanControlTenantRun(t *testing.T) {
	run := &domain.Run{CreatedBy: "user-a"}
	for _, role := range []string{"admin", "owner"} {
		actor := Actor{UserID: role + "-1", Role: role}
		for _, action := range []RunAction{
			RunActionRead, RunActionEvents, RunActionCancel, RunActionPause,
			RunActionResume, RunActionApprove, RunActionResolveManual,
		} {
			require.NoError(t, authorizeRun(run, actor, action))
		}
	}
}

func TestAuthorizeRunFailsClosedWithoutActor(t *testing.T) {
	require.ErrorIs(t, authorizeRun(&domain.Run{CreatedBy: "user-a"}, Actor{}, RunActionRead), domain.ErrForbidden)
}
