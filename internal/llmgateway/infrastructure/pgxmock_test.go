package infrastructure

import (
	"testing"

	"github.com/pashagolub/pgxmock/v2"
	"github.com/stretchr/testify/require"
)

// newFactMock 返回可注入 tenantPool 接口的 pgxmock 池。
func newFactMock(t *testing.T) pgxmock.PgxPoolIface {
	t.Helper()
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	return mock
}

// anyArgs 生成 n 个 AnyArg，用于对参数值不敏感的期望。
func anyArgs(n int) []any {
	args := make([]any, n)
	for i := range args {
		args[i] = pgxmock.AnyArg()
	}
	return args
}
