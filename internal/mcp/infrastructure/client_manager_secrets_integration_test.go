package infrastructure

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	mcpdomain "github.com/byteBuilderX/stratum/internal/mcp/domain"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres/postgrestest"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestPersistConnect_encryptsSecretsAtRest 验证 MCP 敏感字段（env 值、header 值、
// auth token）落库为密文（JSONB 内逐项 enc:v1: 前缀），非敏感字段保持明文；
// 且内存配置不被二次加密污染（深拷贝）。
func TestPersistConnect_encryptsSecretsAtRest(t *testing.T) {
	pool := postgrestest.NewPool(t)
	tenantID := postgrestest.CreateTestTenant(t, pool)
	m := NewClientManager(zap.NewNop(), nil, pool, "")
	require.NoError(t, m.WithSecretKey(mcpTestKey))
	ctx := tenantdb.WithTenant(t.Context(), &tenantdb.TenantContext{
		TenantID: tenantID, Role: tenantdb.RoleTenantAdmin,
	})

	cfg := &MCPServerConfig{
		ID: "srv-enc", Name: "srv-enc", Transport: "stdio", Command: "/bin/true",
		Env:     map[string]string{"OPENAI_API_KEY": "sk-plain-env"},
		Headers: map[string]string{"Authorization": "Bearer hdr-token"},
		Auth: &MCPAuthConfig{
			Type: mcpdomain.AuthTypeBearer, Token: "bt-plain-token",
		},
		Version: "v1",
		Timeout: 30 * time.Second,
	}
	require.NoError(t, m.persistConnect(ctx, cfg))

	var envJSON, hdrsJSON, authJSON string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT env, headers, auth_config FROM "tenant_`+tenantID+`".mcp_configs WHERE id=$1`, cfg.ID).
		Scan(&envJSON, &hdrsJSON, &authJSON))
	require.NotContains(t, envJSON, "sk-plain-env", "env value must not be stored in plaintext")
	require.Contains(t, envJSON, "enc:v1:")
	require.NotContains(t, hdrsJSON, "hdr-token", "header value must not be stored in plaintext")
	require.Contains(t, hdrsJSON, "enc:v1:")
	require.NotContains(t, authJSON, "bt-plain-token", "auth token must not be stored in plaintext")
	require.Contains(t, authJSON, "enc:v1:")
	// 非敏感字段保持明文。
	require.Contains(t, envJSON, "OPENAI_API_KEY")

	// 内存中的配置仍是明文（深拷贝未被二次加密）。
	require.Equal(t, "sk-plain-env", cfg.Env["OPENAI_API_KEY"])
	require.Equal(t, "bt-plain-token", cfg.Auth.Token)

	// GetServerConfig 从 DB 还原明文。
	got, err := m.GetServerConfig(ctx, cfg.ID)
	require.NoError(t, err)
	require.Equal(t, "sk-plain-env", got.Env["OPENAI_API_KEY"])
	require.Equal(t, "Bearer hdr-token", got.Headers["Authorization"])
	require.Equal(t, "bt-plain-token", got.Auth.Token)
}

// TestGetServerConfig_legacyPlaintextFailsClosed 验证加密上线前的存量明文
// （无前缀）读取时 fail closed：返回错误而不是把明文当可用配置。
func TestGetServerConfig_legacyPlaintextFailsClosed(t *testing.T) {
	pool := postgrestest.NewPool(t)
	tenantID := postgrestest.CreateTestTenant(t, pool)
	m := NewClientManager(zap.NewNop(), nil, pool, "")
	require.NoError(t, m.WithSecretKey(mcpTestKey))
	ctx := tenantdb.WithTenant(t.Context(), &tenantdb.TenantContext{
		TenantID: tenantID, Role: tenantdb.RoleTenantAdmin,
	})

	_, err := pool.Exec(ctx,
		`INSERT INTO "tenant_`+tenantID+`".mcp_configs
		 (id, name, transport, command, url, args, env, capabilities, timeout_sec,
		  enabled, version, headers, auth_config, retry_config)
		 VALUES ($1,$2,'stdio','','','[]','{"TOKEN":"sk-legacy"}','[]',30,true,'','{}','{}','{}')`,
		"srv-legacy", "srv-legacy")
	require.NoError(t, err)

	_, err = m.GetServerConfig(ctx, "srv-legacy")
	require.ErrorContains(t, err, "legacy plaintext")
	require.ErrorContains(t, err, "env secrets")
}

// TestConfigFromDBRow_roundTrip 验证 configFromDBRow 对密文行解密还原明文配置。
func TestConfigFromDBRow_roundTrip(t *testing.T) {
	m := NewClientManager(zap.NewNop(), nil, nil, "")
	require.NoError(t, m.WithSecretKey(mcpTestKey))

	envEnc, err := encryptSecretMap(mcpTestKey, map[string]string{"K": "v-plain"})
	require.NoError(t, err)
	envB, err := json.Marshal(envEnc)
	require.NoError(t, err)

	cfg, err := m.configFromDBRow(mcpConfigRow{
		id: "srv", name: "srv", transport: "stdio", timeoutSec: 30,
		env: envB,
	})
	require.NoError(t, err)
	require.Equal(t, "v-plain", cfg.Env["K"])
	require.True(t, strings.HasPrefix(string(envB), `{"K":"enc:v1:`), "DB row must hold ciphertext")
}
