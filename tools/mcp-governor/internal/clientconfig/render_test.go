package clientconfig

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
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
		{config.ClientClaude, jsonGolden("mcpServers", "claude", true, true)},
		{config.ClientVSCode, jsonGolden("servers", "vscode", true, false)},
		{config.ClientLingma, jsonGolden("mcpServers", "lingma", false, false)},
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
		{"unavailable client", Options{Client: config.ClientCodex, ConfigPath: "/catalog", GovernorPath: "/governor", Services: []config.ServiceRule{unavailable}, SelectedServices: []string{"claude-only"}}, "not enabled"},
		{"repository scope", Options{Client: config.ClientCodex, ConfigPath: "/catalog", GovernorPath: "/governor", Services: []config.ServiceRule{repository}, SelectedServices: []string{"repository-index"}}, "repository"},
		{"non stdio", Options{Client: config.ClientCodex, ConfigPath: "/catalog", GovernorPath: "/governor", Services: []config.ServiceRule{nonStdio}, SelectedServices: []string{"remote"}}, "stdio"},
		{"missing selected", Options{Client: config.ClientCodex, ConfigPath: "/catalog", GovernorPath: "/governor", Services: cfg.Services, SelectedServices: []string{"missing"}}, "missing"},
		{"duplicate selected", Options{Client: config.ClientCodex, ConfigPath: "/catalog", GovernorPath: "/governor", Services: cfg.Services, SelectedServices: []string{"alpha", "alpha"}}, "duplicate"},
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

func TestRenderExplicitSelectionRendersOnlyRequestedServices(t *testing.T) {
	cfg := readCatalog(t)
	got, err := Render(Options{Client: config.ClientClaude, ConfigPath: "/catalog", GovernorPath: "/governor",
		Services: cfg.Services, SelectedServices: []string{"claude-only"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "claude-only") || strings.Contains(string(got), "alpha") ||
		strings.Contains(string(got), "zeta") {
		t.Fatalf("rendered config=%s", got)
	}
}

func TestRenderRejectsAmbiguousCatalogAndUnsafeTOMLStrings(t *testing.T) {
	cfg := readCatalog(t)
	duplicate := append([]config.ServiceRule(nil), cfg.Services...)
	duplicate = append(duplicate, cfg.Services[0])
	unsafeName := cfg.Services[0]
	unsafeName.Name = "alpha]\\ninjected"
	invalidPolicy := cfg.Services[0]
	invalidPolicy.SessionPolicy = "invalid"
	tests := []struct {
		name     string
		services []config.ServiceRule
		config   string
		governor string
		want     string
	}{
		{"duplicate catalog", duplicate, "/catalog", "/governor", "duplicate"},
		{"table injection", []config.ServiceRule{unsafeName}, "/catalog", "/governor", "name"},
		{"invalid eligible policy", []config.ServiceRule{invalidPolicy}, "/catalog", "/governor", "invalid"},
		{"config newline", cfg.Services, "/catalog\nnext", "/governor", "control"},
		{"governor del", cfg.Services, "/catalog", "/governor\x7f", "control"},
		{"argument tab", withArgument(cfg.Services, "bad\targ"), "/catalog", "/governor", "control"},
		{"invalid utf8", withArgument(cfg.Services, string([]byte{0xff})), "/catalog", "/governor", "utf-8"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Render(Options{Client: config.ClientCodex, ConfigPath: test.config,
				GovernorPath: test.governor, Services: test.services})
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), test.want) {
				t.Fatalf("error=%v, want %q", err, test.want)
			}
		})
	}
}

func TestRenderCodexAcceptsQuotedBackslashAndUnicodeStrings(t *testing.T) {
	service := config.ServiceRule{Name: "unicode", Command: "工具\\server", Args: []string{"quoted \"value\"", "雪"},
		Transport: config.TransportStdio, Scope: config.ScopeUser, SessionPolicy: config.SessionPolicyIsolated,
		Clients: []config.Client{config.ClientCodex}}
	got, err := Render(Options{Client: config.ClientCodex, ConfigPath: "C:\\catalog.json",
		GovernorPath: "工具\\governor", Services: []config.ServiceRule{service}})
	if err != nil {
		t.Fatal(err)
	}
	for _, quoted := range []string{`"C:\\catalog.json"`, `"工具\\governor"`, `"quoted \"value\""`, `"雪"`} {
		if !strings.Contains(string(got), quoted) {
			t.Fatalf("rendered TOML lacks %s: %s", quoted, got)
		}
	}
	var parsed map[string]any
	if _, err := toml.Decode(string(got), &parsed); err != nil {
		t.Fatalf("invalid TOML: %v\n%s", err, got)
	}
}

func jsonGolden(root, client string, withType, claudeOnly bool) string {
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
	entries := []string{entry("alpha", "/opt/mcp/alpha", []string{"serve", "--safe"})}
	if claudeOnly {
		entries = append(entries, entry("claude-only", "/opt/mcp/claude", []string{"start"}))
	}
	entries = append(entries, entry("zeta", "/opt/mcp/zeta server", []string{"--label", "quoted \"value\""}))
	return "{\n  \"" + root + "\": {\n" + strings.Join(entries, ",\n") + "\n  }\n}\n"
}

func withArgument(services []config.ServiceRule, arg string) []config.ServiceRule {
	result := append([]config.ServiceRule(nil), services...)
	result[0].Args = []string{arg}
	return result
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
