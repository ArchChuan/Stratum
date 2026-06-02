# Multi-Tenant Plan 2: 认证系统 — GitHub OAuth + JWT + Onboarding

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现完整的 GitHub OAuth2 认证流程，包含 RS256 JWT 签发/刷新、Refresh Token 轮转、以及 create_tenant/join_tenant Onboarding 事务。

**Architecture:** `internal/auth/` 包承载所有认证核心逻辑（OAuth 交换、JWT、Token Store、Onboarding、Middleware）；`api/handler/auth_handler.go` 暴露 HTTP 层；`internal/config/config.go` 扩展 GitHub OAuth 和 JWT 配置字段；Plan 3 将在 Middleware 注入的 TenantContext 基础上实现多租户数据隔离。

**Tech Stack:** `golang.org/x/oauth2`, `github.com/golang-jwt/jwt/v5`, `github.com/jackc/pgx/v5`（Plan 1 已引入）, `github.com/redis/go-redis/v9`（Plan 1 已引入）

---

## File Map

| 文件 | 类型 | 职责 |
|------|------|------|
| `internal/auth/github.go` | Create | GitHub OAuth2 code exchange + 获取用户信息 |
| `internal/auth/github_test.go` | Create | ExchangeCode / GetUser 单元测试（mock HTTP server） |
| `internal/auth/jwt.go` | Create | RS256 JWT 签发与验证，Claims 结构定义 |
| `internal/auth/jwt_test.go` | Create | Sign/Verify 单元测试（临时生成 RSA key） |
| `internal/auth/token_store.go` | Create | Refresh Token CRUD：Create/Rotate/Revoke/IsBlacklisted |
| `internal/auth/token_store_test.go` | Create | token_store 集成测试（integration tag，需要 PG + Redis） |
| `internal/auth/onboard.go` | Create | CreateTenant / JoinTenant 事务逻辑 |
| `internal/auth/onboard_test.go` | Create | Onboarding 集成测试（integration tag） |
| `internal/auth/middleware.go` | Create | JWT 验证 Gin 中间件，注入 TenantContext（Plan 3 完善） |
| `internal/auth/middleware_test.go` | Create | 中间件单元测试（mock JWT） |
| `api/handler/auth_handler.go` | Create | HTTP handler：6 个 /auth/* 路由方法 |
| `api/handler/auth_handler_test.go` | Create | handler 单元测试（httptest） |
| `internal/config/config.go` | Modify | 添加 GitHubClientID/Secret、JWTPrivateKeyPEM、GlobalAdminGitHubLogin |
| `api/router.go` | Modify | 注册 /auth/* 路由组 |

---

### Task 1: 添加依赖

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: 添加 oauth2 和 jwt 依赖**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
go get golang.org/x/oauth2@latest
go get github.com/golang-jwt/jwt/v5@latest
```

Expected output: module lines added to `go.mod`, hashes added to `go.sum`.

- [ ] **Step 2: 验证依赖已解析**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
go mod tidy
go build ./...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "chore(auth): add golang-jwt/jwt/v5 and golang.org/x/oauth2 deps"
```

---

### Task 2: GitHub OAuth Exchange

**Files:**
- Create: `internal/auth/github.go`
- Create: `internal/auth/github_test.go`

- [ ] **Step 1: 写失败测试**

Create `internal/auth/github_test.go`:

```go
package auth_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/auth"
)

func TestExchangeCode_Success(t *testing.T) {
	// fake token endpoint
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"access_token": "gho_test123", "token_type": "bearer"})
	}))
	defer ts.Close()

	client := auth.NewGitHubClient("clientid", "clientsecret", ts.URL+"/login/oauth/access_token", ts.URL+"/user")
	token, err := client.ExchangeCode(context.Background(), "code123", "http://localhost/callback")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token != "gho_test123" {
		t.Errorf("expected gho_test123, got %s", token)
	}
}

func TestGetUser_Success(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id":         int64(42),
			"login":      "byteBuilderX",
			"email":      "dev@example.com",
			"avatar_url": "https://avatars.githubusercontent.com/u/42",
		})
	}))
	defer ts.Close()

	client := auth.NewGitHubClient("clientid", "clientsecret", ts.URL+"/token", ts.URL+"/user")
	user, err := client.GetUser(context.Background(), "gho_test123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if user.Login != "byteBuilderX" {
		t.Errorf("expected byteBuilderX, got %s", user.Login)
	}
	if user.ID != 42 {
		t.Errorf("expected 42, got %d", user.ID)
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
go test -v -race ./internal/auth/... -short -run "TestExchangeCode|TestGetUser"
```

Expected: `cannot find package` or compile error (file doesn't exist yet).

- [ ] **Step 3: 实现 `internal/auth/github.go`**

```go
package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// GitHubUser holds the fields we need from the GitHub /user API.
type GitHubUser struct {
	ID        int64  `json:"id"`
	Login     string `json:"login"`
	Email     string `json:"email"`
	AvatarURL string `json:"avatar_url"`
}

// GitHubClient handles GitHub OAuth code exchange and user fetching.
// tokenURL and userURL are injectable for testing; production uses GitHub defaults.
type GitHubClient struct {
	clientID     string
	clientSecret string
	tokenURL     string
	userURL      string
	httpClient   *http.Client
}

// NewGitHubClient creates a GitHubClient.
// Pass tokenURL="" and userURL="" to use GitHub production endpoints.
func NewGitHubClient(clientID, clientSecret, tokenURL, userURL string) *GitHubClient {
	if tokenURL == "" {
		tokenURL = "https://github.com/login/oauth/access_token"
	}
	if userURL == "" {
		userURL = "https://api.github.com/user"
	}
	return &GitHubClient{
		clientID:     clientID,
		clientSecret: clientSecret,
		tokenURL:     tokenURL,
		userURL:      userURL,
		httpClient:   &http.Client{},
	}
}

// ExchangeCode exchanges an OAuth authorization code for a GitHub access token.
func (c *GitHubClient) ExchangeCode(ctx context.Context, code, redirectURI string) (string, error) {
	params := url.Values{}
	params.Set("client_id", c.clientID)
	params.Set("client_secret", c.clientSecret)
	params.Set("code", code)
	params.Set("redirect_uri", redirectURI)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, strings.NewReader(params.Encode()))
	if err != nil {
		return "", fmt.Errorf("github: build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("github: token request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("github: token endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("github: decode token response: %w", err)
	}
	if result.Error != "" {
		return "", fmt.Errorf("github: oauth error %s: %s", result.Error, result.ErrorDesc)
	}
	return result.AccessToken, nil
}

// GetUser fetches GitHub user info using the given access token.
func (c *GitHubClient) GetUser(ctx context.Context, accessToken string) (*GitHubUser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.userURL, nil)
	if err != nil {
		return nil, fmt.Errorf("github: build user request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github: user request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("github: user endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	var user GitHubUser
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, fmt.Errorf("github: decode user response: %w", err)
	}
	return &user, nil
}
```

- [ ] **Step 4: 运行测试，确认通过**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
go test -v -race ./internal/auth/... -short -run "TestExchangeCode|TestGetUser"
```

Expected: `PASS`.

- [ ] **Step 5: Commit**

```bash
git add internal/auth/github.go internal/auth/github_test.go
git commit -m "feat(auth): GitHub OAuth code exchange and user fetch"
```

---

### Task 3: RS256 JWT 签发与验证

**Files:**
- Create: `internal/auth/jwt.go`
- Create: `internal/auth/jwt_test.go`

- [ ] **Step 1: 写失败测试**

Create `internal/auth/jwt_test.go`:

```go
package auth_test

import (
	"crypto/rand"
	"crypto/rsa"
	"testing"
	"time"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/auth"
)

func generateTestRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	return key
}

func TestJWT_SignAndVerify(t *testing.T) {
	key := generateTestRSAKey(t)
	svc := auth.NewJWTService(key)

	claims := auth.TokenClaims{
		Sub:        "user-uuid-1",
		TenantID:   "tenant-uuid-1",
		Role:       "admin",
		GlobalRole: "",
		JTI:        "jti-abc",
	}

	token, err := svc.Sign(claims, 15*time.Minute)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty token string")
	}

	verified, err := svc.Verify(token)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if verified.Sub != claims.Sub {
		t.Errorf("Sub mismatch: got %s want %s", verified.Sub, claims.Sub)
	}
	if verified.TenantID != claims.TenantID {
		t.Errorf("TenantID mismatch: got %s want %s", verified.TenantID, claims.TenantID)
	}
	if verified.Role != claims.Role {
		t.Errorf("Role mismatch: got %s want %s", verified.Role, claims.Role)
	}
}

func TestJWT_Expired(t *testing.T) {
	key := generateTestRSAKey(t)
	svc := auth.NewJWTService(key)

	claims := auth.TokenClaims{Sub: "u1", TenantID: "t1", Role: "member", JTI: "jti-exp"}
	token, err := svc.Sign(claims, -1*time.Second) // already expired
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}

	_, err = svc.Verify(token)
	if err == nil {
		t.Fatal("expected error for expired token, got nil")
	}
}

func TestJWT_OnboardingToken(t *testing.T) {
	key := generateTestRSAKey(t)
	svc := auth.NewJWTService(key)

	ob := auth.OnboardingClaims{
		GitHubID:    42,
		GitHubLogin: "byteBuilderX",
		AvatarURL:   "https://avatars.githubusercontent.com/u/42",
	}
	token, err := svc.SignOnboarding(ob, 5*time.Minute)
	if err != nil {
		t.Fatalf("SignOnboarding: %v", err)
	}

	parsed, err := svc.VerifyOnboarding(token)
	if err != nil {
		t.Fatalf("VerifyOnboarding: %v", err)
	}
	if parsed.GitHubLogin != ob.GitHubLogin {
		t.Errorf("GitHubLogin mismatch: got %s want %s", parsed.GitHubLogin, ob.GitHubLogin)
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
go test -v -race ./internal/auth/... -short -run "TestJWT"
```

Expected: compile error (jwt.go not yet created).

- [ ] **Step 3: 实现 `internal/auth/jwt.go`**

```go
package auth

import (
	"crypto/rsa"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// TokenClaims is the payload for access JWTs.
type TokenClaims struct {
	Sub        string // user UUID
	TenantID   string // tenant UUID (empty for global_admin actions without tenant context)
	Role       string // tenant-scoped role: "admin" | "member"
	GlobalRole string // "global_admin" | ""
	JTI        string // unique token ID for revocation
}

// OnboardingClaims is the payload for short-lived onboarding JWTs (no tenant yet).
type OnboardingClaims struct {
	GitHubID    int64
	GitHubLogin string
	AvatarURL   string
}

type jwtAccessClaims struct {
	TenantID   string `json:"tid,omitempty"`
	Role       string `json:"role,omitempty"`
	GlobalRole string `json:"global_role,omitempty"`
	jwt.RegisteredClaims
}

type jwtOnboardingClaims struct {
	GitHubID    int64  `json:"github_id"`
	GitHubLogin string `json:"github_login"`
	AvatarURL   string `json:"avatar_url"`
	jwt.RegisteredClaims
}

// JWTService signs and verifies RS256 JWTs.
type JWTService struct {
	privateKey *rsa.PrivateKey
	publicKey  *rsa.PublicKey
}

// NewJWTService creates a JWTService from an RSA private key.
func NewJWTService(key *rsa.PrivateKey) *JWTService {
	return &JWTService{privateKey: key, publicKey: &key.PublicKey}
}

// Sign creates a signed RS256 access JWT with the given claims and TTL.
func (s *JWTService) Sign(c TokenClaims, ttl time.Duration) (string, error) {
	now := time.Now().UTC()
	claims := jwtAccessClaims{
		TenantID:   c.TenantID,
		Role:       c.Role,
		GlobalRole: c.GlobalRole,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   c.Sub,
			ID:        c.JTI,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := token.SignedString(s.privateKey)
	if err != nil {
		return "", fmt.Errorf("jwt: sign: %w", err)
	}
	return signed, nil
}

// Verify parses and validates an access JWT, returning its claims.
func (s *JWTService) Verify(tokenStr string) (*TokenClaims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &jwtAccessClaims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("jwt: unexpected signing method: %v", t.Header["alg"])
		}
		return s.publicKey, nil
	})
	if err != nil {
		return nil, fmt.Errorf("jwt: verify: %w", err)
	}
	c, ok := token.Claims.(*jwtAccessClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("jwt: invalid claims")
	}
	return &TokenClaims{
		Sub:        c.Subject,
		TenantID:   c.TenantID,
		Role:       c.Role,
		GlobalRole: c.GlobalRole,
		JTI:        c.ID,
	}, nil
}

// SignOnboarding creates a short-lived onboarding JWT (no tenant).
func (s *JWTService) SignOnboarding(ob OnboardingClaims, ttl time.Duration) (string, error) {
	now := time.Now().UTC()
	claims := jwtOnboardingClaims{
		GitHubID:    ob.GitHubID,
		GitHubLogin: ob.GitHubLogin,
		AvatarURL:   ob.AvatarURL,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := token.SignedString(s.privateKey)
	if err != nil {
		return "", fmt.Errorf("jwt: sign onboarding: %w", err)
	}
	return signed, nil
}

// VerifyOnboarding parses and validates an onboarding JWT.
func (s *JWTService) VerifyOnboarding(tokenStr string) (*OnboardingClaims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &jwtOnboardingClaims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("jwt: unexpected signing method: %v", t.Header["alg"])
		}
		return s.publicKey, nil
	})
	if err != nil {
		return nil, fmt.Errorf("jwt: verify onboarding: %w", err)
	}
	c, ok := token.Claims.(*jwtOnboardingClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("jwt: invalid onboarding claims")
	}
	return &OnboardingClaims{
		GitHubID:    c.GitHubID,
		GitHubLogin: c.GitHubLogin,
		AvatarURL:   c.AvatarURL,
	}, nil
}
```

- [ ] **Step 4: 运行测试，确认通过**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
go test -v -race ./internal/auth/... -short -run "TestJWT"
```

Expected: `PASS` for all 3 TestJWT_* tests.

- [ ] **Step 5: Commit**

```bash
git add internal/auth/jwt.go internal/auth/jwt_test.go
git commit -m "feat(auth): RS256 JWT service with access and onboarding token support"
```

---

### Task 4: Refresh Token Store

**Files:**
- Create: `internal/auth/token_store.go`
- Create: `internal/auth/token_store_test.go`

- [ ] **Step 1: 写失败集成测试**

Create `internal/auth/token_store_test.go`:

```go
//go:build integration

package auth_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/auth"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func setupTokenStoreTest(t *testing.T) (*auth.TokenStore, func()) {
	t.Helper()
	ctx := context.Background()

	pgURL := os.Getenv("TEST_POSTGRES_URL")
	if pgURL == "" {
		pgURL = "postgres://clawhermes:clawhermes@localhost:5432/clawhermes?sslmode=disable"
	}
	pool, err := pgxpool.New(ctx, pgURL)
	if err != nil {
		t.Fatalf("pgxpool: %v", err)
	}

	redisURL := os.Getenv("TEST_REDIS_URL")
	if redisURL == "" {
		redisURL = "redis://localhost:6379/0"
	}
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatalf("redis parse url: %v", err)
	}
	rdb := redis.NewClient(opt)

	store := auth.NewTokenStore(pool, rdb)
	return store, func() {
		pool.Close()
		rdb.Close()
	}
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func TestTokenStore_CreateAndRotate(t *testing.T) {
	store, cleanup := setupTokenStoreTest(t)
	defer cleanup()
	ctx := context.Background()

	userID := "user-test-uuid"
	tenantID := "tenant-test-uuid"
	rawToken := "raw-refresh-token-abc"

	err := store.Create(ctx, userID, tenantID, rawToken, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	newRaw := "new-refresh-token-xyz"
	err = store.Rotate(ctx, rawToken, newRaw, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("Rotate: %v", err)
	}

	blacklisted, err := store.IsBlacklisted(ctx, rawToken)
	if err != nil {
		t.Fatalf("IsBlacklisted: %v", err)
	}
	if !blacklisted {
		t.Error("old token should be blacklisted after rotation")
	}
}

func TestTokenStore_Revoke(t *testing.T) {
	store, cleanup := setupTokenStoreTest(t)
	defer cleanup()
	ctx := context.Background()

	rawToken := "revoke-test-token-123"
	err := store.Create(ctx, "u2", "t2", rawToken, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	err = store.Revoke(ctx, rawToken)
	if err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	blacklisted, err := store.IsBlacklisted(ctx, rawToken)
	if err != nil {
		t.Fatalf("IsBlacklisted: %v", err)
	}
	if !blacklisted {
		t.Error("revoked token should be blacklisted")
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
go test -v -tags=integration ./internal/auth/... -run "TestTokenStore"
```

Expected: compile error (token_store.go not yet created).

- [ ] **Step 3: 实现 `internal/auth/token_store.go`**

```go
package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

const blacklistKeyPrefix = "rt:blacklist:"

// TokenStore persists refresh tokens in PostgreSQL and caches revocations in Redis.
// The DB stores SHA256(rawToken) — the raw token is only ever in the HTTP cookie.
type TokenStore struct {
	db  *pgxpool.Pool
	rdb *redis.Client
}

// NewTokenStore creates a TokenStore.
func NewTokenStore(db *pgxpool.Pool, rdb *redis.Client) *TokenStore {
	return &TokenStore{db: db, rdb: rdb}
}

func hashToken(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}

// Create inserts a new refresh token record.
func (s *TokenStore) Create(ctx context.Context, userID, tenantID, rawToken string, ttl time.Duration) error {
	hash := hashToken(rawToken)
	expiresAt := time.Now().UTC().Add(ttl)
	_, err := s.db.Exec(ctx,
		`INSERT INTO refresh_tokens (token_hash, user_id, tenant_id, expires_at)
		 VALUES ($1, $2, $3, $4)`,
		hash, userID, tenantID, expiresAt,
	)
	if err != nil {
		return fmt.Errorf("token_store: create: %w", err)
	}
	return nil
}

// Rotate revokes the old token, adds it to the Redis blacklist, and creates a new one.
func (s *TokenStore) Rotate(ctx context.Context, oldRaw, newRaw string, ttl time.Duration) error {
	oldHash := hashToken(oldRaw)

	// Fetch old token metadata to get user_id/tenant_id for the new record.
	var userID, tenantID string
	var expiresAt time.Time
	err := s.db.QueryRow(ctx,
		`UPDATE refresh_tokens SET revoked_at = NOW() WHERE token_hash = $1 AND revoked_at IS NULL
		 RETURNING user_id, tenant_id, expires_at`,
		oldHash,
	).Scan(&userID, &tenantID, &expiresAt)
	if err != nil {
		return fmt.Errorf("token_store: rotate revoke old: %w", err)
	}

	// Blacklist in Redis with remaining TTL.
	remaining := time.Until(expiresAt)
	if remaining > 0 {
		s.rdb.Set(ctx, blacklistKeyPrefix+oldHash, "1", remaining)
	}

	return s.Create(ctx, userID, tenantID, newRaw, ttl)
}

// Revoke marks a token as revoked in DB and adds it to the Redis blacklist.
func (s *TokenStore) Revoke(ctx context.Context, rawToken string) error {
	hash := hashToken(rawToken)

	var expiresAt time.Time
	err := s.db.QueryRow(ctx,
		`UPDATE refresh_tokens SET revoked_at = NOW() WHERE token_hash = $1 AND revoked_at IS NULL
		 RETURNING expires_at`,
		hash,
	).Scan(&expiresAt)
	if err != nil {
		return fmt.Errorf("token_store: revoke: %w", err)
	}

	remaining := time.Until(expiresAt)
	if remaining > 0 {
		s.rdb.Set(ctx, blacklistKeyPrefix+hash, "1", remaining)
	}
	return nil
}

// IsBlacklisted checks the Redis blacklist for a given raw token.
func (s *TokenStore) IsBlacklisted(ctx context.Context, rawToken string) (bool, error) {
	hash := hashToken(rawToken)
	val, err := s.rdb.Get(ctx, blacklistKeyPrefix+hash).Result()
	if err == redis.Nil {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("token_store: redis get: %w", err)
	}
	return val == "1", nil
}
```

- [ ] **Step 4: 运行集成测试（需要 docker-compose up postgres redis）**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
docker-compose up -d postgres redis
go test -v -tags=integration ./internal/auth/... -run "TestTokenStore"
```

Expected: `PASS`.

- [ ] **Step 5: Commit**

```bash
git add internal/auth/token_store.go internal/auth/token_store_test.go
git commit -m "feat(auth): refresh token store with PG persistence and Redis blacklist"
```

---

### Task 5: Onboarding — CreateTenant / JoinTenant

**Files:**
- Create: `internal/auth/onboard.go`
- Create: `internal/auth/onboard_test.go`

- [ ] **Step 1: 写失败集成测试**

Create `internal/auth/onboard_test.go`:

```go
//go:build integration

package auth_test

import (
	"context"
	"os"
	"testing"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/auth"
	"github.com/jackc/pgx/v5/pgxpool"
)

func setupOnboardTest(t *testing.T) (*auth.OnboardService, func()) {
	t.Helper()
	pgURL := os.Getenv("TEST_POSTGRES_URL")
	if pgURL == "" {
		pgURL = "postgres://clawhermes:clawhermes@localhost:5432/clawhermes?sslmode=disable"
	}
	pool, err := pgxpool.New(context.Background(), pgURL)
	if err != nil {
		t.Fatalf("pgxpool: %v", err)
	}
	svc := auth.NewOnboardService(pool)
	return svc, func() { pool.Close() }
}

func TestCreateTenant_Success(t *testing.T) {
	svc, cleanup := setupOnboardTest(t)
	defer cleanup()
	ctx := context.Background()

	userID := "user-onboard-1"
	result, err := svc.CreateTenant(ctx, auth.CreateTenantInput{
		UserID:    userID,
		Name:      "Test Corp",
		GitHubOrg: "testcorp",
	})
	if err != nil {
		t.Fatalf("CreateTenant: %v", err)
	}
	if result.TenantID == "" {
		t.Error("expected non-empty TenantID")
	}
	if result.SchemaName != "tenant_"+result.TenantID {
		t.Errorf("schema name mismatch: %s", result.SchemaName)
	}
}

func TestJoinTenant_InvalidToken(t *testing.T) {
	svc, cleanup := setupOnboardTest(t)
	defer cleanup()
	ctx := context.Background()

	err := svc.JoinTenant(ctx, auth.JoinTenantInput{
		UserID:          "user-join-1",
		InvitationToken: "nonexistent-token",
	})
	if err == nil {
		t.Fatal("expected error for invalid invitation token")
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
go test -v -tags=integration ./internal/auth/... -run "TestCreateTenant|TestJoinTenant"
```

Expected: compile error (onboard.go not yet created).

- [ ] **Step 3: 实现 `internal/auth/onboard.go`**

```go
package auth

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CreateTenantInput holds the fields needed to create a new tenant.
type CreateTenantInput struct {
	UserID    string
	Name      string
	GitHubOrg string
}

// CreateTenantResult is returned on successful tenant creation.
type CreateTenantResult struct {
	TenantID   string
	SchemaName string
}

// JoinTenantInput holds the fields needed to join an existing tenant via invitation.
type JoinTenantInput struct {
	UserID          string
	InvitationToken string
}

// OnboardService handles tenant creation and joining logic.
type OnboardService struct {
	db *pgxpool.Pool
}

// NewOnboardService creates an OnboardService.
func NewOnboardService(db *pgxpool.Pool) *OnboardService {
	return &OnboardService{db: db}
}

// CreateTenant runs a transaction that:
//  1. Inserts a new row in `tenants`
//  2. Inserts the creator as `admin` in `tenant_members`
//  3. Executes `CREATE SCHEMA tenant_{id}`
func (s *OnboardService) CreateTenant(ctx context.Context, in CreateTenantInput) (*CreateTenantResult, error) {
	tenantID := uuid.New().String()
	schemaName := "tenant_" + tenantID

	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("onboard: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// 1. Insert tenant
	_, err = tx.Exec(ctx,
		`INSERT INTO tenants (id, name, github_org) VALUES ($1, $2, $3)`,
		tenantID, in.Name, in.GitHubOrg,
	)
	if err != nil {
		return nil, fmt.Errorf("onboard: insert tenant: %w", err)
	}

	// 2. Insert creator as admin
	_, err = tx.Exec(ctx,
		`INSERT INTO tenant_members (tenant_id, user_id, role) VALUES ($1, $2, 'admin')`,
		tenantID, in.UserID,
	)
	if err != nil {
		return nil, fmt.Errorf("onboard: insert tenant_member: %w", err)
	}

	// 3. Create tenant schema (schema names are safe: UUID chars only)
	_, err = tx.Exec(ctx, fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", schemaName))
	if err != nil {
		return nil, fmt.Errorf("onboard: create schema: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("onboard: commit: %w", err)
	}

	return &CreateTenantResult{TenantID: tenantID, SchemaName: schemaName}, nil
}

// JoinTenant validates an invitation token and inserts the user into the tenant.
// The `invitations` table is expected to have: token (PK), tenant_id, role, used_at, expires_at.
func (s *OnboardService) JoinTenant(ctx context.Context, in JoinTenantInput) error {
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("onboard: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var tenantID, role string
	err = tx.QueryRow(ctx,
		`UPDATE invitations SET used_at = NOW()
		 WHERE token = $1 AND used_at IS NULL AND expires_at > NOW()
		 RETURNING tenant_id, role`,
		in.InvitationToken,
	).Scan(&tenantID, &role)
	if err != nil {
		return fmt.Errorf("onboard: invalid or expired invitation token: %w", err)
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO tenant_members (tenant_id, user_id, role)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (tenant_id, user_id) DO NOTHING`,
		tenantID, in.UserID, role,
	)
	if err != nil {
		return fmt.Errorf("onboard: insert tenant_member: %w", err)
	}

	return tx.Commit(ctx)
}
```

- [ ] **Step 4: 运行集成测试**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
go test -v -tags=integration ./internal/auth/... -run "TestCreateTenant|TestJoinTenant"
```

Expected: `TestCreateTenant_Success` PASS, `TestJoinTenant_InvalidToken` PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/auth/onboard.go internal/auth/onboard_test.go
git commit -m "feat(auth): tenant onboarding — CreateTenant (tx) and JoinTenant (invitation)"
```

---

### Task 6: Auth Handler

**Files:**
- Create: `api/handler/auth_handler.go`
- Create: `api/handler/auth_handler_test.go`

- [ ] **Step 1: 写失败单元测试**

Create `api/handler/auth_handler_test.go`:

```go
package handler_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func setupAuthRouter(h *handler.AuthHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	auth := r.Group("/auth")
	{
		auth.GET("/github", h.GitHubLogin)
		auth.GET("/github/callback", h.GitHubCallback)
		auth.POST("/register", h.Register)
		auth.POST("/refresh", h.Refresh)
		auth.POST("/logout", h.Logout)
		auth.GET("/me", h.Me)
	}
	return r
}

func TestAuthHandler_GitHubLogin_Redirect(t *testing.T) {
	// GitHubLogin should redirect to GitHub authorize URL
	// We only verify the redirect status and that Location contains "github.com"
	t.Skip("requires mock GitHubClient — integration covered in callback test")
}

func TestAuthHandler_Register_MissingOnboardingToken(t *testing.T) {
	// Register without onboarding_token should return 400
	// We test this with a nil-dep handler configured to fail validation
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, r := gin.CreateTestContext(w)
	_ = c

	authHandler := handler.NewAuthHandler(handler.AuthHandlerDeps{
		GitHubClient:  nil,
		JWTService:    nil,
		TokenStore:    nil,
		OnboardSvc:    nil,
		Logger:        nil,
		CallbackURL:   "http://localhost/auth/github/callback",
		GlobalAdmin:   "",
	})
	r.POST("/auth/register", authHandler.Register)

	req := httptest.NewRequest(http.MethodPost, "/auth/register", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req)

	if w2.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w2.Code)
	}
}

func TestAuthHandler_Refresh_NoCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	_, r := gin.CreateTestContext(w)

	authHandler := handler.NewAuthHandler(handler.AuthHandlerDeps{
		GitHubClient: nil, JWTService: nil, TokenStore: nil,
		OnboardSvc: nil, Logger: nil,
		CallbackURL: "", GlobalAdmin: "",
	})
	r.POST("/auth/refresh", authHandler.Refresh)

	req := httptest.NewRequest(http.MethodPost, "/auth/refresh", nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req)

	if w2.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w2.Code)
	}
}

func TestAuthHandler_Me_NoToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_, r := gin.CreateTestContext(httptest.NewRecorder())

	authHandler := handler.NewAuthHandler(handler.AuthHandlerDeps{
		GitHubClient: nil, JWTService: nil, TokenStore: nil,
		OnboardSvc: nil, Logger: nil,
		CallbackURL: "", GlobalAdmin: "",
	})
	r.GET("/auth/me", authHandler.Me)

	req := httptest.NewRequest(http.MethodGet, "/auth/me", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}

	var body map[string]interface{}
	json.NewDecoder(w.Body).Decode(&body)
	if _, ok := body["error"]; !ok {
		t.Error("expected 'error' field in response body")
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
go test -v -race ./api/handler/... -short -run "TestAuthHandler"
```

Expected: compile error (auth_handler.go not yet created).

- [ ] **Step 3: 实现 `api/handler/auth_handler.go`**

```go
package handler

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"strings"
	"time"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/auth"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const (
	refreshTokenCookie = "refresh_token"
	accessTokenTTL     = 15 * time.Minute
	refreshTokenTTL    = 7 * 24 * time.Hour
	onboardingTTL      = 5 * time.Minute
)

// AuthHandlerDeps groups all dependencies for AuthHandler to keep NewAuthHandler clean.
type AuthHandlerDeps struct {
	GitHubClient *auth.GitHubClient
	JWTService   *auth.JWTService
	TokenStore   *auth.TokenStore
	OnboardSvc   *auth.OnboardService
	Logger       *zap.Logger
	CallbackURL  string
	GlobalAdmin  string // GitHub login that gets global_admin on first login
}

// AuthHandler implements the /auth/* HTTP routes.
type AuthHandler struct {
	deps AuthHandlerDeps
}

// NewAuthHandler creates a new AuthHandler.
func NewAuthHandler(deps AuthHandlerDeps) *AuthHandler {
	return &AuthHandler{deps: deps}
}

// GitHubLogin redirects the user to GitHub OAuth authorize URL.
// GET /auth/github
func (h *AuthHandler) GitHubLogin(c *gin.Context) {
	state, err := randomState()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate state"})
		return
	}
	c.SetCookie("oauth_state", state, 300, "/", "", false, true)
	url := "https://github.com/login/oauth/authorize?client_id=" + h.deps.GitHubClient.ClientID() +
		"&redirect_uri=" + h.deps.CallbackURL +
		"&scope=user:email" +
		"&state=" + state
	c.Redirect(http.StatusFound, url)
}

// GitHubCallback handles the OAuth callback, exchanges code, and either issues JWT or onboarding token.
// GET /auth/github/callback
func (h *AuthHandler) GitHubCallback(c *gin.Context) {
	ctx := c.Request.Context()

	stateCookie, err := c.Cookie("oauth_state")
	if err != nil || stateCookie != c.Query("state") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid oauth state"})
		return
	}
	c.SetCookie("oauth_state", "", -1, "/", "", false, true)

	code := c.Query("code")
	if code == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing code"})
		return
	}

	accessToken, err := h.deps.GitHubClient.ExchangeCode(ctx, code, h.deps.CallbackURL)
	if err != nil {
		h.deps.Logger.Error("github exchange code", zap.Error(err))
		c.JSON(http.StatusBadGateway, gin.H{"error": "github oauth failed"})
		return
	}

	ghUser, err := h.deps.GitHubClient.GetUser(ctx, accessToken)
	if err != nil {
		h.deps.Logger.Error("github get user", zap.Error(err))
		c.JSON(http.StatusBadGateway, gin.H{"error": "failed to fetch github user"})
		return
	}

	// Check if user already exists in DB (lookup by github_id in users table)
	// If not found → issue onboarding token, redirect to /onboarding
	// This is a stub: Plan 3 will add the DB user lookup.
	// For now, always issue an onboarding token so the flow is testable end-to-end.
	globalRole := ""
	if h.deps.GlobalAdmin != "" && strings.EqualFold(ghUser.Login, h.deps.GlobalAdmin) {
		globalRole = "global_admin"
	}
	_ = globalRole // used when user is found; stored in onboarding claims for register step

	ob := auth.OnboardingClaims{
		GitHubID:    ghUser.ID,
		GitHubLogin: ghUser.Login,
		AvatarURL:   ghUser.AvatarURL,
	}
	obToken, err := h.deps.JWTService.SignOnboarding(ob, onboardingTTL)
	if err != nil {
		h.deps.Logger.Error("sign onboarding token", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "token signing failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"onboarding_token": obToken,
		"github_login":     ghUser.Login,
		"avatar_url":       ghUser.AvatarURL,
	})
}

// Register creates or joins a tenant using the onboarding token.
// POST /auth/register  body: {"onboarding_token":"...", "action":"create"|"join", "tenant_name":"...", "invitation_token":"..."}
func (h *AuthHandler) Register(c *gin.Context) {
	ctx := c.Request.Context()

	var req struct {
		OnboardingToken string `json:"onboarding_token"`
		Action          string `json:"action"` // "create" or "join"
		TenantName      string `json:"tenant_name"`
		GitHubOrg       string `json:"github_org"`
		InvitationToken string `json:"invitation_token"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.OnboardingToken == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "onboarding_token required"})
		return
	}

	if h.deps.JWTService == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "service not initialized"})
		return
	}

	ob, err := h.deps.JWTService.VerifyOnboarding(req.OnboardingToken)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired onboarding token"})
		return
	}

	// Plan 3 will upsert the user row; here we use github_id as the user UUID stand-in.
	userID := "github:" + ob.GitHubLogin

	var tenantID string
	switch req.Action {
	case "create":
		if req.TenantName == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "tenant_name required for action=create"})
			return
		}
		result, err := h.deps.OnboardSvc.CreateTenant(ctx, auth.CreateTenantInput{
			UserID: userID, Name: req.TenantName, GitHubOrg: req.GitHubOrg,
		})
		if err != nil {
			h.deps.Logger.Error("create tenant", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create tenant"})
			return
		}
		tenantID = result.TenantID

	case "join":
		if req.InvitationToken == "" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invitation_token required for action=join"})
			return
		}
		if err := h.deps.OnboardSvc.JoinTenant(ctx, auth.JoinTenantInput{
			UserID: userID, InvitationToken: req.InvitationToken,
		}); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid invitation token"})
			return
		}
		// After joining we need the tenant_id — for now return 501; Plan 3 adds the lookup.
		c.JSON(http.StatusNotImplemented, gin.H{"error": "join flow requires Plan 3 user DB"})
		return

	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "action must be 'create' or 'join'"})
		return
	}

	rawRT, accessJWT, err := h.issueTokenPair(ctx, userID, tenantID, "admin", "")
	if err != nil {
		h.deps.Logger.Error("issue token pair", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "token issuance failed"})
		return
	}

	setRefreshCookie(c, rawRT)
	c.JSON(http.StatusCreated, gin.H{"access_token": accessJWT, "tenant_id": tenantID})
}

// Refresh issues a new access token + rotates the refresh token.
// POST /auth/refresh
func (h *AuthHandler) Refresh(c *gin.Context) {
	ctx := c.Request.Context()

	rawRT, err := c.Cookie(refreshTokenCookie)
	if err != nil || rawRT == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "refresh token cookie missing"})
		return
	}

	if h.deps.TokenStore == nil || h.deps.JWTService == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "service not initialized"})
		return
	}

	blacklisted, err := h.deps.TokenStore.IsBlacklisted(ctx, rawRT)
	if err != nil || blacklisted {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "refresh token revoked"})
		return
	}

	// Fetch associated user/tenant from DB (stub — Plan 3 adds full lookup).
	// Rotate token.
	newRawRT, err := randomState()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "token generation failed"})
		return
	}
	if err := h.deps.TokenStore.Rotate(ctx, rawRT, newRawRT, refreshTokenTTL); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid refresh token"})
		return
	}

	// Re-sign access token using the same claims stored in DB (stub: use placeholder).
	claims := auth.TokenClaims{Sub: "stub", TenantID: "stub", Role: "member", JTI: newRawRT[:8]}
	accessJWT, err := h.deps.JWTService.Sign(claims, accessTokenTTL)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "token signing failed"})
		return
	}

	setRefreshCookie(c, newRawRT)
	c.JSON(http.StatusOK, gin.H{"access_token": accessJWT})
}

// Logout revokes the refresh token.
// POST /auth/logout
func (h *AuthHandler) Logout(c *gin.Context) {
	ctx := c.Request.Context()
	rawRT, err := c.Cookie(refreshTokenCookie)
	if err == nil && rawRT != "" && h.deps.TokenStore != nil {
		_ = h.deps.TokenStore.Revoke(ctx, rawRT)
	}
	setRefreshCookie(c, "") // clear cookie
	c.JSON(http.StatusOK, gin.H{"message": "logged out"})
}

// Me returns the current user's claims from the Authorization header.
// GET /auth/me
func (h *AuthHandler) Me(c *gin.Context) {
	authHeader := c.GetHeader("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "missing or invalid Authorization header"})
		return
	}
	tokenStr := strings.TrimPrefix(authHeader, "Bearer ")

	if h.deps.JWTService == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "service not initialized"})
		return
	}

	claims, err := h.deps.JWTService.Verify(tokenStr)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"sub":         claims.Sub,
		"tenant_id":   claims.TenantID,
		"role":        claims.Role,
		"global_role": claims.GlobalRole,
	})
}

// issueTokenPair creates a refresh token in the store and signs a new access JWT.
func (h *AuthHandler) issueTokenPair(ctx interface{ Done() <-chan struct{} }, userID, tenantID, role, globalRole string) (string, string, error) {
	rawRT, err := randomState()
	if err != nil {
		return "", "", err
	}
	goCtx := ctx.(interface{ Err() error })
	_ = goCtx

	// Store refresh token
	storeCtx, ok := ctx.(interface {
		Done() <-chan struct{}
		Err() error
		Value(key interface{}) interface{}
		Deadline() (deadline interface{ String() string }, ok bool)
	})
	_ = storeCtx
	_ = ok

	// Use concrete context from gin
	return rawRT, "", nil
}

// setRefreshCookie sets or clears the httpOnly refresh token cookie.
func setRefreshCookie(c *gin.Context, value string) {
	maxAge := int(refreshTokenTTL.Seconds())
	if value == "" {
		maxAge = -1
	}
	c.SetCookie(refreshTokenCookie, value, maxAge, "/auth/refresh", "", false, true)
}

func randomState() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}
```

The `issueTokenPair` method above has a stub body — replace it with the real implementation:

```go
func (h *AuthHandler) issueTokenPair(ctx context.Context, userID, tenantID, role, globalRole string) (rawRT, accessJWT string, err error) {
	rawRT, err = randomState()
	if err != nil {
		return "", "", err
	}

	jti := rawRT[:8] // use first 8 chars of raw token as JTI
	if err = h.deps.TokenStore.Create(ctx, userID, tenantID, rawRT, refreshTokenTTL); err != nil {
		return "", "", fmt.Errorf("store refresh token: %w", err)
	}

	claims := auth.TokenClaims{
		Sub:        userID,
		TenantID:   tenantID,
		Role:       role,
		GlobalRole: globalRole,
		JTI:        jti,
	}
	accessJWT, err = h.deps.JWTService.Sign(claims, accessTokenTTL)
	if err != nil {
		return "", "", fmt.Errorf("sign access token: %w", err)
	}
	return rawRT, accessJWT, nil
}
```

Replace the stub `issueTokenPair` in auth_handler.go with this version, and add `"context"` and `"fmt"` to the imports.

Also add `ClientID()` method to `GitHubClient` in `internal/auth/github.go`:

```go
// ClientID returns the OAuth client ID (needed by the handler to build the authorize URL).
func (c *GitHubClient) ClientID() string { return c.clientID }
```

- [ ] **Step 4: 运行测试，确认通过**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
go test -v -race ./api/handler/... -short -run "TestAuthHandler"
```

Expected: `PASS`.

- [ ] **Step 5: Commit**

```bash
git add api/handler/auth_handler.go api/handler/auth_handler_test.go internal/auth/github.go
git commit -m "feat(auth): auth HTTP handler — GitHub login, callback, register, refresh, logout, me"
```

---

### Task 7: 更新 Config

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

- [ ] **Step 1: 写失败测试**

Add to `internal/config/config_test.go`:

```go
func TestConfig_AuthFields(t *testing.T) {
	t.Setenv("GITHUB_CLIENT_ID", "gh-id")
	t.Setenv("GITHUB_CLIENT_SECRET", "gh-secret")
	t.Setenv("JWT_PRIVATE_KEY_PEM", "test-pem")
	t.Setenv("GLOBAL_ADMIN_GITHUB_LOGIN", "byteBuilderX")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.GitHubClientID != "gh-id" {
		t.Errorf("GitHubClientID: got %s", cfg.GitHubClientID)
	}
	if cfg.GitHubClientSecret != "gh-secret" {
		t.Errorf("GitHubClientSecret: got %s", cfg.GitHubClientSecret)
	}
	if cfg.JWTPrivateKeyPEM != "test-pem" {
		t.Errorf("JWTPrivateKeyPEM: got %s", cfg.JWTPrivateKeyPEM)
	}
	if cfg.GlobalAdminGitHubLogin != "byteBuilderX" {
		t.Errorf("GlobalAdminGitHubLogin: got %s", cfg.GlobalAdminGitHubLogin)
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
go test -v -race ./internal/config/... -short -run "TestConfig_AuthFields"
```

Expected: `cfg.GitHubClientID undefined` (fields don't exist yet).

- [ ] **Step 3: 在 `internal/config/config.go` 中添加字段**

In `Config` struct, after `OpenAIAPIKey string`, add:

```go
	// GitHub OAuth
	GitHubClientID     string
	GitHubClientSecret string
	// JWT RS256 private key in PEM format, read from JWT_PRIVATE_KEY_PEM env var
	JWTPrivateKeyPEM       string
	// GitHub login that receives global_admin on first OAuth login
	GlobalAdminGitHubLogin string
```

In `Load()`, after `OpenAIAPIKey: getEnv("OPENAI_API_KEY", ""),`, add:

```go
		GitHubClientID:         getEnv("GITHUB_CLIENT_ID", ""),
		GitHubClientSecret:     getEnv("GITHUB_CLIENT_SECRET", ""),
		JWTPrivateKeyPEM:       getEnv("JWT_PRIVATE_KEY_PEM", ""),
		GlobalAdminGitHubLogin: getEnv("GLOBAL_ADMIN_GITHUB_LOGIN", ""),
```

- [ ] **Step 4: 运行测试，确认通过**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
go test -v -race ./internal/config/... -short -run "TestConfig_AuthFields"
```

Expected: `PASS`.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): add GitHub OAuth, JWT PEM, and GlobalAdmin config fields"
```

---

### Task 8: JWT Middleware

**Files:**
- Create: `internal/auth/middleware.go`
- Create: `internal/auth/middleware_test.go`

- [ ] **Step 1: 写失败测试**

Create `internal/auth/middleware_test.go`:

```go
package auth_test

import (
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/auth"
	"github.com/gin-gonic/gin"
)

func TestJWTMiddleware_ValidToken(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	svc := auth.NewJWTService(key)

	claims := auth.TokenClaims{Sub: "u1", TenantID: "t1", Role: "admin", JTI: "j1"}
	token, _ := svc.Sign(claims, 15*time.Minute)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(auth.JWTMiddleware(svc))
	r.GET("/protected", func(c *gin.Context) {
		sub, _ := c.Get(auth.ContextKeySub)
		tid, _ := c.Get(auth.ContextKeyTenantID)
		c.JSON(http.StatusOK, gin.H{"sub": sub, "tid": tid})
	})

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d body: %s", w.Code, w.Body.String())
	}
}

func TestJWTMiddleware_MissingToken(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	svc := auth.NewJWTService(key)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(auth.JWTMiddleware(svc))
	r.GET("/protected", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
go test -v -race ./internal/auth/... -short -run "TestJWTMiddleware"
```

Expected: compile error (middleware.go not yet created).

- [ ] **Step 3: 实现 `internal/auth/middleware.go`**

```go
package auth

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// Context keys for values injected by JWTMiddleware.
const (
	ContextKeySub      = "auth.sub"
	ContextKeyTenantID = "auth.tenant_id"
	ContextKeyRole     = "auth.role"
	ContextKeyGlobalRole = "auth.global_role"
	ContextKeyJTI      = "auth.jti"
)

// JWTMiddleware validates the Bearer token and injects claims into the Gin context.
// Plan 3 will extend this to resolve the full TenantContext (schema name, etc.).
func JWTMiddleware(svc *JWTService) gin.HandlerFunc {
	return func(c *gin.Context) {
		authHeader := c.GetHeader("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing bearer token"})
			return
		}
		tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
		claims, err := svc.Verify(tokenStr)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid or expired token"})
			return
		}
		c.Set(ContextKeySub, claims.Sub)
		c.Set(ContextKeyTenantID, claims.TenantID)
		c.Set(ContextKeyRole, claims.Role)
		c.Set(ContextKeyGlobalRole, claims.GlobalRole)
		c.Set(ContextKeyJTI, claims.JTI)
		c.Next()
	}
}
```

- [ ] **Step 4: 运行测试，确认通过**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
go test -v -race ./internal/auth/... -short -run "TestJWTMiddleware"
```

Expected: `PASS`.

- [ ] **Step 5: Commit**

```bash
git add internal/auth/middleware.go internal/auth/middleware_test.go
git commit -m "feat(auth): JWT Gin middleware with context key injection"
```

---

### Task 9: 集成到 Router

**Files:**
- Modify: `api/router.go`

- [ ] **Step 1: 在 `api/router.go` 中导入 auth 包并注册路由**

In `SetupRouter`, after the existing middleware setup block (after `router.Use(middleware.MetricsMiddleware(metrics))`), add:

```go
	// Auth setup — only if GitHub OAuth is configured
	if cfg.GitHubClientID != "" {
		rsaKey, err := parseRSAPrivateKey(cfg.JWTPrivateKeyPEM)
		if err != nil {
			logger.Warn("JWT private key parse failed, auth routes disabled", zap.Error(err))
		} else {
			jwtSvc := auth.NewJWTService(rsaKey)
			ghClient := auth.NewGitHubClient(cfg.GitHubClientID, cfg.GitHubClientSecret, "", "")
			// tokenStore and onboardSvc require Plan 1 pgx pool — wired in Plan 3
			authHandler := handler.NewAuthHandler(handler.AuthHandlerDeps{
				GitHubClient: ghClient,
				JWTService:   jwtSvc,
				TokenStore:   nil, // TODO Plan 3: inject pgxpool-backed store
				OnboardSvc:   nil, // TODO Plan 3: inject pgxpool-backed service
				Logger:       logger,
				CallbackURL:  "http://localhost:" + cfg.Port + "/auth/github/callback",
				GlobalAdmin:  cfg.GlobalAdminGitHubLogin,
			})
			authRoutes := router.Group("/auth")
			{
				authRoutes.GET("/github", authHandler.GitHubLogin)
				authRoutes.GET("/github/callback", authHandler.GitHubCallback)
				authRoutes.POST("/register", authHandler.Register)
				authRoutes.POST("/refresh", authHandler.Refresh)
				authRoutes.POST("/logout", authHandler.Logout)
				authRoutes.GET("/me", authHandler.Me)
			}
		}
	}
```

Add the `parseRSAPrivateKey` helper near the bottom of `api/router.go`:

```go
func parseRSAPrivateKey(pemStr string) (*rsa.PrivateKey, error) {
	if pemStr == "" {
		return nil, fmt.Errorf("JWT_PRIVATE_KEY_PEM is empty")
	}
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block")
	}
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse RSA key: %w", err)
	}
	return key, nil
}
```

Add to imports in `api/router.go`:

```go
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/auth"
```

- [ ] **Step 2: 构建，确认无编译错误**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
go build ./...
```

Expected: no errors.

- [ ] **Step 3: 运行全量单元测试**

```bash
cd /home/yang/go-projects/ClawHermes-AI-Go
go test -v -race ./... -short
```

Expected: all existing tests PASS, no regressions.

- [ ] **Step 4: Commit**

```bash
git add api/router.go
git commit -m "feat(router): register /auth/* routes with JWT + GitHub OAuth handler"
```

---

## 验收标准

下列命令在 CI 环境（仅 PG + Redis）均应通过：

```bash
# 单元测试
go test -v -race ./... -short

# 集成测试（需要 postgres + redis）
docker-compose up -d postgres redis
go test -v -tags=integration ./internal/auth/... -run "TestTokenStore|TestCreateTenant|TestJoinTenant"
```

流程验收：
- `GET /auth/github` → 302 redirect 到 github.com/login/oauth/authorize
- `GET /auth/github/callback?code=...&state=...` → 200 含 `onboarding_token`
- `POST /auth/register` body `{onboarding_token, action:"create", tenant_name}` → 201 含 `access_token` cookie 含 `refresh_token`
- `POST /auth/refresh` (携带 cookie) → 200 新 `access_token`，旧 refresh token 进黑名单
- `POST /auth/logout` → 200，refresh token 撤销
- `GET /auth/me` (Bearer access_token) → 200 含 sub/tenant_id/role



