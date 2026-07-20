// Package token implements IAM token ports.
package token

import (
	"crypto/rsa"
	"fmt"
	"time"

	"github.com/byteBuilderX/stratum/internal/iam/domain"
	iamport "github.com/byteBuilderX/stratum/internal/iam/domain/port"
	"github.com/golang-jwt/jwt/v5"
)

// jwtAccessClaims is the serialized payload for access JWTs.
type jwtAccessClaims struct {
	TenantID    string            `json:"tid,omitempty"`
	Role        string            `json:"role,omitempty"`
	GlobalRole  string            `json:"global_role,omitempty"`
	SystemRole  domain.SystemRole `json:"system_role,omitempty"`
	AvatarURL   string            `json:"ava,omitempty"`
	GitHubLogin string            `json:"ghl,omitempty"`
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
func (s *JWTService) Sign(c iamport.TokenClaims, ttl time.Duration) (string, error) {
	now := time.Now().UTC()
	claims := jwtAccessClaims{
		TenantID:    c.TenantID,
		Role:        c.Role,
		GlobalRole:  c.GlobalRole,
		SystemRole:  c.SystemRole,
		AvatarURL:   c.AvatarURL,
		GitHubLogin: c.GitHubLogin,
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
func (s *JWTService) Verify(tokenStr string) (*iamport.TokenClaims, error) {
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
	return &iamport.TokenClaims{
		Sub:         c.Subject,
		TenantID:    c.TenantID,
		Role:        c.Role,
		GlobalRole:  c.GlobalRole,
		SystemRole:  c.SystemRole,
		JTI:         c.ID,
		AvatarURL:   c.AvatarURL,
		GitHubLogin: c.GitHubLogin,
	}, nil
}

// SignOnboarding creates a short-lived onboarding JWT (no tenant).
func (s *JWTService) SignOnboarding(ob iamport.OnboardingClaims, ttl time.Duration) (string, error) {
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
func (s *JWTService) VerifyOnboarding(tokenStr string) (*iamport.OnboardingClaims, error) {
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
	return &iamport.OnboardingClaims{
		GitHubID:    c.GitHubID,
		GitHubLogin: c.GitHubLogin,
		AvatarURL:   c.AvatarURL,
	}, nil
}
