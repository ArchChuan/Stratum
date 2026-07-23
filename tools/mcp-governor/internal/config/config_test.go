package config

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
)

const validConfig = `{
  "version": 1,
  "output_path": "%h/out.json",
  "registry_path": "%h/registry.json",
  "services": [{"name":"chroma","all_args_contain":["chroma-mcp"]}]
}`

const validVersion2Config = `{
  "version": 2,
  "output_path": "%h/out.json",
  "registry_path": "%h/registry.json",
  "observation": {
    "events_dir": "%h/events",
    "reports_dir": "%h/reports",
    "salt_path": "%h/salt",
    "raw_retention_days": 14
  },
  "services": [{
    "name": "chroma",
    "command": "uvx",
    "args": ["chroma-mcp"],
    "cwd": "%h",
    "transport": "stdio",
    "scope": "user",
    "session_policy": "isolated",
    "clients": ["codex", "claude", "vscode", "lingma"],
    "all_args_contain": ["chroma-mcp"]
  }]
}`

func TestDecodeValidConfig(t *testing.T) {
	cfg, err := Decode(strings.NewReader(validConfig))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if cfg.Version != 1 || cfg.OutputPath != "%h/out.json" || len(cfg.Services) != 1 {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	service := cfg.Services[0]
	if service.Transport != TransportStdio || service.Scope != ScopeUser ||
		service.SessionPolicy != SessionPolicyIsolated ||
		!slices.Equal(service.Clients, []Client{ClientCodex, ClientClaude, ClientVSCode, ClientLingma}) {
		t.Fatalf("version 1 compatibility defaults = %+v", service)
	}
}

func TestDecodeVersion2(t *testing.T) {
	cfg, err := Decode(strings.NewReader(validVersion2Config))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if cfg.Observation.RawRetentionDays != 14 || cfg.Services[0].Command != "uvx" ||
		cfg.Services[0].Transport != TransportStdio {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestDecodeVersion2RejectsInvalidOrMissingEnums(t *testing.T) {
	tests := []struct {
		name, old, replacement, want string
	}{
		{"missing transport", `"transport": "stdio",`, ``, "transport"},
		{"invalid transport", `"transport": "stdio"`, `"transport": "socket"`, "transport"},
		{"missing scope", `"scope": "user",`, ``, "scope"},
		{"invalid scope", `"scope": "user"`, `"scope": "global"`, "scope"},
		{"missing session policy", `"session_policy": "isolated",`, ``, "session_policy"},
		{"invalid session policy", `"session_policy": "isolated"`, `"session_policy": "shared"`, "session_policy"},
		{"missing clients", `"clients": ["codex", "claude", "vscode", "lingma"],`, ``, "clients"},
		{"invalid client", `"codex"`, `"other"`, "clients"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertDecodeError(t, strings.Replace(validVersion2Config, tt.old, tt.replacement, 1), tt.want)
		})
	}
}

func TestDecodeVersion2RejectsDuplicateClients(t *testing.T) {
	input := strings.Replace(validVersion2Config, `"codex", "claude"`, `"codex", "codex"`, 1)
	assertDecodeError(t, input, "duplicate")
}

func TestDecodeVersion2ObservationValidation(t *testing.T) {
	for _, field := range []string{"events_dir", "reports_dir", "salt_path"} {
		t.Run("missing "+field, func(t *testing.T) {
			input := strings.Replace(validVersion2Config, `"`+field+`": "%h/`+map[string]string{
				"events_dir": "events", "reports_dir": "reports", "salt_path": "salt",
			}[field]+`"`, `"`+field+`": ""`, 1)
			assertDecodeError(t, input, field)
		})
	}
	for _, retention := range []int{6, 91} {
		t.Run(fmt.Sprintf("retention %d", retention), func(t *testing.T) {
			input := strings.Replace(validVersion2Config, `"raw_retention_days": 14`,
				fmt.Sprintf(`"raw_retention_days": %d`, retention), 1)
			assertDecodeError(t, input, "raw_retention_days")
		})
	}
	for _, retention := range []int{7, 90} {
		t.Run(fmt.Sprintf("retention boundary %d", retention), func(t *testing.T) {
			input := strings.Replace(validVersion2Config, `"raw_retention_days": 14`,
				fmt.Sprintf(`"raw_retention_days": %d`, retention), 1)
			if _, err := Decode(strings.NewReader(input)); err != nil {
				t.Fatalf("Decode: %v", err)
			}
		})
	}
}

func TestDecodeVersion2RejectsMissingCommand(t *testing.T) {
	assertDecodeError(t, strings.Replace(validVersion2Config, `"command": "uvx"`, `"command": ""`, 1), "command")
}

func TestDecodeVersion2ScopeSessionPolicy(t *testing.T) {
	tests := []struct {
		name, scope, policy string
	}{
		{"repository must be isolated", "repository", "session_local"},
		{"session must be local", "session", "isolated"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := strings.Replace(validVersion2Config, `"scope": "user"`, `"scope": "`+tt.scope+`"`, 1)
			input = strings.Replace(input, `"session_policy": "isolated"`, `"session_policy": "`+tt.policy+`"`, 1)
			assertDecodeError(t, input, "session_policy")
		})
	}
}

func TestDecodeVersion2RejectsInlineCredentials(t *testing.T) {
	for _, arg := range []string{"--token=value", "PASSWORD=value", "api_key=value", "--Api-Key=value"} {
		t.Run(arg, func(t *testing.T) {
			input := strings.Replace(validVersion2Config, `"args": ["chroma-mcp"]`,
				`"args": ["chroma-mcp", "`+arg+`"]`, 1)
			_, err := Decode(strings.NewReader(input))
			if err == nil {
				t.Fatal("Decode succeeded; want error")
			}
			if !strings.Contains(err.Error(), "services[0] args[1]") {
				t.Fatalf("error %q does not identify service/arg index", err)
			}
			if strings.Contains(err.Error(), arg) {
				t.Fatalf("error %q echoes credential argument", err)
			}
		})
	}
	for _, arg := range []string{
		"--token", "--notsecret=value", "--mytoken=value", "prefix-api_key=value", "template_api_key=value",
	} {
		t.Run("allows "+arg, func(t *testing.T) {
			input := strings.Replace(validVersion2Config, `"args": ["chroma-mcp"]`,
				`"args": ["chroma-mcp", "`+arg+`"]`, 1)
			if _, err := Decode(strings.NewReader(input)); err != nil {
				t.Fatalf("benign argument rejected: %v", err)
			}
		})
	}
}

func TestDecodeVersion1RejectsVersion2Fields(t *testing.T) {
	observation := `"observation":{"events_dir":"x","reports_dir":"x","salt_path":"x","raw_retention_days":14},`
	tests := []struct {
		name, input, want string
	}{
		{"observation", strings.Replace(validConfig, `"services":`, observation+`"services":`, 1), "observation"},
	}
	serviceFields := []struct {
		name, value string
	}{
		{"command", `"uvx"`},
		{"args", `[]`},
		{"cwd", `"%h"`},
		{"transport", `"stdio"`},
		{"scope", `"user"`},
		{"session_policy", `"isolated"`},
		{"clients", `["codex"]`},
	}
	for _, field := range serviceFields {
		input := strings.Replace(validConfig, `"name":"chroma"`,
			`"name":"chroma","`+field.name+`":`+field.value, 1)
		tests = append(tests, struct {
			name, input, want string
		}{field.name, input, field.name})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertDecodeError(t, tt.input, "unknown field")
			assertDecodeError(t, tt.input, tt.want)
		})
	}
}

func TestExampleConfigContract(t *testing.T) {
	file, err := os.Open("../../config.example.json")
	if err != nil {
		t.Fatalf("open example config: %v", err)
	}
	defer file.Close()

	cfg, err := Decode(file)
	if err != nil {
		t.Fatalf("Decode example config: %v", err)
	}
	wantNames := []string{
		"chroma", "codegraph", "code-review-graph", "codebase-memory", "obsidian", "claude-mem", "headroom",
		"playwright", "chrome-devtools", "yinxiang", "fetch", "memory", "sequential-thinking", "mcp-delve",
		"tokensave", "context7", "figma",
	}
	if len(cfg.Services) != len(wantNames) {
		t.Fatalf("service count = %d; want %d", len(cfg.Services), len(wantNames))
	}

	canonical := make([][]string, len(cfg.Services))
	for i, service := range cfg.Services {
		if service.Name != wantNames[i] {
			t.Errorf("services[%d].name = %q; want %q", i, service.Name, wantNames[i])
		}
		if len(service.AllArgsContain) == 0 {
			t.Fatalf("services[%d].all_args_contain is empty", i)
		}
		canonical[i] = slices.Clone(service.AllArgsContain)
		slices.Sort(canonical[i])
		for j := range i {
			if slices.Equal(canonical[i], canonical[j]) {
				t.Errorf("services %q and %q have identical matcher sets", cfg.Services[j].Name, service.Name)
			}
		}
	}
}

func TestDecodeRejectsUnknownField(t *testing.T) {
	assertDecodeError(t, strings.Replace(validConfig, `"version": 1`, `"version": 1, "extra": true`, 1), "unknown field")
}

func TestDecodeRejectsDuplicateJSONKeys(t *testing.T) {
	tests := []struct {
		name  string
		input string
		key   string
	}{
		{"top-level version", strings.Replace(validConfig, `"version": 1`, `"version": 1, "version": 1`, 1), "version"},
		{"top-level output path", strings.Replace(validConfig, `"output_path": "%h/out.json"`, `"output_path": "%h/out.json", "output_path": "%h/other.json"`, 1), "output_path"},
		{"service name", strings.Replace(validConfig, `"name":"chroma"`, `"name":"chroma","name":"other"`, 1), "name"},
		{"service fragments", strings.Replace(validConfig, `"all_args_contain":["chroma-mcp"]`, `"all_args_contain":["chroma-mcp"],"all_args_contain":["other"]`, 1), "all_args_contain"},
		{"case-aliased version", strings.Replace(validConfig, `"version": 1`, `"version": 1, "Version": 1`, 1), "Version"},
		{"case-aliased service name", strings.Replace(validConfig, `"name":"chroma"`, `"name":"chroma","Name":"other"`, 1), "Name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertDecodeError(t, tt.input, "duplicate key")
			assertDecodeError(t, tt.input, tt.key)
		})
	}
}

func TestDecodeRejectsTrailingJSON(t *testing.T) {
	assertDecodeError(t, validConfig+` {}`, "trailing")
}

func TestDecodeRejectsUnsupportedVersion(t *testing.T) {
	assertDecodeError(t, strings.Replace(validConfig, `"version": 1`, `"version": 3`, 1), "version")
}

func TestDecodeRejectsMissingPaths(t *testing.T) {
	for _, field := range []string{"output_path", "registry_path"} {
		t.Run(field, func(t *testing.T) {
			input := strings.Replace(validConfig, `"`+field+`": "%h/`+map[string]string{"output_path": "out", "registry_path": "registry"}[field]+`.json"`, `"`+field+`": ""`, 1)
			assertDecodeError(t, input, field)
		})
	}
}

func TestDecodeRejectsEmptyServices(t *testing.T) {
	input := strings.Replace(validConfig, `[{"name":"chroma","all_args_contain":["chroma-mcp"]}]`, `[]`, 1)
	assertDecodeError(t, input, "services")
}

func TestDecodeRejectsInvalidServiceRules(t *testing.T) {
	tests := []struct {
		name, services, want string
	}{
		{"empty name", `[{"name":"","all_args_contain":["x"]}]`, "name"},
		{"duplicate name", `[{"name":"x","all_args_contain":["a"]},{"name":"x","all_args_contain":["b"]}]`, "duplicate"},
		{"empty fragments", `[{"name":"x","all_args_contain":[]}]`, "all_args_contain"},
		{"empty fragment", `[{"name":"x","all_args_contain":[""]}]`, "fragment"},
		{"duplicate fragment", `[{"name":"x","all_args_contain":["a","a"]}]`, "duplicate"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := strings.Index(validConfig, `[{"name"`)
			end := strings.LastIndex(validConfig, `]`)
			assertDecodeError(t, validConfig[:start]+tt.services+validConfig[end+1:], tt.want)
		})
	}
}

func assertDecodeError(t *testing.T, input, contains string) {
	t.Helper()
	_, err := Decode(strings.NewReader(input))
	if err == nil {
		t.Fatal("Decode succeeded; want error")
	}
	if !strings.Contains(err.Error(), contains) {
		t.Fatalf("error %q does not contain %q", err, contains)
	}
}
