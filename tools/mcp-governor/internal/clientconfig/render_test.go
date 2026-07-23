package clientconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
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
		{config.ClientClaude, jsonGolden("mcpServers", "claude", true)},
		{config.ClientVSCode, jsonGolden("servers", "vscode", true)},
		{config.ClientLingma, jsonGolden("mcpServers", "lingma", false)},
	}
	for _, test := range tests {
		t.Run(string(test.client), func(t *testing.T) {
			got, err := Render(Options{Client: test.client, ConfigPath: "/etc/mcp/catalog.json",
				GovernorPath: "/usr/local/bin/mcp-governor", Services: cfg.Services})
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != test.want {
				t.Fatalf("rendered config:\n%s\nwant:\n%s", got, test.want)
			}
			if !strings.HasSuffix(string(got), "\n") {
				t.Fatal("config lacks trailing newline")
			}
			lower := strings.ToLower(string(got))
			for _, forbidden := range []string{"--session", "pid", "environment", "\"env\"", "token", "password",
				"secret", "api_key", "api-key"} {
				if strings.Contains(lower, forbidden) {
					t.Fatalf("generated config contains forbidden field %q: %s", forbidden, got)
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
	base := cfg.Services[0]
	unavailable := base
	unavailable.Name, unavailable.Clients = "claude-only", []config.Client{config.ClientClaude}
	repository := base
	repository.Name, repository.Scope = "repository-index", config.ScopeRepository
	nonStdio := base
	nonStdio.Name, nonStdio.Transport = "remote", config.TransportStreamableHTTP
	tests := []struct {
		name string
		opts Options
		want string
	}{
		{"unknown client", Options{Client: "other", ConfigPath: "/catalog", GovernorPath: "/governor", Services: cfg.Services}, "client"},
		{"empty catalog", Options{Client: config.ClientCodex, GovernorPath: "/governor", Services: cfg.Services}, "config path"},
		{"empty governor", Options{Client: config.ClientCodex, ConfigPath: "/catalog", Services: cfg.Services}, "governor path"},
		{"unavailable client", Options{Client: config.ClientCodex, ConfigPath: "/catalog", GovernorPath: "/governor", Services: []config.ServiceRule{unavailable}}, "not enabled"},
		{"repository scope", Options{Client: config.ClientCodex, ConfigPath: "/catalog", GovernorPath: "/governor", Services: []config.ServiceRule{repository}}, "repository"},
		{"non stdio", Options{Client: config.ClientCodex, ConfigPath: "/catalog", GovernorPath: "/governor", Services: []config.ServiceRule{nonStdio}}, "stdio"},
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

func jsonGolden(root, client string, withType bool) string {
	typeField := ""
	if withType {
		typeField = "\n      \"type\": \"stdio\","
	}
	entry := func(name, command string, serviceArgs []string) string {
		args := []string{"proxy", "--config", "/etc/mcp/catalog.json", "--client", client, "--service", name, "--", command}
		args = append(args, serviceArgs...)
		data, _ := json.MarshalIndent(args, "      ", "  ")
		return "    \"" + name + "\": {" + typeField + "\n      \"command\": \"/usr/local/bin/mcp-governor\",\n" +
			"      \"args\": " + strings.TrimSpace(string(data)) + "\n    }"
	}
	return "{\n  \"" + root + "\": {\n" + entry("alpha", "/opt/mcp/alpha", []string{"serve", "--safe"}) + ",\n" +
		entry("zeta", "/opt/mcp/zeta server", []string{"--label", "quoted \"value\""}) + "\n  }\n}\n"
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
