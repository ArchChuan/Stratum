package verificationplan

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

var levels = map[string]int{"R0": 0, "R1": 1, "R2": 2, "R3": 3, "R4": 4}

type Manifest struct {
	Version int                    `yaml:"version"`
	Risk    RiskPolicy             `yaml:"risk"`
	Levels  map[string]LevelPolicy `yaml:"levels"`
	Reviews ReviewPolicy           `yaml:"reviews"`
}

type RiskPolicy struct {
	DefaultLevel string     `yaml:"default_level"`
	ReleaseLevel string     `yaml:"release_level"`
	Rules        []RiskRule `yaml:"rules"`
}

type RiskRule struct {
	ID    string   `yaml:"id"`
	Level string   `yaml:"level"`
	Paths []string `yaml:"paths"`
}

type LevelPolicy struct {
	Mode   string   `yaml:"mode"`
	Checks []string `yaml:"checks"`
}

type ReviewPolicy struct {
	Required map[string][]string `yaml:"required"`
}

func Load(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("read verification manifest: %w", err)
	}
	var manifest Manifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode verification manifest: %w", err)
	}
	if err := validate(manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func Classify(manifest Manifest, paths []string, minimum string, release bool) (string, error) {
	effective := "R0"
	matched := false
	for _, rule := range manifest.Risk.Rules {
		if !matchesAny(rule.Paths, paths) {
			continue
		}
		matched = true
		if levels[rule.Level] > levels[effective] {
			effective = rule.Level
		}
	}
	if len(paths) > 0 && !matched {
		effective = manifest.Risk.DefaultLevel
	}
	for _, candidate := range []string{minimum, releaseLevel(manifest, release)} {
		if candidate == "" {
			continue
		}
		if _, ok := levels[candidate]; !ok {
			return "", fmt.Errorf("unsupported verification risk level: %s", candidate)
		}
		if levels[candidate] > levels[effective] {
			effective = candidate
		}
	}
	return effective, nil
}

func validate(manifest Manifest) error {
	if manifest.Version != 1 || manifest.Risk.DefaultLevel == "" || manifest.Risk.ReleaseLevel != "R4" {
		return errors.New("verification manifest policy is incomplete")
	}
	for level := range levels {
		policy, ok := manifest.Levels[level]
		if !ok || policy.Mode == "" || len(policy.Checks) == 0 {
			return fmt.Errorf("verification level %s is incomplete", level)
		}
	}
	return nil
}

func matchesAny(patterns, paths []string) bool {
	for _, pattern := range patterns {
		for _, path := range paths {
			if matchGlob(pattern, path) {
				return true
			}
		}
	}
	return false
}

func matchGlob(pattern, path string) bool {
	quoted := regexp.QuoteMeta(pattern)
	quoted = strings.ReplaceAll(quoted, `\*\*`, `.*`)
	quoted = strings.ReplaceAll(quoted, `\*`, `[^/]*`)
	return regexp.MustCompile("^" + quoted + "$").MatchString(path)
}

func releaseLevel(manifest Manifest, release bool) string {
	if release {
		return manifest.Risk.ReleaseLevel
	}
	return ""
}
