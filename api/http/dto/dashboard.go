package dto

type DashboardOverviewResponse struct {
	Agents              int `json:"agents"`
	Skills              int `json:"skills"`
	KnowledgeWorkspaces int `json:"knowledge_workspaces"`
	MCPServers          int `json:"mcp_servers"`
	ModelProviders      int `json:"model_providers"`
	TenantMembers       int `json:"tenant_members"`
	Workflows           int `json:"workflows"`
	AgentUserMessages7d int `json:"agent_user_messages_7d"`
}
