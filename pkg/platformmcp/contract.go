package platformmcp

import "net/http"

type ToolContract struct {
	Name             string
	Method           string
	Path             string
	Risk             RiskLevel
	MinimumRole      string
	RequiresApproval bool
}

type RiskLevel string

const (
	RiskRead            RiskLevel = "read"
	RiskWriteReversible RiskLevel = "write_reversible"

	ToolSearchOfficialDocs    = "stratum_search_official_docs"
	ToolDiagnoseTenant        = "stratum_diagnose_tenant"
	ToolProposeResourceChange = "stratum_propose_resource_change"
)

type ContractRegistry interface {
	Lookup(toolName string) (ToolContract, bool)
}

type StaticContracts struct {
	contracts map[string]ToolContract
}

func NewPhase1Contracts() StaticContracts {
	return StaticContracts{contracts: map[string]ToolContract{
		ToolSearchOfficialDocs: {
			Name: ToolSearchOfficialDocs, Method: http.MethodPost,
			Path: "/internal/platform-assistant/docs/search", Risk: RiskRead, MinimumRole: "member",
		},
		ToolDiagnoseTenant: {
			Name: ToolDiagnoseTenant, Method: http.MethodPost,
			Path: "/internal/platform-assistant/diagnostics", Risk: RiskRead, MinimumRole: "member",
		},
		ToolProposeResourceChange: {
			Name: ToolProposeResourceChange, Method: http.MethodPost,
			Path: "/internal/platform-assistant/proposals", Risk: RiskWriteReversible, MinimumRole: "admin",
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
	ToolSearchOfficialDocs,
	ToolDiagnoseTenant,
	ToolProposeResourceChange,
}
