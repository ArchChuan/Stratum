package wiring

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
	knowledge "github.com/byteBuilderX/stratum/internal/knowledge/application"
	mcpapp "github.com/byteBuilderX/stratum/internal/mcp/application"
	mcpdomain "github.com/byteBuilderX/stratum/internal/mcp/domain"
	"github.com/byteBuilderX/stratum/pkg/platformknowledge"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
)

// _ 断言保证组合根适配器始终满足 agent 消费方 port(编译期契约)。
var _ agentport.SystemResourceGuard = (*systemResourceGuard)(nil)

// platformSetCacheTTL 控制平台托管资源全集缓存时长。缓存只影响运行时净化(C 层)
// 的"剔除范围",写路径(A1/A2/B)仍全量直查;60s 内新增平台资源最多延迟一轮
// 净化,方向是"少剔除"保守侧,可接受。
const platformSetCacheTTL = 60 * time.Second

// systemResourceGuard 实现 agentport.SystemResourceGuard —— 组合根把 MCP /
// knowledge 上下文适配到 agent 消费方 port,满足 DDD 依赖方向(wiring 是唯一
// 允许同时 import 兄弟 domain 的层)。
//
// guard 语义(风险原则 1,fail closed):
//   - 批量方法带 TTL 缓存:查询失败回退最近成功缓存(陈旧安全,方向是"少剔除");
//     无缓存且查询失败 → 返回错误,禁止默认放行。
//   - 未装配的上下文(c.MCP / c.Knowledge 为 nil)返回空集 + nil —— wiring 策略:
//     能力不存在 → 无平台资源 → 无绑定可校验,放行安全。
//   - IsPlatformManagedMCPServer 单查(写路径):server 不存在 → (false, nil),
//     非 platform → (false, nil);查询失败 → fail closed。
type systemResourceGuard struct {
	mcp *mcpapp.MCPService
	ws  *knowledge.WorkspaceService

	mu     sync.Mutex
	mcpIDs []string
	mcpAt  time.Time
	wsIDs  []string
	wsAt   time.Time
}

func newSystemResourceGuard(mcp *mcpapp.MCPService, ws *knowledge.WorkspaceService) *systemResourceGuard {
	return &systemResourceGuard{mcp: mcp, ws: ws}
}

// mcpServiceOf returns the wired MCP service, or nil when the MCP context was
// built without a database. Guard 对 nil service 返回空集(wiring 策略:能力
// 不存在 → 无平台资源)。
func mcpServiceOf(c *Container) *mcpapp.MCPService {
	if c.MCP == nil {
		return nil
	}
	return c.MCP.Service
}

// IsPlatformManagedMCPServer 单查 server 配置判断平台托管。写路径 A2 用它做
// 逐 tool 挂载校验,不走批量缓存(写路径必须看到最新平台状态)。
func (g *systemResourceGuard) IsPlatformManagedMCPServer(ctx context.Context, tenantID, serverID string) (bool, error) {
	if g.mcp == nil {
		return false, nil
	}
	tctx := reqctx.WithTenantID(ctx, tenantID)
	cfg, err := g.mcp.GetServerConfig(tctx, serverID)
	if err != nil {
		if errors.Is(err, mcpdomain.ErrServerNotFound) {
			return false, nil
		}
		return false, fmt.Errorf("system resource guard: inspect mcp server %q: %w", serverID, err)
	}
	return cfg.SystemKey != "" || cfg.ManagementMode == platformknowledge.ManagementPlatform, nil
}

// PlatformManagedMCPServerIDs 返回平台托管 MCP server ID 全集(带 TTL 缓存)。
// 运行时净化(C 层)用它批量剔除 platform MCP tool,避免逐 tool 查询。
func (g *systemResourceGuard) PlatformManagedMCPServerIDs(ctx context.Context, tenantID string) ([]string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.mcpAt.IsZero() && time.Since(g.mcpAt) < platformSetCacheTTL {
		return g.mcpIDs, nil
	}
	if g.mcp == nil {
		return []string{}, nil
	}
	tctx := reqctx.WithTenantID(ctx, tenantID)
	ids, err := g.mcp.PlatformManagedServerIDs(tctx)
	if err != nil {
		if !g.mcpAt.IsZero() {
			// 回退最近成功缓存(陈旧安全)。
			return g.mcpIDs, nil
		}
		return nil, fmt.Errorf("system resource guard: list platform mcp servers: %w", err)
	}
	g.mcpIDs = ids
	g.mcpAt = time.Now()
	return ids, nil
}

// PlatformManagedWorkspaceIDs 返回平台托管 workspace ID 全集(带 TTL 缓存)。
func (g *systemResourceGuard) PlatformManagedWorkspaceIDs(ctx context.Context, tenantID string) ([]string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.wsAt.IsZero() && time.Since(g.wsAt) < platformSetCacheTTL {
		return g.wsIDs, nil
	}
	if g.ws == nil {
		return []string{}, nil
	}
	workspaces, err := g.ws.ListWorkspaces(ctx, tenantID)
	if err != nil {
		if !g.wsAt.IsZero() {
			return g.wsIDs, nil
		}
		return nil, fmt.Errorf("system resource guard: list platform workspaces: %w", err)
	}
	ids := make([]string, 0, len(workspaces))
	for _, ws := range workspaces {
		if ws.SystemKey == platformknowledge.SystemWorkspaceKey ||
			ws.ManagementMode == platformknowledge.ManagementPlatform {
			ids = append(ids, ws.ID)
		}
	}
	g.wsIDs = ids
	g.wsAt = time.Now()
	return ids, nil
}
