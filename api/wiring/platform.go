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
	iamoauth "github.com/byteBuilderX/stratum/internal/iam/infrastructure/oauth"
	iampersistence "github.com/byteBuilderX/stratum/internal/iam/infrastructure/persistence"
	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/infrastructure"
	pkgcrypto "github.com/byteBuilderX/stratum/pkg/crypto"
	"github.com/byteBuilderX/stratum/pkg/observability"
)

// Platform groups cross-cutting application services that other contexts
// (skill, knowledge, agent, iam) depend on: auth (JWT, GitHub OAuth,
// token store, onboarding), the per-tenant LLM gateway cache, the AES key
// derived from the JWT private key, and the shared metrics provider.
//
// Fields are nil when their preconditions are not met (e.g. JWTService
// nil if GitHub OAuth is not configured or the PEM cannot be parsed),
// matching the degrade-rather-than-panic behavior in api/router.go.
type Platform struct {
	JWTService        *application.JWTService
	GitHubClient      *iamoauth.GitHubClient
	TokenStore        *iampersistence.TokenStore
	OnboardSvc        *application.OnboardService
	SchemaProvisioner *iampersistence.AdminTenantRepo
	GatewayCache      *llmgateway.TenantGatewayCache
	AESKey            [32]byte
	Metrics           *observability.PrometheusMetrics
}

func (c *Container) buildPlatform(_ context.Context) error {
	p := &Platform{
		AESKey:       pkgcrypto.DeriveAESKey(c.Config.JWTPrivateKeyPEM),
		GatewayCache: llmgateway.NewTenantGatewayCache(),
		Metrics:      c.LLMGateway.Metrics,
	}

	production := os.Getenv("APP_ENV") == "production"
	if production {
		if c.Config.GitHubClientID == "" || c.Config.GitHubClientSecret == "" {
			return fmt.Errorf("production auth config: GitHub OAuth credentials are required")
		}
		if _, err := parseRSAPrivateKey(c.Config.JWTPrivateKeyPEM); err != nil {
			return fmt.Errorf("production auth config: %w", err)
		}
	}

	if c.Config.GitHubClientID != "" {
		key, err := parseRSAPrivateKey(c.Config.JWTPrivateKeyPEM)
		if err != nil {
			c.Logger.Warn("JWT private key parse failed, auth routes disabled", zap.Error(err))
		} else {
			p.JWTService = application.NewJWTService(key)
			p.GitHubClient = iamoauth.NewGitHubClient(c.Config.GitHubClientID, c.Config.GitHubClientSecret, "", "")
			if c.Storage != nil && c.Storage.PG != nil {
				db := c.Storage.PG.DB()
				if c.Storage.Redis != nil {
					p.TokenStore = iampersistence.NewTokenStore(db, c.Storage.Redis.Client())
				}
				p.OnboardSvc = application.NewOnboardService(iampersistence.NewOnboardRepo(db))
				p.SchemaProvisioner = iampersistence.NewAdminTenantRepo(db)
			}
		}
	}

	c.Platform = p
	return nil
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
