package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// point is a single configuration snapshot: the resource config, the golden
// dataset it is evaluated against, and the baseline it regresses against.
type point struct {
	Kind     string         `yaml:"kind"`
	Key      string         `yaml:"point"`
	Snapshot map[string]any `yaml:"snapshot"`
	Golden   string         `yaml:"golden"`
	Baseline string         `yaml:"baseline"`
	Judge    *judgeConfig   `yaml:"judge,omitempty"`
	Path     string         `yaml:"-"`
	Dir      string         `yaml:"-"`
}

// judgeConfig declares the real-LLM judge endpoint used by skill/agent kinds.
type judgeConfig struct {
	BaseURL   string `yaml:"base_url"`
	Model     string `yaml:"model"`
	APIKeyEnv string `yaml:"api_key_env"`
}

// judgeSpec is the per-case LLM judging specification used by skill/agent
// golden cases. Defined here (next to judgeConfig) so the shared goldenCase
// can carry it from its first definition in Task 2; judge.go (Task 4)
// consumes it. Only one judgeSpec type exists — no judgeSpecYAML variant.
//
//nolint:unused // consumed by judge.go (Task 4)
type judgeSpec struct {
	Criteria string `yaml:"criteria" json:"criteria"`
}

// loadPoint reads and validates a point file. golden and baseline paths are
// resolved relative to the point file's directory (existing test/e2e layout
// nests golden/ and baselines/ beside points/).
func loadPoint(path string) (point, error) {
	var p point
	data, err := os.ReadFile(path)
	if err != nil {
		return p, fmt.Errorf("read point %s: %w", path, err)
	}
	if err := yaml.Unmarshal(data, &p); err != nil {
		return p, fmt.Errorf("decode point %s: %w", path, err)
	}
	switch p.Kind {
	case "skill", "agent", "mcp", "knowledge":
	default:
		return p, fmt.Errorf("point %s: unsupported kind %q", path, p.Kind)
	}
	if strings.TrimSpace(p.Key) == "" {
		return p, errors.New("point: field point is required")
	}
	if p.Golden == "" || p.Baseline == "" {
		return p, errors.New("point: golden and baseline are required")
	}
	p.Path = path
	p.Dir = filepath.Dir(path)
	return p, nil
}

// resolveRelative resolves a point-relative path, falling back to the repo
// root for legacy layouts that reference paths from the repository top.
func resolveRelative(baseDir, ref string) (string, error) {
	joined := filepath.Join(baseDir, ref)
	if _, err := os.Stat(joined); err == nil {
		return joined, nil
	}
	if abs, err := filepath.Abs(ref); err == nil {
		if _, statErr := os.Stat(abs); statErr == nil {
			return abs, nil
		}
	}
	return joined, nil
}
