package application

import (
	"context"
	"errors"
	"testing"
	"time"

	iamport "github.com/byteBuilderX/stratum/internal/iam/domain/port"
	"github.com/byteBuilderX/stratum/pkg/platformmcp"
	"github.com/golang-jwt/jwt/v5"
)

func TestMCPTokenExchangeSuccessUsesCurrentRole(t *testing.T) {
	deps := validExchangeDeps()
	deps.roles.role = "admin"
	exchange := NewMCPTokenExchange(deps.asExchange())

	signed, err := exchange.Exchange(t.Context(), MCPTokenExchangeRequest{
		InvocationToken: "invocation", ResourceID: "resource-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if signed != "delegation" || deps.tokens.signed.Role != "admin" {
		t.Fatalf("delegation=%q claims=%+v", signed, deps.tokens.signed)
	}
	if deps.tokens.signed.HTTPMethod != "POST" || deps.tokens.signed.PathTemplate != "/internal/test" {
		t.Fatalf("route claims=%+v", deps.tokens.signed)
	}
}

func TestMCPTokenExchangeRejectsInvalidAuthority(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*exchangeDeps)
		want   error
	}{
		{name: "ordinary agent", mutate: func(d *exchangeDeps) {
			d.bindings.binding.AgentSystemKey = ""
		}, want: ErrPlatformMCPIdentityInvalid},
		{name: "tenant managed server", mutate: func(d *exchangeDeps) {
			d.bindings.binding.ServerManagementMode = platformmcp.ManagementTenant
		}, want: ErrPlatformMCPIdentityInvalid},
		{name: "missing binding", mutate: func(d *exchangeDeps) {
			d.bindings.binding.Bound = false
		}, want: ErrPlatformMCPBindingMissing},
		{name: "downgraded user", mutate: func(d *exchangeDeps) {
			d.roles.role = "member"
		}, want: ErrPlatformMCPRoleInsufficient},
		{name: "wrong tool contract", mutate: func(d *exchangeDeps) {
			d.contracts.contracts = nil
		}, want: ErrPlatformMCPContractInvalid},
		{name: "stale approval", mutate: func(d *exchangeDeps) {
			d.contracts.contracts["tool-1"] = platformmcp.ToolContract{
				Name: "tool-1", Method: "POST", Path: "/internal/test", RequiresApproval: true,
			}
			d.approvals.err = errors.New("stale")
			d.tokens.claims.ApprovalID = "approval-1"
		}, want: ErrPlatformMCPApprovalInvalid},
		{name: "expired token", mutate: func(d *exchangeDeps) {
			d.tokens.verifyErr = errors.New("expired")
		}, want: ErrPlatformMCPInvocationInvalid},
		{name: "duplicate JTI", mutate: func(d *exchangeDeps) {
			d.replay.consumed = false
		}, want: ErrPlatformMCPInvocationReplayed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps := validExchangeDeps()
			tt.mutate(&deps)
			exchange := NewMCPTokenExchange(deps.asExchange())
			_, err := exchange.Exchange(t.Context(), MCPTokenExchangeRequest{
				InvocationToken: "invocation", ResourceID: "resource-1",
			})
			if !errors.Is(err, tt.want) {
				t.Fatalf("error=%v, want %v", err, tt.want)
			}
		})
	}
}

type exchangeDeps struct {
	tokens    *exchangeTokenFake
	roles     *roleResolverFake
	bindings  *bindingReaderFake
	approvals *approvalReaderFake
	replay    *replayStoreFake
	contracts *contractRegistryFake
}

func validExchangeDeps() exchangeDeps {
	return exchangeDeps{
		tokens: &exchangeTokenFake{claims: &platformmcp.InvocationClaims{
			TenantID: "tenant-1", UserID: "user-1", AgentID: platformmcp.SystemAssistantID,
			ServerID: platformmcp.SystemServerID, ToolName: "tool-1", ExecutionID: "exec-1",
			RegisteredClaims: jwt.RegisteredClaims{ID: "jti-1", ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute))},
		}},
		roles: &roleResolverFake{role: "admin"},
		bindings: &bindingReaderFake{binding: iamport.PlatformMCPBinding{
			AgentSystemKey: platformmcp.SystemAssistantKey, ServerSystemKey: platformmcp.SystemServerKey,
			ServerManagementMode: platformmcp.ManagementPlatform, Bound: true,
		}},
		approvals: &approvalReaderFake{},
		replay:    &replayStoreFake{consumed: true},
		contracts: &contractRegistryFake{contracts: map[string]platformmcp.ToolContract{
			"tool-1": {Name: "tool-1", Method: "POST", Path: "/internal/test", MinimumRole: "admin"},
		}},
	}
}

func (d exchangeDeps) asExchange() MCPTokenExchange {
	return MCPTokenExchange{
		Tokens: d.tokens, Roles: d.roles, Bindings: d.bindings, Approvals: d.approvals,
		Replay: d.replay, Contracts: d.contracts,
	}
}

type exchangeTokenFake struct {
	claims    *platformmcp.InvocationClaims
	verifyErr error
	signed    platformmcp.APIDelegationClaims
}

func (f *exchangeTokenFake) VerifyInvocation(string) (*platformmcp.InvocationClaims, error) {
	return f.claims, f.verifyErr
}
func (f *exchangeTokenFake) SignAPIDelegation(c platformmcp.APIDelegationClaims, _ time.Duration) (string, error) {
	f.signed = c
	return "delegation", nil
}

type roleResolverFake struct {
	role string
	err  error
}

func (f *roleResolverFake) CurrentRole(context.Context, string, string) (string, error) {
	return f.role, f.err
}

type bindingReaderFake struct {
	binding iamport.PlatformMCPBinding
	err     error
}

func (f *bindingReaderFake) ReadPlatformMCPBinding(
	context.Context, string, string, string, string,
) (iamport.PlatformMCPBinding, error) {
	return f.binding, f.err
}

type approvalReaderFake struct{ err error }

func (f *approvalReaderFake) ValidatePlatformMCPApproval(
	context.Context, string, string, string, string,
) error {
	return f.err
}

type replayStoreFake struct {
	consumed bool
	err      error
}

func (f *replayStoreFake) ConsumeInvocationJTI(context.Context, string, string, time.Time) (bool, error) {
	return f.consumed, f.err
}

type contractRegistryFake struct {
	contracts map[string]platformmcp.ToolContract
}

func (f *contractRegistryFake) Lookup(name string) (platformmcp.ToolContract, bool) {
	contract, ok := f.contracts[name]
	return contract, ok
}
