package domain

// DashboardOverview contains tenant-scoped resource counts for the overview page.
type DashboardOverview struct {
	Agents              int
	Skills              int
	KnowledgeWorkspaces int
	MCPServers          int
	ModelProviders      int
	TenantMembers       int
	Workflows           int
	AgentUserMessages7d int
}
