package application

import (
	"errors"
	"fmt"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/observability"
)

const MaxToolResultRunes = 32 * 1024

var (
	ErrMCPToolResult       = errors.New("MCP tool returned an error result")
	ErrMCPToolResultSchema = errors.New("MCP tool result schema mismatch")
)

type ToolResultGuard struct{}

func NewToolResultGuard() *ToolResultGuard { return &ToolResultGuard{} }

func (g *ToolResultGuard) Validate(
	result port.MCPToolResult,
	outputSchema map[string]any,
) (port.GuardedToolResult, error) {
	if result.IsError {
		return port.GuardedToolResult{IsError: true, Untrusted: true}, ErrMCPToolResult
	}
	if len(outputSchema) > 0 {
		if err := validateToolResultSchema(outputSchema, result.StructuredContent); err != nil {
			return port.GuardedToolResult{IsError: true, Untrusted: true}, err
		}
	}

	payload := any(result.Content)
	if result.StructuredContent != nil {
		payload = result.StructuredContent
	}
	safe := observability.SafeTracePayload(payload, MaxToolResultRunes)
	modelContent := "<untrusted_tool_result>\n" + safe.Preview
	if safe.Truncated {
		modelContent += "\n[TRUNCATED]"
	}
	modelContent += "\n</untrusted_tool_result>"
	summary := safe.Preview
	if len([]rune(summary)) > 800 {
		summary = string([]rune(summary)[:800]) + "...[truncated]"
	}
	return port.GuardedToolResult{
		ModelContent: modelContent,
		Summary:      summary,
		SHA256:       safe.SHA256,
		Untrusted:    true,
		Truncated:    safe.Truncated,
	}, nil
}

func validateToolResultSchema(schema map[string]any, value any) error {
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	const schemaURL = "urn:stratum:agent-tool-output"
	if err := compiler.AddResource(schemaURL, schema); err != nil {
		return fmt.Errorf("%w: compile resource", ErrMCPToolResultSchema)
	}
	compiled, err := compiler.Compile(schemaURL)
	if err != nil {
		return fmt.Errorf("%w: compile schema", ErrMCPToolResultSchema)
	}
	if err := compiled.Validate(value); err != nil {
		return fmt.Errorf("%w", ErrMCPToolResultSchema)
	}
	return nil
}
