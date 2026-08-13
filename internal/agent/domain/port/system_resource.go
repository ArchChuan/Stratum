package port

import "context"

// SystemResourceGuard 判定 MCP server / knowledge workspace 是否平台托管。
// 由消费方(agent)定义、wiring 组合根实现(薄 ACL 适配 mcp/knowledge context),
// agent 不 import 兄弟 domain,故返回 (bool, error) 而非兄弟 sentinel。
//
// Fail closed 语义:
//   - 写路径(A2)单查命中平台托管 → 拒绝挂载;查询失败 → 拒绝挂载;
//   - 运行时净化(C)批量查询失败 → 使用最近成功缓存;无缓存且失败 → fail closed;
//   - nil guard 一律 fail closed(AgentService 侧,不静默跳过)。
type SystemResourceGuard interface {
	// IsPlatformManagedMCPServer 单查单个 MCP server 是否平台托管(写路径 A2)。
	IsPlatformManagedMCPServer(ctx context.Context, tenantID, serverID string) (bool, error)
	// PlatformManagedMCPServerIDs 返回平台托管 MCP server ID 全集(运行时净化 C,
	// 批量避免逐 tool 查询)。
	PlatformManagedMCPServerIDs(ctx context.Context, tenantID string) ([]string, error)
	// PlatformManagedWorkspaceIDs 返回平台托管 workspace ID 全集(运行时净化 C)。
	PlatformManagedWorkspaceIDs(ctx context.Context, tenantID string) ([]string, error)
}
