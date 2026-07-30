package application

import (
	"errors"
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/byteBuilderX/stratum/pkg/safetext"
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
		ModelContent:      modelContent,
		Summary:           summary,
		SHA256:            safe.SHA256,
		StructuredContent: cloneStructuredContent(result.StructuredContent),
		Untrusted:         true,
		Truncated:         safe.Truncated,
	}, nil
}

func cloneStructuredContent(content map[string]any) map[string]any {
	if content == nil {
		return nil
	}
	cloned := make(map[string]any, len(content))
	for key, value := range content {
		if sensitiveStructuredKey(key) {
			cloned[key] = "[REDACTED]"
			continue
		}
		cloned[key] = sanitizeStructuredValue(value)
	}
	return cloned
}

func sensitiveStructuredKey(key string) bool {
	normalized := strings.NewReplacer("_", "", "-", "").Replace(strings.ToLower(key))
	switch normalized {
	case "authorization", "password", "token", "apikey", "secret":
		return true
	default:
		return false
	}
}

func sanitizeStructuredValue(value any) any {
	switch typed := value.(type) {
	case string:
		return safetext.RedactCredentials(typed)
	case map[string]any:
		return cloneStructuredContent(typed)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			out[i] = sanitizeStructuredValue(item)
		}
		return out
	default:
		return value
	}
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
