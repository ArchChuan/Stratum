package wiring

import (
	"context"
	"fmt"
	"slices"
	"time"

	agentapp "github.com/byteBuilderX/stratum/internal/agent/application"
	iamapp "github.com/byteBuilderX/stratum/internal/iam/application"
	iamport "github.com/byteBuilderX/stratum/internal/iam/domain/port"
	iampersistence "github.com/byteBuilderX/stratum/internal/iam/infrastructure/persistence"
	iamtoken "github.com/byteBuilderX/stratum/internal/iam/infrastructure/token"
	mcpdomain "github.com/byteBuilderX/stratum/internal/mcp/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/platformmcp"
	"github.com/byteBuilderX/stratum/pkg/tenantdb"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type PlatformMCP struct {
	TokenExchange *iamapp.MCPTokenExchange
	Tokens        iamport.DelegationTokenService
}

func (c *Container) buildPlatformMCP(_ context.Context) error {
	if c.Config == nil || !c.Config.InternalAPI.Configured() {
		return nil
	}
	if err := c.validatePlatformMCPDependencies(); err != nil {
		return err
	}
	key, err := parseRSAPrivateKey(c.Config.JWTPrivateKeyPEM)
	if err != nil {
		return fmt.Errorf("platform MCP signing key: %w", err)
	}
	tokens := iamtoken.NewDelegationTokenService(key)
	exchange := iamapp.NewMCPTokenExchange(iamapp.MCPTokenExchange{
		Tokens: tokens,
		Roles: platformMCPRoleAdapter{
			members: c.IAM.TenantService,
		},
		Bindings: platformMCPBindingAdapter{
			agents: c.Agent.Service, servers: c.MCP.Service,
		},
		Replay:    iampersistence.NewMCPTokenReplayRepo(c.dbOrNil()),
		Contracts: platformmcp.NewPhase1Contracts(),
	})
	c.PlatformMCP = &PlatformMCP{TokenExchange: exchange, Tokens: tokens}
	c.MCP.Manager.SetInvocationCredentialProvider(platformMCPInvocationCredentials{tokens: tokens})
	c.MCP.Manager.SetManagedHTTPTransportProvider(platformMCPTransportProvider{files: c.Config.InternalAPI})
	return nil
}

type invocationTokenSigner interface {
	SignInvocation(platformmcp.InvocationClaims, time.Duration) (string, error)
}

type platformMCPInvocationCredentials struct {
	tokens invocationTokenSigner
}

func (p platformMCPInvocationCredentials) Authorization(
	ctx context.Context,
	serverID, toolName string,
) (string, error) {
	invocation, ok := platformmcp.InvocationContextFrom(ctx)
	if !ok || !validPlatformMCPInvocation(invocation, serverID, toolName) || p.tokens == nil {
		return "", fmt.Errorf("issue Platform MCP invocation token: execution identity unavailable")
	}
	token, err := p.tokens.SignInvocation(platformmcp.InvocationClaims{
		TenantID: invocation.TenantID, UserID: invocation.UserID, AgentID: invocation.AgentID,
		ServerID: serverID, ToolName: toolName, ExecutionID: invocation.ExecutionID,
		ApprovalID:       invocation.ApprovalID,
		RegisteredClaims: jwt.RegisteredClaims{ID: uuid.NewString()},
	}, constants.PlatformMCPInvocationTokenTTL)
	if err != nil {
		return "", fmt.Errorf("sign Platform MCP invocation token: %w", err)
	}
	return "Bearer " + token, nil
}

func validPlatformMCPInvocation(invocation platformmcp.InvocationContext, serverID, toolName string) bool {
	return invocation.TenantID != "" && invocation.UserID != "" && invocation.AgentID != "" &&
		invocation.ExecutionID != "" && serverID == platformmcp.SystemServerID &&
		slices.Contains(platformmcp.Phase1ToolNames, toolName)
}

func (c *Container) validatePlatformMCPDependencies() error {
	if c.dbOrNil() == nil {
		return fmt.Errorf("platform MCP database is not configured")
	}
	if c.IAM == nil || c.IAM.TenantService == nil {
		return fmt.Errorf("platform MCP IAM service is not configured")
	}
	if c.Agent == nil || c.Agent.Service == nil {
		return fmt.Errorf("platform MCP Agent service is not configured")
	}
	if c.MCP == nil || c.MCP.Service == nil {
		return fmt.Errorf("platform MCP service is not configured")
	}
	return nil
}

type platformMCPAgentReader interface {
	Get(context.Context, string) (agentapp.AgentDTO, error)
}

type platformMCPServerReader interface {
	GetServerConfig(context.Context, string) (*mcpdomain.ServerConfig, error)
}

type platformMCPBindingAdapter struct {
	agents  platformMCPAgentReader
	servers platformMCPServerReader
}

func (a platformMCPBindingAdapter) ReadPlatformMCPBinding(
	ctx context.Context,
	tenantID, agentID, serverID, toolName string,
) (iamport.PlatformMCPBinding, error) {
	if a.agents == nil || a.servers == nil {
		return iamport.PlatformMCPBinding{}, fmt.Errorf("read platform MCP binding: resource services unavailable")
	}
	tenantCtx := platformMCPTenantContext(ctx, tenantID)
	agent, err := a.agents.Get(tenantCtx, agentID)
	if err != nil {
		return iamport.PlatformMCPBinding{}, fmt.Errorf("read platform MCP agent: %w", err)
	}
	server, err := a.servers.GetServerConfig(tenantCtx, serverID)
	if err != nil {
		return iamport.PlatformMCPBinding{}, fmt.Errorf("read platform MCP server: %w", err)
	}
	if server == nil {
		return iamport.PlatformMCPBinding{}, fmt.Errorf("read platform MCP server: empty result")
	}
	return iamport.PlatformMCPBinding{
		AgentSystemKey:       agent.SystemKey,
		ServerSystemKey:      server.SystemKey,
		ServerManagementMode: server.ManagementMode,
		Bound:                slices.Contains(agent.MCPToolIDs, platformMCPToolID(serverID, toolName)),
	}, nil
}

type platformMCPRoleAdapter struct {
	members tenantMemberRoleService
}

func (a platformMCPRoleAdapter) CurrentRole(
	ctx context.Context,
	tenantID, userID string,
) (string, error) {
	if a.members == nil {
		return "", fmt.Errorf("resolve platform MCP role: member service unavailable")
	}
	role, err := a.members.GetMemberRole(ctx, tenantID, userID)
	if err != nil {
		return "", fmt.Errorf("resolve platform MCP role: %w", err)
	}
	return role, nil
}

func platformMCPTenantContext(ctx context.Context, tenantID string) context.Context {
	return tenantdb.WithTenant(ctx, &tenantdb.TenantContext{
		TenantID: tenantID,
		Role:     tenantdb.RoleTenantAdmin,
	})
}

func platformMCPToolID(serverID, toolName string) string {
	return fmt.Sprintf("mcp:%s:%s", serverID, toolName)
}
