package infrastructure

import (
	"fmt"

	"github.com/byteBuilderX/stratum/pkg/crypto"
)

// MCP server 配置的敏感字段（env 值、header 值、auth token/api key/oauth secret）
// 在落库前逐项经 crypto.EncryptSecret 加密（"enc:v1:" 前缀 AES-256-GCM 密文），
// 其余字段（key 名、URL、超时等）保持明文。
//
// 存量兼容策略：加密上线前落库的 JSONB 字段是明文，读取时 crypto.DecryptSecret
// 对无前缀值返回 ErrLegacyPlaintext。本层 fail closed：解密失败即返回"配置无效，
// 请重新保存"错误，禁止把存储值当明文使用。内存中的 MCPServerConfig 始终持有
// 明文，仅在 DB 边界（persistConnect / configFromDBRow / GetServerConfig）加解密，
// 避免同一对象被二次加密。
//
// 注意：加密后的密文禁止进入日志。

// encryptSecretMap 深拷贝 m 并把每个 value 加密为密文。nil 返回 nil。
func encryptSecretMap(key [32]byte, m map[string]string) (map[string]string, error) {
	if m == nil {
		return nil, nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		enc, err := crypto.EncryptSecret(key, v)
		if err != nil {
			return nil, fmt.Errorf("encrypt secret %q: %w", k, err)
		}
		out[k] = enc
	}
	return out, nil
}

// decryptSecretMap 把 m 的每个 value 从密文解密为明文。任一值解密失败即返回错误
// （fail closed），并拒绝返回部分解密的 map。
func decryptSecretMap(key [32]byte, m map[string]string) (map[string]string, error) {
	if m == nil {
		return nil, nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		plain, err := crypto.DecryptSecret(key, v)
		if err != nil {
			return nil, fmt.Errorf("decrypt secret %q: %w", k, err)
		}
		out[k] = plain
	}
	return out, nil
}

// encryptAuthConfig 深拷贝 auth 并只加密 secret 类字段（Token、APIKeyValue、
// OAuth2ClientSecret）；type、header 名、token URL 等非 secret 字段原样透传。
// nil 返回 nil。
func encryptAuthConfig(key [32]byte, auth *MCPAuthConfig) (*MCPAuthConfig, error) {
	if auth == nil {
		return nil, nil
	}
	token, err := crypto.EncryptSecret(key, auth.Token)
	if err != nil {
		return nil, fmt.Errorf("encrypt auth token: %w", err)
	}
	apiKeyValue, err := crypto.EncryptSecret(key, auth.APIKeyValue)
	if err != nil {
		return nil, fmt.Errorf("encrypt auth api key: %w", err)
	}
	clientSecret, err := crypto.EncryptSecret(key, auth.OAuth2ClientSecret)
	if err != nil {
		return nil, fmt.Errorf("encrypt auth oauth2 client secret: %w", err)
	}
	out := *auth
	out.Token = token
	out.APIKeyValue = apiKeyValue
	out.OAuth2ClientSecret = clientSecret
	return &out, nil
}

// decryptAuthConfig 把 auth 的 secret 类字段从密文解密为明文。任一字段解密失败
// 即返回错误（fail closed）。
func decryptAuthConfig(key [32]byte, auth *MCPAuthConfig) (*MCPAuthConfig, error) {
	if auth == nil {
		return nil, nil
	}
	token, err := crypto.DecryptSecret(key, auth.Token)
	if err != nil {
		return nil, fmt.Errorf("decrypt auth token: %w", err)
	}
	apiKeyValue, err := crypto.DecryptSecret(key, auth.APIKeyValue)
	if err != nil {
		return nil, fmt.Errorf("decrypt auth api key: %w", err)
	}
	clientSecret, err := crypto.DecryptSecret(key, auth.OAuth2ClientSecret)
	if err != nil {
		return nil, fmt.Errorf("decrypt auth oauth2 client secret: %w", err)
	}
	out := *auth
	out.Token = token
	out.APIKeyValue = apiKeyValue
	out.OAuth2ClientSecret = clientSecret
	return &out, nil
}
