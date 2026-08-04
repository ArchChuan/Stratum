package persistence

import (
	"errors"
	"testing"

	"github.com/pashagolub/pgxmock/v2"
	"github.com/stretchr/testify/require"
)

// errAny 是通用的查询失败注入。
var errAny = errors.New("boom")

// newMockPool 创建 pgxmock 池，满足 pgxPool 与 tokenStoreDB。
func newMockPool(t *testing.T) pgxmock.PgxPoolIface {
	t.Helper()
	mock, err := pgxmock.NewPool()
	require.NoError(t, err)
	return mock
}
