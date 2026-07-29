package token

import (
	"crypto/rsa"
	"errors"
	"fmt"
	"time"

	"github.com/byteBuilderX/stratum/pkg/platformmcp"
	"github.com/golang-jwt/jwt/v5"
)

type DelegationTokenService struct {
	privateKey *rsa.PrivateKey
	publicKey  *rsa.PublicKey
}

func NewDelegationTokenService(key *rsa.PrivateKey) *DelegationTokenService {
	return &DelegationTokenService{privateKey: key, publicKey: &key.PublicKey}
}

func (s *DelegationTokenService) SignInvocation(
	claims platformmcp.InvocationClaims,
	ttl time.Duration,
) (string, error) {
	if err := validateTTL(ttl); err != nil {
		return "", err
	}
	now := time.Now().UTC()
	claims.TokenUse = platformmcp.TokenUseInvocation
	claims.Issuer = platformmcp.InvocationIssuer
	claims.Audience = jwt.ClaimStrings{platformmcp.InvocationAudience}
	claims.IssuedAt = jwt.NewNumericDate(now)
	claims.ExpiresAt = jwt.NewNumericDate(now.Add(ttl))
	if err := validateInvocationClaims(&claims); err != nil {
		return "", err
	}
	return signDelegationClaims(s.privateKey, claims)
}

func (s *DelegationTokenService) VerifyInvocation(raw string) (*platformmcp.InvocationClaims, error) {
	claims := &platformmcp.InvocationClaims{}
	if err := s.parse(raw, claims, platformmcp.InvocationIssuer, platformmcp.InvocationAudience); err != nil {
		return nil, fmt.Errorf("verify invocation token: %w", err)
	}
	if err := validateInvocationClaims(claims); err != nil {
		return nil, err
	}
	return claims, nil
}

func (s *DelegationTokenService) SignAPIDelegation(
	claims platformmcp.APIDelegationClaims,
	ttl time.Duration,
) (string, error) {
	if err := validateTTL(ttl); err != nil {
		return "", err
	}
	now := time.Now().UTC()
	claims.TokenUse = platformmcp.TokenUseAPIDelegation
	claims.Issuer = platformmcp.APIDelegationIssuer
	claims.Audience = jwt.ClaimStrings{platformmcp.APIDelegationAudience}
	claims.IssuedAt = jwt.NewNumericDate(now)
	claims.ExpiresAt = jwt.NewNumericDate(now.Add(ttl))
	if err := validateAPIDelegationClaims(&claims); err != nil {
		return "", err
	}
	return signDelegationClaims(s.privateKey, claims)
}

func (s *DelegationTokenService) VerifyAPIDelegation(raw string) (*platformmcp.APIDelegationClaims, error) {
	claims := &platformmcp.APIDelegationClaims{}
	if err := s.parse(raw, claims, platformmcp.APIDelegationIssuer, platformmcp.APIDelegationAudience); err != nil {
		return nil, fmt.Errorf("verify API delegation token: %w", err)
	}
	if err := validateAPIDelegationClaims(claims); err != nil {
		return nil, err
	}
	return claims, nil
}

func (s *DelegationTokenService) parse(raw string, claims jwt.Claims, issuer, audience string) error {
	token, err := jwt.ParseWithClaims(raw, claims, func(token *jwt.Token) (any, error) {
		return s.publicKey, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}), jwt.WithIssuer(issuer),
		jwt.WithAudience(audience), jwt.WithExpirationRequired())
	if err != nil {
		return err
	}
	if !token.Valid {
		return errors.New("invalid token")
	}
	return nil
}

func signDelegationClaims(key *rsa.PrivateKey, claims jwt.Claims) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := token.SignedString(key)
	if err != nil {
		return "", fmt.Errorf("sign delegation token: %w", err)
	}
	return signed, nil
}

func validateTTL(ttl time.Duration) error {
	if ttl <= 0 {
		return errors.New("delegation token TTL must be positive")
	}
	return nil
}

func validateInvocationClaims(claims *platformmcp.InvocationClaims) error {
	if !matchesTokenProfile(claims.RegisteredClaims, platformmcp.InvocationIssuer, platformmcp.InvocationAudience) {
		return errors.New("invalid invocation token profile")
	}
	if claims.TokenUse != platformmcp.TokenUseInvocation {
		return errors.New("invalid invocation token use")
	}
	if claims.TenantID == "" || claims.UserID == "" || claims.AgentID == "" || claims.ServerID == "" ||
		claims.ToolName == "" || claims.ExecutionID == "" || claims.ID == "" {
		return errors.New("invocation token missing required claims")
	}
	return nil
}

func validateAPIDelegationClaims(claims *platformmcp.APIDelegationClaims) error {
	if !matchesTokenProfile(
		claims.RegisteredClaims,
		platformmcp.APIDelegationIssuer,
		platformmcp.APIDelegationAudience,
	) {
		return errors.New("invalid API delegation token profile")
	}
	if claims.TokenUse != platformmcp.TokenUseAPIDelegation {
		return errors.New("invalid API delegation token use")
	}
	if claims.TenantID == "" || claims.AgentID == "" || claims.ServerID == "" || claims.ToolName == "" ||
		claims.ExecutionID == "" || claims.HTTPMethod == "" || claims.PathTemplate == "" || claims.Role == "" ||
		claims.ID == "" {
		return errors.New("API delegation token missing required claims")
	}
	return nil
}

func matchesTokenProfile(claims jwt.RegisteredClaims, issuer, audience string) bool {
	return claims.Issuer == issuer && len(claims.Audience) == 1 && claims.Audience[0] == audience
}
