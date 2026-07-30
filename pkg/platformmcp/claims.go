package platformmcp

import "github.com/golang-jwt/jwt/v5"

const (
	InvocationIssuer      = "stratum-agent-runtime"
	InvocationAudience    = "stratum-platform-mcp"
	APIDelegationIssuer   = "stratum-token-exchange"
	APIDelegationAudience = "stratum-api"
	TokenUseInvocation    = "platform_mcp_invocation"
	TokenUseAPIDelegation = "platform_mcp_api_delegation"
)

type InvocationClaims struct {
	TenantID    string `json:"tenant_id"`
	UserID      string `json:"user_id"`
	AgentID     string `json:"agent_id"`
	ServerID    string `json:"server_id"`
	ToolName    string `json:"tool_name"`
	ExecutionID string `json:"execution_id"`
	ApprovalID  string `json:"approval_id,omitempty"`
	TokenUse    string `json:"token_use"`
	jwt.RegisteredClaims
}

type APIDelegationClaims struct {
	TenantID     string `json:"tenant_id"`
	AgentID      string `json:"agent_id"`
	ServerID     string `json:"server_id"`
	ToolName     string `json:"tool_name"`
	ExecutionID  string `json:"execution_id"`
	HTTPMethod   string `json:"http_method"`
	PathTemplate string `json:"path_template"`
	ResourceID   string `json:"resource_id,omitempty"`
	ApprovalID   string `json:"approval_id,omitempty"`
	Role         string `json:"role"`
	TokenUse     string `json:"token_use"`
	jwt.RegisteredClaims
}
