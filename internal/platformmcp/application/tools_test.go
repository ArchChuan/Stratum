package application

import (
	"context"
	"errors"
	"testing"

	agentport "github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/platformmcp"
)

func TestToolDispatcherExposesOnlyPhase1Tools(t *testing.T) {
	dispatcher, err := NewToolDispatcher(&toolInvokerFake{})
	if err != nil {
		t.Fatal(err)
	}

	tools := dispatcher.ListTools(t.Context())

	if len(tools) != len(platformmcp.Phase1ToolNames) {
		t.Fatalf("tool count=%d, want %d", len(tools), len(platformmcp.Phase1ToolNames))
	}
	for i, name := range platformmcp.Phase1ToolNames {
		if tools[i].Name != name || tools[i].InputSchema["type"] != "object" {
			t.Fatalf("tool %d=%+v, want %q with object schema", i, tools[i], name)
		}
	}
}

func TestPhase1ProposalToolSchemaClosesResourcePayloads(t *testing.T) {
	dispatcher, err := NewToolDispatcher(&toolInvokerFake{})
	if err != nil {
		t.Fatal(err)
	}
	tools := dispatcher.ListTools(t.Context())
	proposal := tools[len(tools)-1].InputSchema
	branches, ok := proposal["oneOf"].([]any)
	if !ok || len(branches) != 8 {
		t.Fatalf("proposal schema=%v", proposal)
	}
	for i, raw := range branches {
		branch := raw.(map[string]any)
		properties := branch["properties"].(map[string]any)
		payload := properties["payload"].(map[string]any)
		if branch["additionalProperties"] != false || payload["additionalProperties"] != false {
			t.Fatalf("branch %d is not closed: %v", i, branch)
		}
	}
}

func TestToolDispatcherRejectsUnknownToolBeforeInvocation(t *testing.T) {
	invoker := &toolInvokerFake{}
	dispatcher, err := NewToolDispatcher(invoker)
	if err != nil {
		t.Fatal(err)
	}

	_, err = dispatcher.CallTool(t.Context(), "tenant_supplied_tool", map[string]any{})

	if !errors.Is(err, ErrUnknownTool) || invoker.calls != 0 {
		t.Fatalf("error=%v calls=%d", err, invoker.calls)
	}
}

func TestToolDispatcherRejectsInvalidArgumentsBeforeInvocation(t *testing.T) {
	tests := []struct {
		name      string
		tool      string
		arguments map[string]any
	}{
		{name: "forged docs tenant", tool: "stratum_search_official_docs", arguments: map[string]any{"query": "help", "tenant": "other"}},
		{name: "unknown diagnostic area", tool: "stratum_diagnose_tenant", arguments: map[string]any{"areas": []any{"iam"}}},
		{name: "update without resource ID", tool: "stratum_propose_resource_change", arguments: map[string]any{
			"resourceKind": "agent", "operation": "update", "payload": map[string]any{"name": "updated"},
		}},
		{name: "proposal payload with unknown field", tool: platformmcp.ToolProposeResourceChange, arguments: map[string]any{
			"resourceKind": "agent", "operation": "create", "payload": map[string]any{"forged": true},
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			invoker := &toolInvokerFake{}
			dispatcher, err := NewToolDispatcher(invoker)
			if err != nil {
				t.Fatal(err)
			}

			_, err = dispatcher.CallTool(t.Context(), tc.tool, tc.arguments)

			if !errors.Is(err, ErrInvalidArguments) || invoker.calls != 0 {
				t.Fatalf("error=%v calls=%d", err, invoker.calls)
			}
		})
	}
}

type toolInvokerFake struct {
	calls int
}

func (f *toolInvokerFake) Call(
	context.Context,
	string,
	map[string]any,
) (agentport.MCPToolResult, error) {
	f.calls++
	return agentport.MCPToolResult{}, nil
}
