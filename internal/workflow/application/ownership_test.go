package application

import (
	"testing"

	"github.com/byteBuilderX/stratum/internal/workflow/domain"
	"github.com/stretchr/testify/require"
)

// TestEnforceWorkflowOwnershipMatrix pins the fail-closed ownership matrix for
// Update/Publish/Delete. 见 spec 矩阵：owner 全放行；admin 除 Delete 需
// createdBy==actorID 外放行；member 仅白名单成员且非 Delete；其余一律 403。
func TestEnforceWorkflowOwnershipMatrix(t *testing.T) {
	cases := []struct {
		name      string
		role      string
		actor     string
		createdBy string
		editors   []string
		op        OwnershipOp
		wantErr   error
	}{
		{"owner update", "owner", "owner-1", "u-2", nil, OpEdit, nil},
		{"owner delete", "owner", "owner-1", "u-2", nil, OpDelete, nil},
		{"owner rollback", "owner", "owner-1", "u-2", nil, OpRollback, nil},
		{"admin creator update", "admin", "u-1", "u-1", nil, OpEdit, nil},
		{"admin creator delete", "admin", "u-1", "u-1", nil, OpDelete, nil},
		{"admin non-creator update", "admin", "u-9", "u-1", nil, OpEdit, nil},
		{"admin non-creator delete forbidden", "admin", "u-9", "u-1", nil, OpDelete, domain.ErrForbidden},
		{"admin creator rollback", "admin", "u-1", "u-1", nil, OpRollback, nil},
		{"admin non-creator rollback", "admin", "u-9", "u-1", nil, OpRollback, nil},
		{"whitelist member update", "member", "m-1", "u-1", []string{"m-1"}, OpEdit, nil},
		{"whitelist member delete forbidden", "member", "m-1", "u-1", []string{"m-1"}, OpDelete, domain.ErrForbidden},
		{"whitelist member rollback forbidden", "member", "m-1", "u-1", []string{"m-1"}, OpRollback, domain.ErrForbidden},
		{"whitelist member access forbidden", "member", "m-1", "u-1", []string{"m-1"}, OpAccess, domain.ErrForbidden},
		{"other member update forbidden", "member", "m-2", "u-1", []string{"m-1"}, OpEdit, domain.ErrForbidden},
		{"other member delete forbidden", "member", "m-2", "u-1", []string{"m-1"}, OpDelete, domain.ErrForbidden},
		{"other member rollback forbidden", "member", "m-2", "u-1", []string{"m-1"}, OpRollback, domain.ErrForbidden},
		{"unknown role forbidden", "guest", "g-1", "u-1", nil, OpEdit, domain.ErrForbidden},
		{"unknown role rollback forbidden", "guest", "g-1", "u-1", nil, OpRollback, domain.ErrForbidden},
		{"empty actor forbidden", "owner", "", "u-1", nil, OpEdit, domain.ErrForbidden},
		{"empty actor rollback forbidden", "owner", "", "u-1", nil, OpRollback, domain.ErrForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := enforceOwnership(tc.role, tc.actor, tc.createdBy, tc.editors, tc.op)
			require.ErrorIs(t, err, tc.wantErr)
		})
	}
}
