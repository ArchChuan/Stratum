package platformmcp

import "net/http"

type ToolContract struct {
	Name             string
	Method           string
	Path             string
	MinimumRole      string
	RequiresApproval bool
}

type ContractRegistry interface {
	Lookup(toolName string) (ToolContract, bool)
}

type StaticContracts struct {
	contracts map[string]ToolContract
}

func NewPhase1Contracts() StaticContracts {
	return StaticContracts{contracts: map[string]ToolContract{
		"stratum_search_official_docs": {
			Name: "stratum_search_official_docs", Method: http.MethodPost,
			Path: "/internal/platform-assistant/docs/search", MinimumRole: "member",
		},
		"stratum_diagnose_tenant": {
			Name: "stratum_diagnose_tenant", Method: http.MethodPost,
			Path: "/internal/platform-assistant/diagnostics", MinimumRole: "member",
		},
		"stratum_propose_resource_change": {
			Name: "stratum_propose_resource_change", Method: http.MethodPost,
			Path: "/internal/platform-assistant/proposals", MinimumRole: "admin",
		},
	}}
}

func (r StaticContracts) Lookup(toolName string) (ToolContract, bool) {
	contract, ok := r.contracts[toolName]
	return contract, ok
}

const (
	SystemAssistantID  = "stratum-platform-assistant"
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
