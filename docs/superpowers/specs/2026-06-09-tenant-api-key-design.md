# 租户级 LLM API Key 配置设计

**日期**: 2026-06-09
**分支**: feat/tenant-suspend-enforcement
**状态**: 待实现

---

## 背景

当前 LLM Gateway 使用全局环境变量（`QWEN_API_KEY` / `ZHIPU_API_KEY`）初始化，所有租户共享同一套 API key。需要支持每个租户配置自己的 API key，在 agent 执行时动态加载使用。

---

## 范围

- 支持 provider：Qwen、Zhipu（框架可扩展，key 为 provider 名）
- 加密存储：AES-256-GCM，密钥由 `JWTPrivateKeyPEM` SHA-256 派生
- 缓存：5 分钟 TTL 内存缓存，key 更新时主动失效
- 权限：`owner`/`admin` 可读写；`member` 只读（脱敏）
- 降级：租户未配置 → 使用全局 Gateway，行为不变

---

## Section 1：存储层

### settings JSONB 结构

API key 加密后写入 `public.tenants.settings` JSONB（已有列，无需 schema 变更）：

```json
{
  "llm_api_keys": {
    "qwen":  "<base64(nonce + ciphertext)>",
    "zhipu": "<base64(nonce + ciphertext)>"
  }
}
```

### 加密方案

- **算法**：AES-256-GCM（AEAD，认证加密）
- **密钥派生**：`SHA-256([]byte(JWTPrivateKeyPEM))` → 32 字节 AES key
- **nonce**：每次加密随机生成 12 bytes，prepend 到密文前
- **存储格式**：`base64(nonce || ciphertext || tag)`

### 新增文件

`pkg/crypto/aes.go`：

```go
package crypto

import (
    "crypto/aes"
    "crypto/cipher"
    "crypto/rand"
    "crypto/sha256"
    "encoding/base64"
    "fmt"
    "io"
)

func DeriveAESKey(jwtPrivateKeyPEM string) [32]byte {
    return sha256.Sum256([]byte(jwtPrivateKeyPEM))
}

func Encrypt(key [32]byte, plaintext string) (string, error) {
    block, err := aes.NewCipher(key[:])
    if err != nil {
        return "", fmt.Errorf("crypto: new cipher: %w", err)
    }
    gcm, err := cipher.NewGCM(block)
    if err != nil {
        return "", fmt.Errorf("crypto: new gcm: %w", err)
    }
    nonce := make([]byte, gcm.NonceSize())
    if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
        return "", fmt.Errorf("crypto: nonce: %w", err)
    }
    ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
    return base64.StdEncoding.EncodeToString(ciphertext), nil
}

func Decrypt(key [32]byte, encoded string) (string, error) {
    data, err := base64.StdEncoding.DecodeString(encoded)
    if err != nil {
        return "", fmt.Errorf("crypto: base64 decode: %w", err)
    }
    block, err := aes.NewCipher(key[:])
    if err != nil {
        return "", fmt.Errorf("crypto: new cipher: %w", err)
    }
    gcm, err := cipher.NewGCM(block)
    if err != nil {
        return "", fmt.Errorf("crypto: new gcm: %w", err)
    }
    nonceSize := gcm.NonceSize()
    if len(data) < nonceSize {
        return "", fmt.Errorf("crypto: ciphertext too short")
    }
    plaintext, err := gcm.Open(nil, data[:nonceSize], data[nonceSize:], nil)
    if err != nil {
        return "", fmt.Errorf("crypto: decrypt: %w", err)
    }
    return string(plaintext), nil
}
```

---

## Section 2：后端 API 层

### 接口变更

| 接口 | 变更 |
|------|------|
| `GET /tenant/settings` | `llm_api_keys.*` 值脱敏返回：末 4 位可见，如 `sk-****1234`；全空则返回空字符串 |
| `PATCH /tenant/settings` | 检测到 `llm_api_keys.*` 字段时，加密后再写入 JSONB；权限：`owner`/`admin` 可写，`member` 调用返回 403 |

### TenantHandler 改动

`NewTenantHandler` 新增参数 `aesKey [32]byte`：

- `UpdateSettings`：遍历 `req.Settings["llm_api_keys"]`，对每个 provider 值调用 `crypto.Encrypt`，再存入 JSONB
- `GetSettings`：读出后对 `llm_api_keys.*` 调用 `maskAPIKey`（末 4 位可见，其余替换为 `****`）

### maskAPIKey 逻辑

```
len <= 4  → "****"
len > 4   → "****" + last4
```

### 密钥注入

`SetupRouter` 中构造 `TenantHandler` 时，从 `cfg.JWTPrivateKeyPEM` 派生 `aesKey` 并注入。

---

## Section 3：Agent 执行时动态加载 + 5min 缓存

### 新增文件：`internal/llmgateway/tenant_cache.go`

```go
type tenantGatewayCache struct {
    mu      sync.Mutex
    entries map[string]*cacheEntry
}

type cacheEntry struct {
    gateway   *Gateway
    expiresAt time.Time
}

func (c *tenantGatewayCache) Get(tenantID string) (*Gateway, bool)
func (c *tenantGatewayCache) Set(tenantID string, gw *Gateway, ttl time.Duration)
func (c *tenantGatewayCache) Invalidate(tenantID string)
```

### AgentHandler 改动

新增字段：

```go
type AgentHandler struct {
    // 现有字段...
    gatewayCache *llmgateway.TenantGatewayCache
    aesKey       [32]byte
    db           PgxPool
}
```

`ExecuteAgent` 执行流程：

```
1. 从缓存查 tenantID → hit: 用缓存 Gateway
2. miss: 查 DB settings → 解密 llm_api_keys
3. 有 key: 构造租户专属 Gateway（RegisterClient 覆盖）
4. 无 key: 使用全局 h.gateway（降级）
5. 写缓存 TTL=5min
6. 将 Gateway 构造 CapGateway 注入 agent
```

### 缓存失效

`TenantHandler.UpdateSettings` 更新成功后调用 `agentHandler.gatewayCache.Invalidate(tenantID)`，保证 key 更新立即生效（不等 TTL）。

实现方式：`TenantHandler` 持有 `cache *llmgateway.TenantGatewayCache` 引用，`SetupRouter` 构造时注入同一个 cache 实例。

---

## Section 4：前端 API Key 配置界面

### 修改文件：`web/src/pages/tenant/SettingsPage.jsx`

在现有表单下方新增"LLM API Key 配置"Card：

**UI 结构**：

```
┌─ LLM API Key 配置 ──────────────────────────────┐
│ Qwen API Key  [sk-****1234          ] [保存]    │
│ Zhipu API Key [未配置               ] [保存]    │
│                                                  │
│ （member 角色：input disabled，无保存按钮）       │
└──────────────────────────────────────────────────┘
```

**行为细节**：

- 加载时调 `GET /tenant/settings`，将 `settings.llm_api_keys.qwen/zhipu` 填入对应 input（脱敏值）
- input placeholder：`"未配置"`
- 用户修改后点"保存" → `PATCH /tenant/settings` 带 `{ settings: { llm_api_keys: { qwen: newVal } } }`
- 每个 provider 独立保存，互不影响
- `member` 角色（从 `useAuth().user.current_tenant.role` 判断）：input `disabled`，隐藏保存按钮
- 成功：`message.success('API Key 已保存')`
- 失败：`message.error(err.response?.data?.message || '保存失败')`

**API 调用**：复用现有 `updateTenant(values)`，无需新增 services 函数。

---

## 变更文件清单

| 文件 | 变更类型 |
|------|---------|
| `pkg/crypto/aes.go` | 新增 |
| `pkg/crypto/aes_test.go` | 新增 |
| `internal/llmgateway/tenant_cache.go` | 新增 |
| `internal/llmgateway/tenant_cache_test.go` | 新增 |
| `api/handler/tenant_handler.go` | 修改：加密/脱敏/权限 |
| `api/handler/agent_handler.go` | 修改：动态 Gateway 加载 |
| `api/router.go` | 修改：注入 aesKey + cache |
| `web/src/pages/tenant/SettingsPage.jsx` | 修改：新增 API Key 表单 |

---

## 安全注意事项

- `JWTPrivateKeyPEM` 是唯一密钥源，泄露则所有租户 API key 暴露，需确保其通过 Vault/Secrets Manager 管理，禁止入 git
- 日志中禁止打印解密后的 API key 明文
- `maskAPIKey` 在 GetSettings 响应和日志中统一使用
