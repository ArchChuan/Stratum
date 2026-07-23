package config_test

import (
	"os"
	"testing"

	"github.com/byteBuilderX/stratum/tools/mcp-governor/internal/config"
	"github.com/byteBuilderX/stratum/tools/mcp-governor/internal/process"
)

func TestExampleConfigClassifiesReviewedCommands(t *testing.T) {
	file, err := os.Open("../../config.example.json")
	if err != nil {
		t.Fatalf("open example config: %v", err)
	}
	defer file.Close()

	cfg, err := config.Decode(file)
	if err != nil {
		t.Fatalf("Decode example config: %v", err)
	}
	rules := make([]process.Rule, len(cfg.Services))
	for i, service := range cfg.Services {
		rules[i] = process.Rule{Name: service.Name, AllArgsContain: service.AllArgsContain}
	}
	classifier, err := process.NewClassifier(rules)
	if err != nil {
		t.Fatalf("NewClassifier: %v", err)
	}

	tests := []struct {
		service string
		args    []string
	}{
		{"chroma", []string{"python", "/opt/bin/chroma-mcp", "--client-type", "persistent"}},
		{"codegraph", []string{"/opt/codegraph", "serve", "--mcp"}},
		{"code-review-graph", []string{"code-review-graph", "--mcp"}},
		{"codebase-memory", []string{"codebase-memory-mcp"}},
		{"obsidian", []string{"node", "/opt/obsidian-mcp-server"}},
		{"claude-mem", []string{"node", "/opt/claude-mem/plugin/scripts/mcp-server.cjs"}},
		{"headroom", []string{"python", "/opt/headroom", "mcp", "serve"}},
		{"playwright", []string{"node", "/opt/playwright-mcp"}},
		{"chrome-devtools", []string{"npm", "exec", "chrome-devtools-mcp@latest", "--headless"}},
		{"yinxiang", []string{"yinxiang-mcp"}},
		{"fetch", []string{"mcp-server-fetch"}},
		{"memory", []string{"mcp-server-memory"}},
		{"sequential-thinking", []string{"mcp-server-sequential-thinking"}},
		{"mcp-delve", []string{"mcp-delve"}},
		{"tokensave", []string{"tokensave-mcp"}},
		{"context7", []string{"context7-mcp"}},
		{"figma", []string{"figma-developer-mcp"}},
	}
	for _, tt := range tests {
		t.Run(tt.service, func(t *testing.T) {
			if got := classifier.Classify(process.Process{Args: tt.args}); got != tt.service {
				t.Fatalf("Classify(%q) = %q; want %q", tt.args, got, tt.service)
			}
		})
	}

	nearMisses := []struct {
		service string
		args    []string
	}{
		{"chroma", []string{"python", "/opt/bin/chroma-mcp"}},
		{"codegraph", []string{"/opt/codegraph", "serve"}},
		{"claude-mem", []string{"node", "/opt/claude-mem/plugin/worker.cjs"}},
		{"headroom", []string{"python", "/opt/headroom", "mcp"}},
		{"chrome-devtools", []string{"npm", "exec", "chrome-devtools-mcp@latest"}},
	}
	for _, tt := range nearMisses {
		t.Run(tt.service+" near miss", func(t *testing.T) {
			if got := classifier.Classify(process.Process{Args: tt.args}); got == tt.service {
				t.Fatalf("Classify(%q) = %q; omitted required fragment still matched", tt.args, got)
			}
		})
	}
}
