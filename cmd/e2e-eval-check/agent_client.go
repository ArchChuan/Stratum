package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// agentClient executes agents over the real Stratum HTTP endpoint. It
// authenticates as the tenant owner via the shared httpClient roundtrip, so
// the bearer credential never appears in logs, URLs, or error bodies.
type agentClient struct {
	client *httpClient
}

// executeAgent runs one agent with a query and returns its result text.
// ExecuteAgentRequest: {"query": "..."}. The server's 200 response is an
// AgentExecutionResult carrying the final answer in output; every non-200 is
// gated by classifyExecuteAgent (404 fatal, other 4xx case error, 5xx infra).
func (a *agentClient) executeAgent(ctx context.Context, agentID, query string) (string, error) {
	payload, err := json.Marshal(map[string]any{"query": query})
	if err != nil {
		return "", fmt.Errorf("encode execute agent: %w", err)
	}
	path := "/agents/" + agentID + "/execute"
	status, body, err := a.client.roundtrip(ctx, http.MethodPost, path, "application/json", bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	if err := classifyExecuteAgent(path, status, string(body)); err != nil {
		return "", err
	}
	var resp struct {
		Output string `json:"output"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", &infraError{fmt.Errorf("decode execute agent response: %w", err)}
	}
	// A 200 is a completed execution; a blank output means the run ended
	// without a usable answer (e.g. an approval never resolved). That is an
	// agent-behavior outcome — a dataset or product defect — not an
	// infrastructure break, so keep it a case error.
	if strings.TrimSpace(resp.Output) == "" {
		if resp.Error != "" {
			return "", fmt.Errorf("agent execution failed: %s", resp.Error)
		}
		return "", fmt.Errorf("agent execution completed without a result")
	}
	return resp.Output, nil
}

// classifyExecuteAgent labels the agent execute response. A 404 means the
// point references an agent that does not exist — a dataset defect that must
// abort the run (fatal, exit 1) rather than let every case 404 into a silent
// 0%-pass green, matching skill's fail-closed provisioning. Everything else
// defers to classifyHTTP (401/403/5xx → infra, other 4xx → case error).
func classifyExecuteAgent(path string, status int, body string) error {
	if status == http.StatusNotFound {
		return &resourceNotFoundError{fmt.Errorf("%s: HTTP %d: %s", path, status, body)}
	}
	return classifyHTTP(path, status, body)
}

// deleteAgent removes a transient carrier agent.
func (c *httpClient) deleteAgent(ctx context.Context, agentID string) error {
	status, body, err := c.roundtrip(ctx, http.MethodDelete, "/agents/"+agentID, "", nil)
	if err != nil {
		return err
	}
	return classifyHTTP("/agents/"+agentID, status, string(body))
}
