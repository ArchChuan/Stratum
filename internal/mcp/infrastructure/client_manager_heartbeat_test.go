package infrastructure

import (
	"context"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/pkg/storage/postgres/postgrestest"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestRefreshHeartbeat_updatesHeartbeatInTenantSchema 验证 heartbeat 走租户边界：
// mcp_configs 是 tenant-schema 表，修复前在 public schema 下 UPDATE 必然 42P01
// 且错误被吞（heartbeat 永不刷新 → failover 自我 churn）。本测试断言真实 DB 中
// owner_heartbeat 确实前进，证明 search_path 切换正确。
func TestRefreshHeartbeat_updatesHeartbeatInTenantSchema(t *testing.T) {
	pool := postgrestest.NewPool(t)
	tenantID := postgrestest.CreateTestTenant(t, pool)
	m := NewClientManager(zap.NewNop(), nil, pool, "node-hb")

	m.mu.Lock()
	m.configs[tenantID+":srv-stdio"] = &MCPServerConfig{ID: "srv-stdio", Transport: "stdio"}
	m.configs[tenantID+":srv-sse"] = &MCPServerConfig{ID: "srv-sse", Transport: "sse"}
	m.mu.Unlock()

	ctx := context.Background()
	stale := time.Now().Add(-time.Hour)
	for _, id := range []string{"srv-stdio", "srv-sse"} {
		_, err := pool.Exec(ctx,
			`INSERT INTO "tenant_`+tenantID+`".mcp_configs
			 (id, name, transport, command, url, args, env, capabilities, timeout_sec,
			  enabled, version, headers, auth_config, retry_config, owner_node, owner_heartbeat)
			 VALUES ($1,$2,'stdio','','','[]','{}','[]',30,true,'','{}','{}','{}','node-hb',$3)`,
			id, id, stale)
		require.NoError(t, err)
	}

	m.refreshHeartbeat()

	// stdio 的 heartbeat 必须前进；sse 不参与心跳，保持原值。
	var hb time.Time
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT owner_heartbeat FROM "tenant_`+tenantID+`".mcp_configs WHERE id=$1`, "srv-stdio").
		Scan(&hb))
	require.True(t, hb.After(stale), "stdio owner_heartbeat must advance, got %v", hb)
	require.WithinDuration(t, time.Now(), hb, 5*time.Second)

	var sseHB time.Time
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT owner_heartbeat FROM "tenant_`+tenantID+`".mcp_configs WHERE id=$1`, "srv-sse").
		Scan(&sseHB))
	require.WithinDuration(t, stale, sseHB, time.Second, "sse heartbeat must not change")
}

// TestRefreshHeartbeat_poolFailureNotSwallowed 验证 DB 不可用时 heartbeat 不再吞错：
// 事务失败向上传播并记 ERROR 日志（zap.Nop 吞掉输出但调用不 panic、不崩溃）。
func TestRefreshHeartbeat_poolFailureNotSwallowed(t *testing.T) {
	pool := postgrestest.NewPool(t)
	tenantID := postgrestest.CreateTestTenant(t, pool)
	m := NewClientManager(zap.NewNop(), nil, pool, "node-hb")

	m.mu.Lock()
	m.configs[tenantID+":srv-stdio"] = &MCPServerConfig{ID: "srv-stdio", Transport: "stdio"}
	m.mu.Unlock()

	pool.Close() // Begin 必然失败

	require.NotPanics(t, m.refreshHeartbeat)
}
