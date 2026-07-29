package platformmcp

const (
	SystemAssistantKey = "stratum.platform_assistant"
	SystemServerID     = "stratum-platform-mcp"
	SystemServerKey    = "stratum.platform_mcp"
	ManagementPlatform = "platform_managed"
	ManagementTenant   = "tenant_managed"
)

var Phase1ToolNames = []string{
	"stratum_search_official_docs",
	"stratum_diagnose_tenant",
	"stratum_propose_resource_change",
}
