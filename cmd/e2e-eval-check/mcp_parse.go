package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// parseMCPServers reads the point snapshot's server declarations.
//
//	servers:
//	  - name: weather
//	    url: http://localhost:18081
func parseMCPServers(snapshot map[string]any) (map[string]mcpServerConfig, error) {
	raw, ok := snapshot["servers"].([]any)
	if !ok {
		return nil, fmt.Errorf("mcp point snapshot.servers must be a list")
	}
	out := make(map[string]mcpServerConfig, len(raw))
	for _, item := range raw {
		obj, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("mcp server entry must be an object")
		}
		name, _ := obj["name"].(string)
		url, _ := obj["url"].(string)
		if name == "" || url == "" {
			return nil, fmt.Errorf("mcp server entry requires name and url")
		}
		out[name] = mcpServerConfig{URL: url}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("mcp point declares no servers")
	}
	return out, nil
}

// parseToolCall parses a case's Query field as "server/tool args-json".
func parseToolCall(query string) (server, tool string, args map[string]any, err error) {
	parts := strings.SplitN(query, " ", 2)
	spec := parts[0]
	sv := strings.SplitN(spec, "/", 2)
	if len(sv) != 2 {
		return "", "", nil, fmt.Errorf("tool spec must be server/tool")
	}
	args = map[string]any{}
	if len(parts) == 2 && strings.TrimSpace(parts[1]) != "" {
		if err := json.Unmarshal([]byte(parts[1]), &args); err != nil {
			return "", "", nil, fmt.Errorf("tool args must be JSON: %w", err)
		}
	}
	return sv[0], sv[1], args, nil
}
