package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// agentClient executes agents over the real Stratum HTTP endpoint. It
// authenticates as the tenant owner via the shared httpClient roundtrip, so
// the bearer credential never appears in logs, URLs, or error bodies.
type agentClient struct {
	client *httpClient
}

// executeAgent runs one agent with a query and returns its result text.
// ExecuteAgentRequest: {"query": "..."}. ExecuteAgentResponse:
// {"result": <value>, "steps": [...], "status": "...", "error": "..."}.
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
	if err := classifyHTTP(path, status, string(body)); err != nil {
		return "", err
	}
	var resp struct {
		Result any    `json:"result"`
		Status string `json:"status"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", &infraError{fmt.Errorf("decode execute agent response: %w", err)}
	}
	if resp.Status == "error" || resp.Error != "" {
		return "", fmt.Errorf("agent execution failed: %s", resp.Error)
	}
	switch v := resp.Result.(type) {
	case string:
		return v, nil
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return "", fmt.Errorf("encode agent result: %w", err)
		}
		return string(b), nil
	}
}
