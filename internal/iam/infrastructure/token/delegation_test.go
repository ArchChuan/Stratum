package token_test

import (
	"testing"
	"time"

	iamtoken "github.com/byteBuilderX/stratum/internal/iam/infrastructure/token"
	"github.com/byteBuilderX/stratum/pkg/platformmcp"
	"github.com/golang-jwt/jwt/v5"
)

func TestDelegationTokenServiceSignAndVerify(t *testing.T) {
	svc := iamtoken.NewDelegationTokenService(generateTestRSAKey(t))
	invocation := validInvocationClaims()
	signed, err := svc.SignInvocation(invocation, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.VerifyInvocation(signed)
	if err != nil {
		t.Fatal(err)
	}
	if got.ToolName != invocation.ToolName || got.TenantID != invocation.TenantID {
		t.Fatalf("invocation claims = %+v", got)
	}

	delegation := validAPIDelegationClaims()
	signed, err = svc.SignAPIDelegation(delegation, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	apiClaims, err := svc.VerifyAPIDelegation(signed)
	if err != nil {
		t.Fatal(err)
	}
	if apiClaims.HTTPMethod != delegation.HTTPMethod || apiClaims.PathTemplate != delegation.PathTemplate {
		t.Fatalf("delegation claims = %+v", apiClaims)
	}
}

func TestDelegationTokenServiceRejectsInvalidInvocationClaims(t *testing.T) {
	key := generateTestRSAKey(t)
	svc := iamtoken.NewDelegationTokenService(key)

	tests := []struct {
		name   string
		mutate func(*platformmcp.InvocationClaims)
	}{
		{name: "issuer", mutate: func(c *platformmcp.InvocationClaims) { c.Issuer = "wrong" }},
		{name: "audience", mutate: func(c *platformmcp.InvocationClaims) { c.Audience = []string{"wrong"} }},
		{name: "multiple audiences", mutate: func(c *platformmcp.InvocationClaims) {
			c.Audience = []string{platformmcp.InvocationAudience, "other"}
		}},
		{name: "token use", mutate: func(c *platformmcp.InvocationClaims) { c.TokenUse = "api_delegation" }},
		{name: "expiry", mutate: func(c *platformmcp.InvocationClaims) { c.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Minute)) }},
		{name: "missing tool", mutate: func(c *platformmcp.InvocationClaims) { c.ToolName = "" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims := validInvocationClaims()
			tt.mutate(&claims)
			signed := signRaw(t, key, claims)
			if _, err := svc.VerifyInvocation(signed); err == nil {
				t.Fatal("VerifyInvocation succeeded")
			}
		})
	}
}

func TestDelegationTokenServiceRejectsWrongAlgorithmAndCrossTokenUse(t *testing.T) {
	key := generateTestRSAKey(t)
	svc := iamtoken.NewDelegationTokenService(key)
	hs := jwt.NewWithClaims(jwt.SigningMethodHS256, validInvocationClaims())
	signedHS, err := hs.SignedString([]byte("not-the-rsa-key"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.VerifyInvocation(signedHS); err == nil {
		t.Fatal("HS256 invocation accepted")
	}

	delegation, err := svc.SignAPIDelegation(validAPIDelegationClaims(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.VerifyInvocation(delegation); err == nil {
		t.Fatal("API delegation token accepted as invocation")
	}
}

func TestDelegationTokenServiceRejectsMissingRoute(t *testing.T) {
	key := generateTestRSAKey(t)
	svc := iamtoken.NewDelegationTokenService(key)
	claims := validAPIDelegationClaims()
	claims.PathTemplate = ""
	signed := signRaw(t, key, claims)
	if _, err := svc.VerifyAPIDelegation(signed); err == nil {
		t.Fatal("delegation without route accepted")
	}
}

func validInvocationClaims() platformmcp.InvocationClaims {
	return platformmcp.InvocationClaims{
		TenantID: "tenant-1", UserID: "user-1", AgentID: platformmcp.SystemAssistantKey,
		ServerID: platformmcp.SystemServerID, ToolName: "stratum_diagnose_tenant", ExecutionID: "exec-1",
		TokenUse: platformmcp.TokenUseInvocation,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: platformmcp.InvocationIssuer, Audience: jwt.ClaimStrings{platformmcp.InvocationAudience},
			ID: "jti-1", IssuedAt: jwt.NewNumericDate(time.Now()), ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
		},
	}
}

func validAPIDelegationClaims() platformmcp.APIDelegationClaims {
	return platformmcp.APIDelegationClaims{
		TenantID: "tenant-1", AgentID: platformmcp.SystemAssistantKey, ServerID: platformmcp.SystemServerID,
		ToolName: "stratum_diagnose_tenant", ExecutionID: "exec-1", HTTPMethod: "POST",
		PathTemplate: "/internal/platform-assistant/diagnose", Role: "admin",
		TokenUse: platformmcp.TokenUseAPIDelegation,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: platformmcp.APIDelegationIssuer, Audience: jwt.ClaimStrings{platformmcp.APIDelegationAudience},
			ID: "jti-2", IssuedAt: jwt.NewNumericDate(time.Now()), ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
		},
	}
}

func signRaw(t *testing.T, key any, claims jwt.Claims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}
