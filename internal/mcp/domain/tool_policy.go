package domain

import "fmt"

type ToolRiskLevel string

const (
	ToolRiskRead            ToolRiskLevel = "read"
	ToolRiskWriteReversible ToolRiskLevel = "write_reversible"
	ToolRiskDestructive     ToolRiskLevel = "destructive"
	ToolRiskUnclassified    ToolRiskLevel = "unclassified"
)

func (r ToolRiskLevel) Validate() error {
	switch r {
	case ToolRiskRead, ToolRiskWriteReversible, ToolRiskDestructive, ToolRiskUnclassified:
		return nil
	default:
		return fmt.Errorf("invalid MCP tool risk level %q", r)
	}
}

type ToolPolicy struct {
	ServerID  string        `json:"serverId"`
	ToolName  string        `json:"toolName"`
	RiskLevel ToolRiskLevel `json:"riskLevel"`
	UpdatedBy string        `json:"updatedBy,omitempty"`
}
