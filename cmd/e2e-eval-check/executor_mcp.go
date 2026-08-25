package main

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcpExecutor evaluates MCP tool calls with deterministic string assertions.
type mcpExecutor struct {
	invoker mcpToolInvoker
}

// mcpInvokerCloser is implemented by live invokers that hold MCP sessions.
// Injected fakes do not implement it, so Execute's deferred close is a no-op.
type mcpInvokerCloser interface {
	Close(ctx context.Context) error
}

func init() {
	registerExecutor("mcp", func() executor { return &mcpExecutor{} })
}

// Execute provisions the invoker from the point snapshot and runs the cases.
func (e *mcpExecutor) Execute(ctx context.Context, o options, p point) (execResult, error) {
	inv, err := e.invokerOr(p)
	if err != nil {
		return execResult{}, err
	}
	e.invoker = inv
	if closer, ok := inv.(mcpInvokerCloser); ok {
		defer closer.Close(context.Background())
	}
	dataset, err := loadMCPSet(ctx, o, p)
	if err != nil {
		return execResult{}, err
	}
	out, err := e.runCases(ctx, p, dataset)
	if err != nil {
		return out, err
	}
	out.Evidence = append(out.Evidence, evidence{Kind: "mcp_servers", Ref: fmt.Sprintf("%v", p.Snapshot["servers"])})
	return out, nil
}

// invokerOr builds a live invoker unless one was injected for tests.
func (e *mcpExecutor) invokerOr(p point) (mcpToolInvoker, error) {
	if e.invoker != nil {
		return e.invoker, nil
	}
	cfg, err := parseMCPServers(p.Snapshot)
	if err != nil {
		return nil, err
	}
	return &liveMCPInvoker{servers: cfg, sessions: map[string]*mcp.ClientSession{}}, nil
}

// runCases evaluates every case and aggregates pass_rate/latency/cost.
func (e *mcpExecutor) runCases(ctx context.Context, p point, dataset goldenSet) (execResult, error) {
	res := execResult{Cases: []caseOutcome{}}
	var pass float64
	for _, tc := range dataset.Cases {
		outcome := caseOutcome{
			CaseID:        tc.ID,
			AssertionMode: tc.Mode,
		}
		if tc.Query == "" {
			return res, fmt.Errorf("case %s: mcp case requires tool spec in query", tc.ID)
		}
		server, tool, args, err := parseToolCall(tc.Query)
		if err != nil {
			return res, fmt.Errorf("case %s: %w", tc.ID, err)
		}
		out, err := e.invoker.CallTool(ctx, server, tool, args)
		if err != nil {
			if isInfra(err) {
				return res, err
			}
			outcome.Error = err.Error()
		} else if err := assertOutput(tc.Mode, out, expectedOf(tc)); err != nil {
			outcome.Error = err.Error()
		} else {
			outcome.Passed = true
			pass++
		}
		res.Cases = append(res.Cases, outcome)
	}
	if n := len(res.Cases); n > 0 {
		res.Aggregate = aggregate{CaseCount: n, PassRate: pass / float64(n)}
	}
	return res, nil
}
