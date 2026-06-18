// Package providers implements skill providers for MCP, LLM, and code execution.
package providers

import (
	"context"
	"fmt"

	"github.com/byteBuilderX/stratum/internal/skill/domain/port"
)

// MCPSkillProvider adapts an MCP adapter (read view) to the SkillProvider
// contract used by the skill gateway.
type MCPSkillProvider struct {
	adapter port.MCPAdapterReader
}

// NewMCPSkillProvider wires the adapter.
func NewMCPSkillProvider(adapter port.MCPAdapterReader) *MCPSkillProvider {
	return &MCPSkillProvider{adapter: adapter}
}

func (p *MCPSkillProvider) SkillIDs() []string {
	return p.adapter.SkillIDs()
}

func (p *MCPSkillProvider) Has(skillID string) bool {
	return p.adapter.Has(skillID)
}

func (p *MCPSkillProvider) SkillType() string {
	return "mcp"
}

func (p *MCPSkillProvider) Execute(ctx context.Context, skillID string, input any) (any, error) {
	if !p.adapter.Has(skillID) {
		return nil, fmt.Errorf("MCP skill not found: %s", skillID)
	}
	return p.adapter.Execute(ctx, skillID, input)
}
