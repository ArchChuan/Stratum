package port

import "context"

// MCPServerSummary 是 MCP server 的只读摘要投影：Tools 仅暴露工具名列表，
// 不携带 Tool 的 InputSchema/OutputSchema 等内部契约，防止内部端点与描述
// 进入系统助手模型可见面。
type MCPServerSummary struct {
	ID        string
	Name      string
	Version   string
	Transport string
	Status    string
	Tools     []string
}

// MCPServerLister 由 wiring 以薄 ACL 适配 mcp context 的 MCPService，
// 供系统助手 stratum_list_mcp_servers 工具只读枚举当前租户已连接的
// MCP server。消费方在 agent domain/port 定义接口，避免 application
// 层 import 兄弟 context。
type MCPServerLister interface {
	ListMCPServers(context.Context) ([]MCPServerSummary, error)
}
