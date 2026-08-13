package wiring

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"time"

	"go.uber.org/zap"

	agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
	mcpapp "github.com/byteBuilderX/stratum/internal/mcp/application"
	mcpport "github.com/byteBuilderX/stratum/internal/mcp/domain/port"
	mcp "github.com/byteBuilderX/stratum/internal/mcp/infrastructure"
	pkgcrypto "github.com/byteBuilderX/stratum/pkg/crypto"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
)

var _ mcpport.RevisionClientManager = (*mcp.ClientManager)(nil)

// MCP holds the Model-Context-Protocol client manager and tool
// registry exposed to agents. The manager owns long-lived
// per-tenant client connections and is restored from DB on startup.
type MCP struct {
	Manager           *mcp.ClientManager
	Registry          *mcp.MCPToolRegistry
	Service           *mcpapp.MCPService
	AgentToolProvider agentport.MCPToolProvider
}

type mcpClientResolver interface {
	GetClient(ctx context.Context, serverID string) mcp.MCPClient
}

type mcpRuntimeRevision struct {
	Config       *mcp.MCPServerConfig
	EnabledTools []string
	Timeout      time.Duration
	MaxRetries   int
}

type mcpRuntimeRevisionLoader interface {
	LoadMCPRuntimeRevision(
		ctx context.Context, tenantID, serverID, revisionID string,
	) (mcpRuntimeRevision, error)
}

type agentMCPExecutor struct {
	clients         mcpClientResolver
	revisionRuntime mcpport.RevisionClientManager
	revisions       mcpRuntimeRevisionLoader
}

func (e agentMCPExecutor) ExecuteMCPTool(
	ctx context.Context, serverID, toolName string, input map[string]any,
) (agentport.MCPToolResult, error) {
	if e.clients == nil {
		return agentport.MCPToolResult{}, &agentport.MCPToolExecutionError{
			Outcome: agentport.ToolExecutionOutcomeNotSent,
			Err:     fmt.Errorf("MCP client resolver unavailable"),
		}
	}
	client := e.clients.GetClient(ctx, serverID)
	if client == nil {
		return agentport.MCPToolResult{}, &agentport.MCPToolExecutionError{
			Outcome: agentport.ToolExecutionOutcomeNotSent,
			Err:     fmt.Errorf("MCP client not found: %s", serverID),
		}
	}
	output, err := client.CallTool(ctx, toolName, input)
	if err != nil {
		return agentport.MCPToolResult{}, &agentport.MCPToolExecutionError{
			Outcome: agentport.ToolExecutionOutcomeUnknown,
			Err:     err,
		}
	}
	return normalizeMCPToolResult(output)
}

func (e agentMCPExecutor) ExecuteMCPToolRevision(
	ctx context.Context,
	serverID, toolName, revisionID string,
	risk agentport.ToolRiskLevel,
	input map[string]any,
) (agentport.MCPToolResult, error) {
	tenant, ok := postgres.FromContext(ctx)
	if !ok || tenant == nil || tenant.TenantID == "" || e.revisions == nil || e.revisionRuntime == nil {
		return agentport.MCPToolResult{}, &agentport.MCPToolExecutionError{
			Outcome: agentport.ToolExecutionOutcomeNotSent,
			Err:     fmt.Errorf("MCP revision runtime unavailable"),
		}
	}
	revision, err := e.revisions.LoadMCPRuntimeRevision(ctx, tenant.TenantID, serverID, revisionID)
	if err != nil {
		return agentport.MCPToolResult{}, &agentport.MCPToolExecutionError{
			Outcome: agentport.ToolExecutionOutcomeNotSent, Err: err,
		}
	}
	if !slices.Contains(revision.EnabledTools, toolName) {
		return agentport.MCPToolResult{}, &agentport.MCPToolExecutionError{
			Outcome: agentport.ToolExecutionOutcomeNotSent,
			Err:     fmt.Errorf("MCP tool disabled by assigned revision"),
		}
	}
	attempts := 1
	if risk == agentport.ToolRiskRead {
		attempts += revision.MaxRetries
	}
	for attempt := 0; attempt < attempts; attempt++ {
		callCtx, cancel := context.WithTimeout(ctx, revision.Timeout)
		output, callErr := e.revisionRuntime.CallToolWithConfig(callCtx, revision.Config, toolName, input)
		cancel()
		if callErr == nil {
			return normalizeMCPToolResult(output)
		}
		if attempt+1 == attempts {
			return agentport.MCPToolResult{}, &agentport.MCPToolExecutionError{
				Outcome: agentport.ToolExecutionOutcomeUnknown, Err: callErr,
			}
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return agentport.MCPToolResult{}, &agentport.MCPToolExecutionError{
				Outcome: agentport.ToolExecutionOutcomeNotSent, Err: ctx.Err(),
			}
		case <-timer.C:
		}
	}
	return agentport.MCPToolResult{}, &agentport.MCPToolExecutionError{
		Outcome: agentport.ToolExecutionOutcomeUnknown, Err: fmt.Errorf("MCP revision execution exhausted"),
	}
}

func normalizeMCPToolResult(output any) (agentport.MCPToolResult, error) {
	if result, ok := output.(agentport.MCPToolResult); ok {
		return result, nil
	}
	raw, err := json.Marshal(output)
	if err != nil {
		return agentport.MCPToolResult{}, fmt.Errorf("decode MCP tool result: unsupported result")
	}
	var result agentport.MCPToolResult
	if err := json.Unmarshal(raw, &result); err == nil &&
		(len(result.Content) > 0 || result.StructuredContent != nil || result.IsError) {
		return result, nil
	}
	if object, ok := output.(map[string]any); ok {
		return agentport.MCPToolResult{StructuredContent: object}, nil
	}
	return agentport.MCPToolResult{
		Content: []agentport.MCPContent{{Type: "text", Text: fmt.Sprint(output)}},
	}, nil
}

type agentMCPPolicyResolver struct{ service *mcpapp.MCPService }

type mcpAgentToolAdapter struct{ registry *mcp.MCPToolRegistry }

func (a mcpAgentToolAdapter) ToolsForServer(_ context.Context, tenantID, serverID string) []agentport.ToolDefinition {
	catalog := a.registry.GetCatalogForServer(tenantID, serverID)
	if catalog == nil {
		return nil
	}
	handles := catalog.GetAllTools()
	tools := make([]agentport.ToolDefinition, 0, len(handles))
	for _, handle := range handles {
		tools = append(tools, agentport.ToolDefinition{
			Name:         handle.GetID(),
			Description:  handle.Tool.Description,
			InputSchema:  handle.Tool.InputSchema,
			OutputSchema: handle.Tool.OutputSchema,
			ProviderType: "mcp",
			ProviderID:   serverID,
			ServerID:     serverID,
			CapabilityID: handle.Tool.Name,
			NodeType:     "mcp",
		})
	}
	return tools
}

func (r agentMCPPolicyResolver) ResolveMCPToolRisk(ctx context.Context, _, serverID, toolName string) (agentport.ToolRiskLevel, error) {
	level, err := r.service.GetToolRisk(ctx, serverID, toolName)
	return agentport.ToolRiskLevel(level), err
}

func (c *Container) buildMCP(ctx context.Context) error {
	var db = c.dbOrNil()
	manager := mcp.NewClientManager(c.Logger, nil, db)
	// 注入 mcp_configs 敏感字段（env/headers/auth_config）的 at-rest 加密密钥，
	// 必须在 RestoreFromDB 之前设置，否则启动恢复读到的是密文而无法解密。
	// 密钥材料独立于 JWT 签名密钥；两者皆空时 fail closed，禁止以
	// sha256("") 公开常量密钥加密 MCP secret。
	aesKey, err := pkgcrypto.ResolveDataKey(c.Config.DataEncryptionKey, c.Config.JWTPrivateKeyPEM)
	if err != nil {
		return fmt.Errorf("build mcp: %w", err)
	}
	if err := manager.WithSecretKey(aesKey); err != nil {
		return fmt.Errorf("build mcp: %w", err)
	}
	// e2e/本地验证的 fixture 监听 loopback；生产默认 Strict 拒绝私网目标。
	if c.Config.MCPAllowPrivateTargets {
		if os.Getenv("STRATUM_E2E_MODE") != "true" {
			// 非 e2e 环境下放行私网目标会整体削弱 SSRF 护栏，必须显式告警。
			c.Logger.Error("MCP_ALLOW_PRIVATE_TARGETS=true 且非 STRATUM_E2E_MODE: " +
				"SSRF 护栏被削弱，禁止在生产环境设置")
		}
		manager.WithURLPolicy(mcp.URLPolicyAllowPrivate)
	}
	manager.SetMetrics(c.platformMetrics())
	registry := mcp.NewMCPToolRegistry(manager, c.Logger)
	svc := mcpapp.NewMCPService(
		mcp.ToolRegistryAsPort(registry),
		mcp.ServerManagerAsPort(manager),
		c.Logger,
	)
	if db != nil {
		svc.SetToolPolicyRepo(mcp.NewPgToolPolicyRepo(db))
	}

	if db != nil {
		if err := manager.RestoreFromDB(ctx); err != nil {
			c.Logger.Warn("failed to restore MCP servers from DB", zap.Error(err))
		}
	}

	manager.StartHealthCheck(30 * time.Second)
	manager.StartIdleEviction(0, 0)
	c.shutdown = append(c.shutdown, manager.Stop)

	c.MCP = &MCP{
		Manager:           manager,
		Registry:          registry,
		Service:           svc,
		AgentToolProvider: mcpAgentToolAdapter{registry: registry},
	}
	return nil
}
