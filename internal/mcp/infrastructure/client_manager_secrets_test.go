package infrastructure

import (
	"strings"
	"testing"

	mcpdomain "github.com/byteBuilderX/stratum/internal/mcp/domain"
	"github.com/byteBuilderX/stratum/pkg/crypto"
	"github.com/stretchr/testify/require"
)

// mcpTestKey 是 MCP 加密 helper 的固定测试密钥。
var mcpTestKey = [32]byte{
	9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9,
	9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9, 9,
}

func TestEncryptSecretMap_roundTrip(t *testing.T) {
	src := map[string]string{
		"OPENAI_API_KEY": "sk-env-secret",
		"EMPTY":          "",
		"LOG_LEVEL":      "info",
	}
	enc, err := encryptSecretMap(mcpTestKey, src)
	require.NoError(t, err)
	// 加密后每个非空值都是带前缀的密文，且不等于明文；key 名保持原样。
	for k, v := range src {
		if v == "" {
			require.Equal(t, "", enc[k], "empty value stays empty")
			continue
		}
		require.True(t, strings.HasPrefix(enc[k], "enc:v1:"), "value of %s not ciphertext", k)
		require.NotEqual(t, v, enc[k])
	}
	// 深拷贝：源 map 不被污染。
	require.Equal(t, src, map[string]string{
		"OPENAI_API_KEY": "sk-env-secret", "EMPTY": "", "LOG_LEVEL": "info",
	})

	dec, err := decryptSecretMap(mcpTestKey, enc)
	require.NoError(t, err)
	require.Equal(t, src, dec)
}

func TestDecryptSecretMap_legacyPlaintextFailsClosed(t *testing.T) {
	// 加密上线前的存量明文：必须报错，禁止按明文返回。
	_, err := decryptSecretMap(mcpTestKey, map[string]string{"TOKEN": "sk-legacy"})
	require.ErrorIs(t, err, crypto.ErrLegacyPlaintext)
}

func TestDecryptSecretMap_corruptedCiphertextFailsClosed(t *testing.T) {
	_, err := decryptSecretMap(mcpTestKey, map[string]string{"TOKEN": "enc:v1:garbage!!!"})
	require.Error(t, err)
	require.NotErrorIs(t, err, crypto.ErrLegacyPlaintext)
}

func TestEncryptAuthConfig_roundTrip(t *testing.T) {
	auth := &MCPAuthConfig{
		Type:               mcpdomain.AuthTypeAPIKey,
		Token:              "bt-oauth-token",
		APIKeyHeader:       "X-API-Key",
		APIKeyValue:        "ak-value",
		OAuth2ClientID:     "client-123",
		OAuth2ClientSecret: "client-secret",
		OAuth2TokenURL:     "https://issuer.example.com/token",
		OAuth2Scopes:       []string{"read", "write"},
	}
	enc, err := encryptAuthConfig(mcpTestKey, auth)
	require.NoError(t, err)
	// secret 类字段加密，非 secret 字段透传；原对象不被污染。
	for _, secret := range []string{enc.Token, enc.APIKeyValue, enc.OAuth2ClientSecret} {
		require.True(t, strings.HasPrefix(secret, "enc:v1:"))
		require.NotContains(t, secret, "secret")
		require.NotContains(t, secret, "ak-")
	}
	require.Equal(t, auth.Token, "bt-oauth-token", "input must stay plaintext")
	require.Equal(t, enc.APIKeyHeader, "X-API-Key")
	require.Equal(t, enc.OAuth2ClientID, "client-123")
	require.Equal(t, enc.OAuth2TokenURL, "https://issuer.example.com/token")
	require.Equal(t, enc.OAuth2Scopes, []string{"read", "write"})

	dec, err := decryptAuthConfig(mcpTestKey, enc)
	require.NoError(t, err)
	require.Equal(t, auth, dec)
}

func TestDecryptAuthConfig_legacyPlaintextFailsClosed(t *testing.T) {
	// 存量明文 token：fail closed。
	_, err := decryptAuthConfig(mcpTestKey, &MCPAuthConfig{Token: "sk-legacy"})
	require.ErrorIs(t, err, crypto.ErrLegacyPlaintext)
}

func TestEncryptAuthConfig_nilSafe(t *testing.T) {
	enc, err := encryptAuthConfig(mcpTestKey, nil)
	require.NoError(t, err)
	require.Nil(t, enc)
	dec, err := decryptAuthConfig(mcpTestKey, nil)
	require.NoError(t, err)
	require.Nil(t, dec)
}
