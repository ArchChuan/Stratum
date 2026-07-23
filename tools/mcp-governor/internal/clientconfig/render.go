package clientconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/byteBuilderX/stratum/tools/mcp-governor/internal/config"
)

type Options struct {
	Client       config.Client
	ConfigPath   string
	GovernorPath string
	Services     []config.ServiceRule
}

type stdioServer struct {
	Type    string   `json:"type,omitempty"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

func Render(opts Options) ([]byte, error) {
	if !knownClient(opts.Client) {
		return nil, fmt.Errorf("client is not supported")
	}
	if strings.TrimSpace(opts.ConfigPath) == "" {
		return nil, fmt.Errorf("config path is required")
	}
	if strings.TrimSpace(opts.GovernorPath) == "" {
		return nil, fmt.Errorf("governor path is required")
	}
	services := append([]config.ServiceRule(nil), opts.Services...)
	sort.Slice(services, func(i, j int) bool { return services[i].Name < services[j].Name })
	for _, service := range services {
		if !safeName(service.Name) {
			return nil, fmt.Errorf("service name is unsafe")
		}
		if !enabledFor(service, opts.Client) {
			return nil, fmt.Errorf("service %q is not enabled for client", service.Name)
		}
		if service.Transport != config.TransportStdio {
			return nil, fmt.Errorf("service %q does not use stdio transport", service.Name)
		}
		if service.Scope == config.ScopeRepository {
			return nil, fmt.Errorf("service %q requires a repository identity unavailable to static config", service.Name)
		}
	}
	if opts.Client == config.ClientCodex {
		return renderCodex(opts, services), nil
	}
	return renderJSON(opts, services)
}

func renderCodex(opts Options, services []config.ServiceRule) []byte {
	var out strings.Builder
	for i, service := range services {
		if i > 0 {
			out.WriteByte('\n')
		}
		fmt.Fprintf(&out, "[mcp_servers.%s]\ncommand = %s\nargs = [", service.Name, strconv.Quote(opts.GovernorPath))
		args := proxyArgs(opts, service)
		for j, arg := range args {
			if j > 0 {
				out.WriteString(", ")
			}
			out.WriteString(strconv.Quote(arg))
		}
		out.WriteString("]\n")
	}
	return []byte(out.String())
}

func renderJSON(opts Options, services []config.ServiceRule) ([]byte, error) {
	servers := make(map[string]stdioServer, len(services))
	for _, service := range services {
		server := stdioServer{Command: opts.GovernorPath, Args: proxyArgs(opts, service)}
		if opts.Client == config.ClientClaude || opts.Client == config.ClientVSCode {
			server.Type = "stdio"
		}
		servers[service.Name] = server
	}
	var document any
	if opts.Client == config.ClientVSCode {
		document = struct {
			Servers map[string]stdioServer `json:"servers"`
		}{servers}
	} else {
		document = struct {
			MCPServers map[string]stdioServer `json:"mcpServers"`
		}{servers}
	}
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(document); err != nil {
		return nil, fmt.Errorf("encode client config: %w", err)
	}
	return out.Bytes(), nil
}

func proxyArgs(opts Options, service config.ServiceRule) []string {
	args := []string{"proxy", "--config", opts.ConfigPath, "--client", string(opts.Client), "--service", service.Name,
		"--", service.Command}
	return append(args, service.Args...)
}

func enabledFor(service config.ServiceRule, client config.Client) bool {
	for _, enabled := range service.Clients {
		if enabled == client {
			return true
		}
	}
	return false
}

func knownClient(client config.Client) bool {
	return client == config.ClientCodex || client == config.ClientClaude || client == config.ClientVSCode ||
		client == config.ClientLingma
}

func safeName(name string) bool {
	if name == "" {
		return false
	}
	for _, char := range name {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' ||
			char == '-' || char == '_' {
			continue
		}
		return false
	}
	return true
}
