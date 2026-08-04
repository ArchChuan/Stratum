package persistence

import (
	"context"
	"testing"
	"time"

	"github.com/pashagolub/pgxmock/v2"
	"github.com/stretchr/testify/require"
)

func newReplayRepo(t *testing.T) (*MCPTokenReplayRepo, pgxmock.PgxPoolIface) {
	mock := newMockPool(t)
	// 构造签名保持 *pgxpool.Pool 以兼容 wiring；测试注入接口实例。
	return &MCPTokenReplayRepo{pool: mock}, mock
}

func TestMCPTokenReplay_ConsumeInvocationJTI_Inserted(t *testing.T) {
	repo, mock := newReplayRepo(t)
	expectTenantTx(mock)
	mock.ExpectExec(`INSERT INTO mcp_invocation_jtis`).
		WithArgs("jti-1", pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 1))
	mock.ExpectCommit()

	consumed, err := repo.ConsumeInvocationJTI(context.Background(), "t1", "jti-1", time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.True(t, consumed)
}

func TestMCPTokenReplay_ConsumeInvocationJTI_AlreadyConsumed(t *testing.T) {
	repo, mock := newReplayRepo(t)
	expectTenantTx(mock)
	mock.ExpectExec(`INSERT INTO mcp_invocation_jtis`).
		WithArgs("jti-1", pgxmock.AnyArg()).
		WillReturnResult(pgxmock.NewResult("INSERT", 0))
	mock.ExpectCommit()

	consumed, err := repo.ConsumeInvocationJTI(context.Background(), "t1", "jti-1", time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.False(t, consumed)
}

func TestMCPTokenReplay_ConsumeInvocationJTI_ExecFails(t *testing.T) {
	repo, mock := newReplayRepo(t)
	expectTenantTx(mock)
	mock.ExpectExec(`INSERT INTO mcp_invocation_jtis`).
		WithArgs("jti-1", pgxmock.AnyArg()).
		WillReturnError(errAny)
	mock.ExpectRollback()

	consumed, err := repo.ConsumeInvocationJTI(context.Background(), "t1", "jti-1", time.Now().Add(time.Hour))
	require.Error(t, err)
	require.ErrorContains(t, err, "insert invocation JTI")
	require.False(t, consumed)
}
