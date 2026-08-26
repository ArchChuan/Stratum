package wiring

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"strings"

	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/iam/application"
	iamport "github.com/byteBuilderX/stratum/internal/iam/domain/port"
	iamoauth "github.com/byteBuilderX/stratum/internal/iam/infrastructure/oauth"
	iampersistence "github.com/byteBuilderX/stratum/internal/iam/infrastructure/persistence"
	iamtoken "github.com/byteBuilderX/stratum/internal/iam/infrastructure/token"
	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	platformapp "github.com/byteBuilderX/stratum/internal/platform/application"
	platformpersistence "github.com/byteBuilderX/stratum/internal/platform/infrastructure/persistence"
	pkgcrypto "github.com/byteBuilderX/stratum/pkg/crypto"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/byteBuilderX/stratum/pkg/storage/filestore"
)

// Platform groups cross-cutting application services that other contexts
// (skill, knowledge, agent, iam) depend on: auth (JWT, GitHub OAuth,
// token store, onboarding), the per-tenant model registry, the at-rest
// data encryption key (ResolveDataKey: DATA_ENCRYPTION_KEY 优先，回退
// JWT 私钥派生以兼容存量密文), and the shared metrics provider.
//
// Fields are nil when their preconditions are not met (e.g. JWTService
// nil if GitHub OAuth is not configured or the PEM cannot be parsed),
// matching the degrade-rather-than-panic behavior in api/router.go.
type Platform struct {
	JWTService         iamport.TokenService
	GitHubClient       *iamoauth.GitHubClient
	TokenStore         *iampersistence.TokenStore
	OAuthExchangeStore *iampersistence.OAuthExchangeStore
	OnboardSvc         *application.OnboardService
	SchemaProvisioner  iamport.TenantSchemaProvisioner
	ModelRegistry      *llmgateway.ModelRegistry
	AvatarStore        *filestore.AvatarStore
	AESKey             [32]byte
	Metrics            *observability.PrometheusMetrics
	DashboardService   *platformapp.DashboardService
}

func (c *Container) buildPlatform(_ context.Context) error {
	// at-rest 密钥独立于 JWT 签名密钥；两者皆空时 fail closed，禁止以
	// sha256("") 公开常量密钥加密 OAuth exchange secret。错误必须先于
	// 任何使用 AESKey 的路径（OAuthExchangeStore 构造）。
	aesKey, err := pkgcrypto.ResolveDataKey(c.Config.DataEncryptionKey, c.Config.JWTPrivateKeyPEM)
	if err != nil {
		return fmt.Errorf("build platform: %w", err)
	}
	p := &Platform{
		AESKey:  aesKey,
		Metrics: c.LLMGateway.Metrics,
	}
	if c.LLMGateway != nil {
		p.ModelRegistry = c.LLMGateway.Registry
	}
	if db := c.dbOrNil(); db != nil {
		p.DashboardService = platformapp.NewDashboardService(platformpersistence.NewDashboardRepository(db))
	}

	c.initAvatarStore(p)

	if err := c.buildPlatformAuth(p); err != nil {
		return err
	}

	c.Platform = p
	return nil
}

// buildPlatformAuth 装配 auth 相关字段：生产环境强制要求 GitHub OAuth 凭据
// 与可解析的 JWT 私钥（fail closed）；GitHubClientID 未配置或私钥解析失败时
// 相应字段保持 nil（auth routes disabled），与 api/router.go 的降级行为一致。
// 提取自 buildPlatform 以收敛圈复杂度。
func (c *Container) buildPlatformAuth(p *Platform) error {
	production := os.Getenv("APP_ENV") == "production"
	if production {
		if c.Config.GitHubClientID == "" || c.Config.GitHubClientSecret == "" {
			return fmt.Errorf("production auth config: GitHub OAuth credentials are required")
		}
		if _, err := parseRSAPrivateKey(c.Config.JWTPrivateKeyPEM); err != nil {
			return fmt.Errorf("production auth config: %w", err)
		}
	}

	if c.Config.GitHubClientID == "" {
		return nil
	}
	key, err := parseRSAPrivateKey(c.Config.JWTPrivateKeyPEM)
	if err != nil {
		c.Logger.Warn("JWT private key parse failed, auth routes disabled", zap.Error(err))
		return nil
	}
	p.JWTService = iamtoken.NewJWTService(key)
	p.GitHubClient = iamoauth.NewGitHubClient(
		c.Config.GitHubClientID,
		c.Config.GitHubClientSecret,
		c.Config.GitHubTokenURL,
		c.Config.GitHubUserURL,
	)
	if c.Storage != nil && c.Storage.PG != nil {
		db := c.Storage.PG.DB()
		if c.Storage.Redis != nil {
			p.TokenStore = iampersistence.NewTokenStore(db, c.Storage.Redis.Client())
		}
		p.OnboardSvc = application.NewOnboardService(iampersistence.NewOnboardRepo(db))
		p.OAuthExchangeStore = iampersistence.NewOAuthExchangeStore(db, p.AESKey)
		// Decorating the provisioner seeds the built-in knowledge workspace for
		// every auth-path tenant (register/guest/tenant). The seed runs
		// asynchronously (nil queue-retry budget) so registration is never
		// blocked by document embedding.
		p.SchemaProvisioner = seedAfterProvision{
			base: iampersistence.NewAdminTenantRepo(db),
			seedFn: func(ctx context.Context, tenantID string) {
				c.syncBuiltinDocsForTenant(ctx, tenantID, nil)
			},
		}
	}
	return nil
}

func (c *Container) initAvatarStore(p *Platform) {
	if c.Config.AvatarDir == "" {
		return
	}
	store, err := filestore.NewAvatarStore(c.Config.AvatarDir)
	if err != nil {
		c.Logger.Warn("avatar store unavailable, avatar upload disabled", zap.Error(err))
		return
	}
	p.AvatarStore = store
}

// parseRSAPrivateKey decodes a PEM-encoded RSA private key. It accepts both
// PKCS#1 ("RSA PRIVATE KEY") and PKCS#8 ("PRIVATE KEY") formats because
// deployment secrets commonly use either OpenSSL output.
func parseRSAPrivateKey(pemStr string) (*rsa.PrivateKey, error) {
	if pemStr == "" {
		return nil, fmt.Errorf("JWT_PRIVATE_KEY_PEM is empty")
	}
	pemStr = strings.ReplaceAll(pemStr, `\n`, "\n")
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse RSA key: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("parse RSA key: PKCS#8 key is %T, not *rsa.PrivateKey", parsed)
	}
	return key, nil
}
