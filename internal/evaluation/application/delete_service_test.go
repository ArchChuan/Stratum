package application

import (
	"context"
	"errors"
	"strings"
	"testing"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
	"github.com/stretchr/testify/require"
)

// stubRoles 模拟租户角色解析器。
type stubRoles struct {
	role string
	err  error
}

func (r *stubRoles) ResolveTenantRole(context.Context, string, string) (string, error) {
	return r.role, r.err
}

// deleteFake 实现全部 7 个删除实体接口（测试 helper，方法与共享字段透传）。
type deleteFake struct {
	createdBy string
	found     bool
	getErr    error
	delErr    error
	deleted   []string
	audit     *auditdomain.ResourceChangeAuditEvent
}

func (f *deleteFake) GetSuiteCreatedBy(context.Context, string, string) (string, bool, error) {
	return f.createdBy, f.found, f.getErr
}
func (f *deleteFake) DeleteSuite(_ context.Context, _ string, id string, a *auditdomain.ResourceChangeAuditEvent) error {
	f.deleted = append(f.deleted, "suite:"+id)
	f.audit = a
	return f.delErr
}
func (f *deleteFake) GetRunCreatedBy(context.Context, string, string) (string, bool, error) {
	return f.createdBy, f.found, f.getErr
}
func (f *deleteFake) DeleteRun(_ context.Context, _ string, id string, a *auditdomain.ResourceChangeAuditEvent) error {
	f.deleted = append(f.deleted, "run:"+id)
	f.audit = a
	return f.delErr
}
func (f *deleteFake) GetJobCreatedBy(context.Context, string, string) (string, bool, error) {
	return f.createdBy, f.found, f.getErr
}
func (f *deleteFake) DeleteJob(_ context.Context, _ string, id string, a *auditdomain.ResourceChangeAuditEvent) error {
	f.deleted = append(f.deleted, "job:"+id)
	f.audit = a
	return f.delErr
}
func (f *deleteFake) GetExperimentCreatedBy(context.Context, string, string) (string, bool, error) {
	return f.createdBy, f.found, f.getErr
}
func (f *deleteFake) DeleteExperiment(_ context.Context, _ string, id string, a *auditdomain.ResourceChangeAuditEvent) error {
	f.deleted = append(f.deleted, "experiment:"+id)
	f.audit = a
	return f.delErr
}
func (f *deleteFake) GetCandidateCreatedBy(context.Context, string, string) (string, bool, error) {
	return f.createdBy, f.found, f.getErr
}
func (f *deleteFake) DeleteCandidate(_ context.Context, _ string, id string, a *auditdomain.ResourceChangeAuditEvent) error {
	f.deleted = append(f.deleted, "candidate:"+id)
	f.audit = a
	return f.delErr
}
func (f *deleteFake) GetReviewItemCreatedBy(context.Context, string, string) (string, bool, error) {
	return f.createdBy, f.found, f.getErr
}
func (f *deleteFake) DeleteReviewItem(_ context.Context, _ string, id string, a *auditdomain.ResourceChangeAuditEvent) error {
	f.deleted = append(f.deleted, "review:"+id)
	f.audit = a
	return f.delErr
}
func (f *deleteFake) GetFeedbackCreatedBy(context.Context, string, string) (string, bool, error) {
	return f.createdBy, f.found, f.getErr
}
func (f *deleteFake) DeleteFeedback(_ context.Context, _ string, id string, a *auditdomain.ResourceChangeAuditEvent) error {
	f.deleted = append(f.deleted, "feedback:"+id)
	f.audit = a
	return f.delErr
}

func newDeleteTestService(fake *deleteFake, roles port.TenantRoleResolver) *DeleteService {
	return NewDeleteService(roles, fake, fake, fake, fake, fake, fake, fake)
}

// TestDeleteAuthorizeMatrix 覆盖删除门禁的授权矩阵（fail-closed 语义）。
func TestDeleteAuthorizeMatrix(t *testing.T) {
	cases := []struct {
		name          string
		role          string
		roleErr       error
		actorID       string
		createdBy     string
		wantForbidden bool
	}{
		{name: "owner always allowed", role: "owner", actorID: "admin-1", createdBy: "someone-else", wantForbidden: false},
		{name: "creator allowed", role: "admin", actorID: "creator-1", createdBy: "creator-1", wantForbidden: false},
		{name: "admin non creator forbidden", role: "admin", actorID: "admin-1", createdBy: "creator-1", wantForbidden: true},
		// member 无评测写权限（创建路径均 admin+），尝试删除的必然是他人资源。
		{name: "member forbidden", role: "member", actorID: "member-1", createdBy: "admin-1", wantForbidden: true},
		{name: "unknown role forbidden", role: "editor", actorID: "user-1", createdBy: "admin-1", wantForbidden: true},
		{name: "resolver error fail closed", role: "owner", roleErr: errors.New("lookup failed"), actorID: "user-1", createdBy: "user-1", wantForbidden: true},
		{name: "empty actor forbidden", role: "owner", actorID: "", createdBy: "creator-1", wantForbidden: true},
		{name: "legacy empty creator only owner", role: "admin", actorID: "admin-1", createdBy: "", wantForbidden: true},
		{name: "legacy empty creator owner allowed", role: "owner", actorID: "owner-1", createdBy: "", wantForbidden: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &deleteFake{createdBy: tc.createdBy, found: true}
			svc := newDeleteTestService(fake, &stubRoles{role: tc.role, err: tc.roleErr})
			err := svc.DeleteSuite(context.Background(), "tenant-1", "suite-1", tc.actorID)
			if tc.wantForbidden {
				require.ErrorIs(t, err, domain.ErrDeleteForbidden)
				require.Empty(t, fake.deleted, "delete must not run when forbidden")
				return
			}
			require.NoError(t, err)
			require.Equal(t, []string{"suite:suite-1"}, fake.deleted)
		})
	}
}

// TestDeleteRolesNilFailClosed 覆盖 roles 未装配时全量 fail-closed。
func TestDeleteRolesNilFailClosed(t *testing.T) {
	fake := &deleteFake{createdBy: "creator-1", found: true}
	svc := NewDeleteService(nil, fake, fake, fake, fake, fake, fake, fake)
	err := svc.DeleteSuite(context.Background(), "tenant-1", "suite-1", "owner-1")
	require.ErrorIs(t, err, domain.ErrDeleteForbidden)
	require.Empty(t, fake.deleted)
}

// TestDeleteEntityNotFound 每实体未命中 → 各自的 not-found sentinel，Delete 不执行。
func TestDeleteEntityNotFound(t *testing.T) {
	cases := []struct {
		name    string
		method  func(*DeleteService, context.Context, string, string, string) error
		wantErr error
	}{
		{"suite", (*DeleteService).DeleteSuite, ErrSuiteNotFound},
		{"run", (*DeleteService).DeleteRun, ErrRunNotFound},
		{"job", (*DeleteService).DeleteJob, ErrJobNotFound},
		{"experiment", (*DeleteService).DeleteExperiment, ErrExperimentNotFound},
		{"candidate", (*DeleteService).DeleteCandidate, domain.ErrCandidateNotFound},
		{"review item", (*DeleteService).DeleteReviewItem, ErrReviewItemNotFound},
		{"feedback", (*DeleteService).DeleteFeedback, ErrFeedbackNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &deleteFake{found: false}
			svc := newDeleteTestService(fake, &stubRoles{role: "owner"})
			err := tc.method(svc, context.Background(), "tenant-1", "id-1", "user-1")
			require.ErrorIs(t, err, tc.wantErr)
			require.Empty(t, fake.deleted, "delete must not run when entity missing")
		})
	}
}

// TestDeleteEntityChain 每实体：创建者可删且 Delete 收到审计事件。
func TestDeleteEntityChain(t *testing.T) {
	cases := []struct {
		name     string
		method   func(*DeleteService, context.Context, string, string, string) error
		wantCall string
	}{
		{"suite", (*DeleteService).DeleteSuite, "suite:suite-1"},
		{"run", (*DeleteService).DeleteRun, "run:run-1"},
		{"job", (*DeleteService).DeleteJob, "job:job-1"},
		{"experiment", (*DeleteService).DeleteExperiment, "experiment:exp-1"},
		{"candidate", (*DeleteService).DeleteCandidate, "candidate:cand-1"},
		{"review item", (*DeleteService).DeleteReviewItem, "review:rev-1"},
		{"feedback", (*DeleteService).DeleteFeedback, "feedback:fb-1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &deleteFake{createdBy: "creator-1", found: true}
			svc := newDeleteTestService(fake, &stubRoles{role: "admin"})
			err := tc.method(svc, context.Background(), "tenant-1", tc.wantCall[strings.IndexByte(tc.wantCall, ':')+1:], "creator-1")
			require.NoError(t, err)
			require.Equal(t, []string{tc.wantCall}, fake.deleted)
			require.NotNil(t, fake.audit)
			require.Equal(t, auditdomain.ResourceKindEvaluation, fake.audit.ResourceKind)
			require.Equal(t, auditdomain.ChangeOpDelete, fake.audit.Operation)
			require.Equal(t, "creator-1", fake.audit.ActorID)
			require.Equal(t, auditdomain.ChangeActorUser, fake.audit.ActorType)
			require.Equal(t, auditdomain.ChangeSourceAPI, fake.audit.Source)
		})
	}
}

// TestDeleteReferencedPropagates 被引用 409 语义：仓储返回 ErrEntityReferenced 向上传播。
func TestDeleteReferencedPropagates(t *testing.T) {
	fake := &deleteFake{createdBy: "creator-1", found: true, delErr: domain.ErrEntityReferenced}
	svc := newDeleteTestService(fake, &stubRoles{role: "owner"})
	err := svc.DeleteSuite(context.Background(), "tenant-1", "suite-1", "owner-1")
	require.ErrorIs(t, err, domain.ErrEntityReferenced)
}
