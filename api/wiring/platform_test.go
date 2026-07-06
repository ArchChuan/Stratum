package wiring

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"

	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/config"
	"github.com/byteBuilderX/stratum/pkg/observability"
)

func TestBuildPlatformRequiresAuthConfigInProduction(t *testing.T) {
	t.Setenv("APP_ENV", "production")

	c := &Container{
		Config: &config.Config{},
		Logger: zap.NewNop(),
		LLMGateway: &LLMGateway{
			Metrics: observability.NewPrometheusMetrics(zap.NewNop()),
		},
	}

	err := c.buildPlatform(context.Background())
	if err == nil {
		t.Fatal("expected production auth config error, got nil")
	}
}

func TestParseRSAPrivateKeyAcceptsPKCS8(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("marshal pkcs8: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})

	parsed, err := parseRSAPrivateKey(string(pemBytes))
	if err != nil {
		t.Fatalf("parse pkcs8 key: %v", err)
	}
	if parsed.N.Cmp(key.N) != 0 {
		t.Fatal("parsed key modulus does not match original")
	}
}
