package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"unicode/utf8"

	agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
	mcpdomain "github.com/byteBuilderX/stratum/internal/mcp/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/platformmcp"
)

var (
	ErrUnknownTool      = errors.New("platform MCP tool is not registered")
	ErrInvalidArguments = errors.New("platform MCP tool arguments are invalid")
)

type ToolInvoker interface {
	Call(context.Context, string, map[string]any) (agentport.MCPToolResult, error)
}

type ToolDispatcher struct {
	invoker ToolInvoker
}

func NewToolDispatcher(invoker ToolInvoker) (*ToolDispatcher, error) {
	if invoker == nil {
		return nil, errors.New("platform MCP tool invoker is not configured")
	}
	return &ToolDispatcher{invoker: invoker}, nil
}

func (d *ToolDispatcher) ListTools(context.Context) []mcpdomain.Tool {
	return phase1Tools()
}

func (d *ToolDispatcher) CallTool(
	ctx context.Context,
	name string,
	arguments map[string]any,
) (agentport.MCPToolResult, error) {
	if !slices.Contains(platformmcp.Phase1ToolNames, name) {
		return agentport.MCPToolResult{}, ErrUnknownTool
	}
	if err := validateToolArguments(name, arguments); err != nil {
		return agentport.MCPToolResult{}, errors.Join(ErrInvalidArguments, err)
	}
	result, err := d.invoker.Call(ctx, name, arguments)
	if err != nil {
		return agentport.MCPToolResult{}, fmt.Errorf("invoke platform MCP tool: %w", err)
	}
	return result, nil
}

func phase1Tools() []mcpdomain.Tool {
	return []mcpdomain.Tool{
		{
			Name:        "stratum_search_official_docs",
			Description: "检索当前版本的 Stratum 官方文档。",
			InputSchema: objectSchema(map[string]any{
				"query": map[string]any{
					"type": "string", "minLength": 1, "maxLength": constants.SystemAssistantQueryMaxRunes,
				},
			}, []string{"query"}),
		},
		{
			Name:        "stratum_diagnose_tenant",
			Description: "按当前成员权限只读诊断当前租户业务资源。",
			InputSchema: objectSchema(map[string]any{
				"areas": map[string]any{
					"type": "array", "minItems": 1, "maxItems": constants.SystemAssistantAreasMaxCount,
					"uniqueItems": true,
					"items": map[string]any{
						"type": "string", "enum": diagnosticAreas(),
					},
				},
			}, []string{"areas"}),
		},
		{
			Name:        "stratum_propose_resource_change",
			Description: "创建受治理的资源变更提案，不直接修改资源。",
			InputSchema: objectSchema(map[string]any{
				"resourceKind": map[string]any{
					"type": "string", "enum": proposalResourceKinds(),
				},
				"operation": map[string]any{
					"type": "string", "enum": []string{"create", "update"},
				},
				"resourceId": map[string]any{"type": "string", "minLength": 1},
				"payload":    map[string]any{"type": "object"},
			}, []string{"resourceKind", "operation", "payload"}),
		},
	}
}

func objectSchema(properties map[string]any, required []string) map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           properties,
		"required":             required,
	}
}

func validateToolArguments(name string, arguments map[string]any) error {
	switch name {
	case "stratum_search_official_docs":
		return validateDocsArguments(arguments)
	case "stratum_diagnose_tenant":
		return validateDiagnosticArguments(arguments)
	case "stratum_propose_resource_change":
		return validateProposalArguments(arguments)
	default:
		return ErrUnknownTool
	}
}

func validateDocsArguments(arguments map[string]any) error {
	var input struct {
		Query string `json:"query"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return err
	}
	if input.Query == "" || utf8.RuneCountInString(input.Query) > constants.SystemAssistantQueryMaxRunes {
		return errors.New("query length is invalid")
	}
	return nil
}

func validateDiagnosticArguments(arguments map[string]any) error {
	var input struct {
		Areas []string `json:"areas"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return err
	}
	if len(input.Areas) == 0 || len(input.Areas) > constants.SystemAssistantAreasMaxCount {
		return errors.New("diagnostic area count is invalid")
	}
	seen := make(map[string]struct{}, len(input.Areas))
	allowed := diagnosticAreas()
	for _, area := range input.Areas {
		if !slices.Contains(allowed, area) {
			return errors.New("diagnostic area is not allowed")
		}
		if _, exists := seen[area]; exists {
			return errors.New("diagnostic areas must be unique")
		}
		seen[area] = struct{}{}
	}
	return nil
}

func validateProposalArguments(arguments map[string]any) error {
	var input struct {
		ResourceKind string         `json:"resourceKind"`
		Operation    string         `json:"operation"`
		ResourceID   string         `json:"resourceId"`
		Payload      map[string]any `json:"payload"`
	}
	if err := decodeArguments(arguments, &input); err != nil {
		return err
	}
	if !slices.Contains(proposalResourceKinds(), input.ResourceKind) || input.Payload == nil {
		return errors.New("proposal resource is invalid")
	}
	if input.Operation == "create" {
		if input.ResourceID != "" {
			return errors.New("create proposal must not include resource ID")
		}
		return nil
	}
	if input.Operation != "update" || input.ResourceID == "" {
		return errors.New("update proposal requires resource ID")
	}
	return nil
}

func decodeArguments(arguments map[string]any, target any) error {
	encoded, err := json.Marshal(arguments)
	if err != nil {
		return fmt.Errorf("encode tool arguments: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode tool arguments: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("tool arguments have trailing content")
	}
	return nil
}

func diagnosticAreas() []string {
	return []string{"agent", "skill", "mcp", "knowledge", "model"}
}

func proposalResourceKinds() []string {
	return []string{"agent", "skill_draft", "mcp_config", "knowledge_workspace"}
}
