package clientconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/byteBuilderX/stratum/tools/mcp-governor/internal/config"
)

const catalogPath = "../../testdata/catalog.json"

func TestRenderGoldenClientConfigs(t *testing.T) {
	cfg := readCatalog(t)
	tests := []struct {
		client config.Client
		want   string
	}{
		{config.ClientCodex, "[mcp_servers.alpha]\ncommand = \"/usr/local/bin/mcp-governor\"\nargs = [\"proxy\", \"--config\", \"/etc/mcp/catalog.json\", \"--client\", \"codex\", \"--service\", \"alpha\", \"--\", \"/opt/mcp/alpha\", \"serve\", \"--safe\"]\n\n[mcp_servers.zeta]\ncommand = \"/usr/local/bin/mcp-governor\"\nargs = [\"proxy\", \"--config\", \"/etc/mcp/catalog.json\", \"--client\", \"codex\", \"--service\", \"zeta\", \"--\", \"/opt/mcp/zeta server\", \"--label\", \"quoted \\\"value\\\"\"]\n"},
		{config.ClientClaude, "{\n  \"mcpServers\": {\n    \"alpha\": {\n      \"type\": \"stdio\",\n      \"command\": \"/usr/local/bin/mcp-governor\",\n      \"args\": [\n        \"proxy\",\n        \"--config\",\n        \"/etc/mcp/catalog.json\",\n        \"--client\",\n        \"claude\",\n        \"--service\",\n        \"alpha\",\n        \"--\",\n        \"/opt/mcp/alpha\",\n        \"serve\",\n        \"--safe\"\n      ]\n    },\n    \"claude-only\": {\n      \"type\": \"stdio\",\n      \"command\": \"/usr/local/bin/mcp-governor\",\n      \"args\": [\n        \"proxy\",\n        \"--config\",\n        \"/etc/mcp/catalog.json\",\n        \"--client\",\n        \"claude\",\n        \"--service\",\n        \"claude-only\",\n        \"--\",\n        \"/opt/mcp/claude\",\n        \"start\"\n      ]\n    },\n    \"zeta\": {\n      \"type\": \"stdio\",\n      \"command\": \"/usr/local/bin/mcp-governor\",\n      \"args\": [\n        \"proxy\",\n        \"--config\",\n        \"/etc/mcp/catalog.json\",\n        \"--client\",\n        \"claude\",\n        \"--service\",\n        \"zeta\",\n        \"--\",\n        \"/opt/mcp/zeta server\",\n        \"--label\",\n        \"quoted \\\"value\\\"\"\n      ]\n    }\n  }\n}\n"},
		{config.ClientVSCode, "{\n  \"servers\": {\n    \"alpha\": {\n      \"type\": \"stdio\",\n      \"command\": \"/usr/local/bin/mcp-governor\",\n      \"args\": [\n        \"proxy\",\n        \"--config\",\n        \"/etc/mcp/catalog.json\",\n        \"--client\",\n        \"vscode\",\n        \"--service\",\n        \"alpha\",\n        \"--\",\n        \"/opt/mcp/alpha\",\n        \"serve\",\n        \"--safe\"\n      ]\n    },\n    \"zeta\": {\n      \"type\": \"stdio\",\n      \"command\": \"/usr/local/bin/mcp-governor\",\n      \"args\": [\n        \"proxy\",\n        \"--config\",\n        \"/etc/mcp/catalog.json\",\n        \"--client\",\n        \"vscode\",\n        \"--service\",\n        \"zeta\",\n        \"--\",\n        \"/opt/mcp/zeta server\",\n        \"--label\",\n        \"quoted \\\"value\\\"\"\n      ]\n    }\n  }\n}\n"},
		{config.ClientLingma, "{\n  \"mcpServers\": {\n    \"alpha\": {\n      \"command\": \"/usr/local/bin/mcp-governor\",\n      \"args\": [\n        \"proxy\",\n        \"--config\",\n        \"/etc/mcp/catalog.json\",\n        \"--client\",\n        \"lingma\",\n        \"--service\",\n        \"alpha\",\n        \"--\",\n        \"/opt/mcp/alpha\",\n        \"serve\",\n        \"--safe\"\n      ]\n    },\n    \"zeta\": {\n      \"command\": \"/usr/local/bin/mcp-governor\",\n      \"args\": [\n        \"proxy\",\n        \"--config\",\n        \"/etc/mcp/catalog.json\",\n        \"--client\",\n        \"lingma\",\n        \"--service\",\n        \"zeta\",\n        \"--\",\n        \"/opt/mcp/zeta server\",\n        \"--label\",\n        \"quoted \\\"value\\\"\"\n      ]\n    }\n  }\n}\n"},
	}
	for _, test := range tests {
		t.Run(string(test.client), func(t *testing.T) {
			names := []string{"alpha", "zeta"}
			if test.client == config.ClientClaude {
				names = append(names, "claude-only")
			}
			got, err := Render(Options{Client: test.client, ConfigPath: "/etc/mcp/catalog.json",
				GovernorPath: "/usr/local/bin/mcp-governor", Services: selectServices(t, cfg, names...)})
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != test.want {
				t.Fatalf("rendered config:\n%s\nwant:\n%s", got, test.want)
			}
			if !strings.HasSuffix(string(got), "\n") {
				t.Fatal("config lacks trailing newline")
			}
			if strings.Contains(string(got), "--session") || strings.Contains(string(got), "PID") ||
				strings.Contains(string(got), "environment") || strings.Contains(string(got), "\"env\"") {
				t.Fatalf("unsafe generated field: %s", got)
			}
			for _, credential := range []string{"token", "password", "secret", "api_key", "api-key"} {
				if strings.Contains(strings.ToLower(string(got)), credential) {
					t.Fatalf("generated config contains credential field %q: %s", credential, got)
				}
			}
			if test.client != config.ClientCodex {
				var parsed any
				if err := json.Unmarshal(got, &parsed); err != nil {
					t.Fatalf("invalid JSON: %v", err)
				}
			}
		})
	}
}

func TestRenderRejectsInvalidOrUnavailableSelections(t *testing.T) {
	cfg := readCatalog(t)
	tests := []struct {
		name string
		opts Options
		want string
	}{
		{"unknown client", Options{Client: "other", ConfigPath: "/catalog", GovernorPath: "/governor", Services: cfg.Services}, "client"},
		{"empty catalog", Options{Client: config.ClientCodex, GovernorPath: "/governor", Services: cfg.Services}, "config path"},
		{"empty governor", Options{Client: config.ClientCodex, ConfigPath: "/catalog", Services: cfg.Services}, "governor path"},
		{"unavailable client", Options{Client: config.ClientCodex, ConfigPath: "/catalog", GovernorPath: "/governor", Services: selectServices(t, cfg, "claude-only")}, "not enabled"},
		{"repository scope", Options{Client: config.ClientCodex, ConfigPath: "/catalog", GovernorPath: "/governor", Services: selectServices(t, cfg, "repository-index")}, "repository"},
		{"non stdio", Options{Client: config.ClientCodex, ConfigPath: "/catalog", GovernorPath: "/governor", Services: selectServices(t, cfg, "remote")}, "stdio"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Render(test.opts)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), test.want) {
				t.Fatalf("error=%v, want %q", err, test.want)
			}
		})
	}
}

func readCatalog(t *testing.T) config.Config {
	t.Helper()
	f, err := os.Open(filepath.Clean(catalogPath))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cfg, err := config.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func selectServices(t *testing.T, cfg config.Config, names ...string) []config.ServiceRule {
	t.Helper()
	var selected []config.ServiceRule
	for _, service := range cfg.Services {
		if slices.Contains(names, service.Name) {
			selected = append(selected, service)
		}
	}
	if len(selected) != len(names) {
		t.Fatalf("selected %d services, want %d", len(selected), len(names))
	}
	return selected
}
