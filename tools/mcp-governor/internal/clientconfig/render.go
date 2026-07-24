package clientconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/byteBuilderX/stratum/tools/mcp-governor/internal/config"
)

type Options struct {
	Client           config.Client
	ConfigPath       string
	GovernorPath     string
	Services         []config.ServiceRule
	SelectedServices []string
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
	services, err := selectServices(opts)
	if err != nil {
		return nil, err
	}
	sort.Slice(services, func(i, j int) bool { return services[i].Name < services[j].Name })
	for _, service := range services {
		if !enabledFor(service, opts.Client) {
			return nil, fmt.Errorf("service %q is not enabled for client", service.Name)
		}
		if err := validateServiceStructure(service); err != nil {
			return nil, err
		}
		if service.Transport != config.TransportStdio {
			return nil, fmt.Errorf("service %q does not use stdio transport", service.Name)
		}
		if service.Scope == config.ScopeRepository {
			return nil, fmt.Errorf("service %q requires a repository identity unavailable to static config", service.Name)
		}
		if err := validateRenderedStrings(opts, service); err != nil {
			return nil, err
		}
	}
	if opts.Client == config.ClientCodex {
		return renderCodex(opts, services), nil
	}
	return renderJSON(opts, services)
}

func selectServices(opts Options) ([]config.ServiceRule, error) {
	byName := make(map[string]config.ServiceRule, len(opts.Services))
	for _, service := range opts.Services {
		if !safeName(service.Name) {
			return nil, fmt.Errorf("service name is unsafe")
		}
		if _, exists := byName[service.Name]; exists {
			return nil, fmt.Errorf("service name %q is duplicate", service.Name)
		}
		byName[service.Name] = service
	}
	if len(opts.SelectedServices) > 0 {
		selected := make([]config.ServiceRule, 0, len(opts.SelectedServices))
		seen := make(map[string]struct{}, len(opts.SelectedServices))
		for _, name := range opts.SelectedServices {
			if _, exists := seen[name]; exists {
				return nil, fmt.Errorf("selected service %q is duplicate", name)
			}
			seen[name] = struct{}{}
			service, exists := byName[name]
			if !exists {
				return nil, fmt.Errorf("selected service %q is missing", name)
			}
			selected = append(selected, service)
		}
		return selected, nil
	}
	selected := make([]config.ServiceRule, 0, len(opts.Services))
	for _, service := range opts.Services {
		if !enabledFor(service, opts.Client) {
			continue
		}
		if err := validateServiceStructure(service); err != nil {
			return nil, err
		}
		if service.Transport == config.TransportStdio && service.Scope != config.ScopeRepository {
			selected = append(selected, service)
		}
	}
	return selected, nil
}

func validateServiceStructure(service config.ServiceRule) error {
	if !knownTransport(service.Transport) || !knownScope(service.Scope) || !knownSessionPolicy(service.SessionPolicy) {
		return fmt.Errorf("service %q has invalid static configuration", service.Name)
	}
	if service.Scope == config.ScopeRepository && service.SessionPolicy != config.SessionPolicyIsolated ||
		service.Scope == config.ScopeSession && service.SessionPolicy != config.SessionPolicySessionLocal {
		return fmt.Errorf("service %q has invalid scope session policy", service.Name)
	}
	return nil
}

func validateRenderedStrings(opts Options, service config.ServiceRule) error {
	values := append([]string{opts.ConfigPath, opts.GovernorPath, service.Command}, service.Args...)
	for _, value := range values {
		if !utf8.ValidString(value) {
			return fmt.Errorf("service %q rendered string must be valid UTF-8", service.Name)
		}
		for _, char := range value {
			if char < 0x20 || char == 0x7f {
				return fmt.Errorf("service %q rendered string contains a control character", service.Name)
			}
		}
	}
	return nil
}

func knownTransport(value config.Transport) bool {
	return value == config.TransportStdio || value == config.TransportStreamableHTTP || value == config.TransportSSE
}

func knownScope(value config.Scope) bool {
	return value == config.ScopeUser || value == config.ScopeSession || value == config.ScopeRepository
}

func knownSessionPolicy(value config.SessionPolicy) bool {
	return value == config.SessionPolicyIsolated || value == config.SessionPolicySessionLocal
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
