package wiring

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	storagepg "github.com/byteBuilderX/stratum/pkg/storage/postgres"
)

// TestBuildKnowledgeWithDBRegistersBuiltinSync guards against a regression
// where buildKnowledge dereferences c.Knowledge (to wire BuiltinSync) before
// c.Knowledge itself is initialized. That nil deref only fires when a DB is
// present, which is exactly the startup path in production — so it cannot be
// caught by the db==nil unit paths.
func TestBuildKnowledgeWithDBRegistersBuiltinSync(t *testing.T) {
	// pgxpool.New connects lazily; a bogus DSN is enough to obtain a non-nil
	// pool for construction-time wiring (no query is issued by buildKnowledge).
	pool, err := pgxpool.New(context.Background(), "postgres://u:p@127.0.0.1:1/s")
	require.NoError(t, err)
	defer pool.Close()

	c := &Container{
		Logger: zap.NewNop(),
		Storage: &Storage{
			PG: &storagepg.Pool{Pool: pool},
			// Milvus may be nil: vectorAdapter wraps the store without deref
			// during wiring.
		},
	}

	require.NotPanics(t, func() {
		require.NoError(t, c.buildKnowledge(context.Background()))
	}, "buildKnowledge must not nil-deref c.Knowledge when a DB is present")
	require.NotNil(t, c.Knowledge, "c.Knowledge must be initialized")
	require.NotNil(t, c.Knowledge.BuiltinSync, "BuiltinSync must be wired when a DB is present")
}
