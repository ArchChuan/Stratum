package config

import (
	"strings"
	"testing"
)

const validConfig = `{
  "version": 1,
  "output_path": "%h/out.json",
  "registry_path": "%h/registry.json",
  "services": [{"name":"chroma","all_args_contain":["chroma-mcp"]}]
}`

func TestDecodeValidConfig(t *testing.T) {
	cfg, err := Decode(strings.NewReader(validConfig))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if cfg.Version != 1 || cfg.OutputPath != "%h/out.json" || len(cfg.Services) != 1 {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestDecodeRejectsUnknownField(t *testing.T) {
	assertDecodeError(t, strings.Replace(validConfig, `"version": 1`, `"version": 1, "extra": true`, 1), "unknown field")
}

func TestDecodeRejectsTrailingJSON(t *testing.T) {
	assertDecodeError(t, validConfig+` {}`, "trailing")
}

func TestDecodeRejectsUnsupportedVersion(t *testing.T) {
	assertDecodeError(t, strings.Replace(validConfig, `"version": 1`, `"version": 2`, 1), "version")
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
