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
	Policy  AuthorityPolicy        `yaml:"policy"`
	Risk    RiskPolicy             `yaml:"risk"`
	Levels  map[string]LevelPolicy `yaml:"levels"`
}

type AuthorityPolicy struct {
	BrowserE2EAuthority string `yaml:"browser_e2e_authority"`
	MergeAuthority      string `yaml:"merge_authority"`
	DeploymentAuthority string `yaml:"deployment_authority"`
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
	Mode        string   `yaml:"mode"`
	LocalChecks []string `yaml:"local_checks"`
	CIChecks    []string `yaml:"ci_checks"`
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

// MatchingRules returns the IDs of every rule whose path patterns match any of
// the given paths, preserving manifest order. Consumers use it to select
// risk-triggered verification steps (e.g. the RAG retrieval spot-check)
// deterministically instead of re-deriving the file set in each skill.
func MatchingRules(manifest Manifest, paths []string) []string {
	matched := make([]string, 0, len(manifest.Risk.Rules))
	for _, rule := range manifest.Risk.Rules {
		if matchesAny(rule.Paths, paths) {
			matched = append(matched, rule.ID)
		}
	}
	return matched
}

func validate(manifest Manifest) error {
	if manifest.Version != 1 || manifest.Risk.DefaultLevel == "" || manifest.Risk.ReleaseLevel != "R4" {
		return errors.New("verification manifest policy is incomplete")
	}
	if err := validateAuthorities(manifest.Policy); err != nil {
		return err
	}
	return validateLevels(manifest.Levels)
}

func validateLevels(policies map[string]LevelPolicy) error {
	for level := range levels {
		policy, ok := policies[level]
		if !ok || policy.Mode == "" || len(policy.LocalChecks) == 0 || len(policy.CIChecks) == 0 {
			return fmt.Errorf("verification level %s is incomplete", level)
		}
		if err := rejectCIBrowserChecks(level, policy.CIChecks); err != nil {
			return err
		}
	}
	return nil
}

func validateAuthorities(policy AuthorityPolicy) error {
	if policy.BrowserE2EAuthority != "local" || policy.MergeAuthority != "ci" ||
		policy.DeploymentAuthority != "release_pipeline" {
		return errors.New("verification authorities must be local, ci, and release_pipeline")
	}
	return nil
}

func rejectCIBrowserChecks(level string, checks []string) error {
	for _, check := range checks {
		if isBrowserCheck(check) {
			return fmt.Errorf("verification level %s assigns browser check %s to CI", level, check)
		}
	}
	return nil
}

func isBrowserCheck(check string) bool {
	value := strings.ToLower(check)
	return strings.Contains(value, "browser") || strings.Contains(value, "playwright") ||
		strings.Contains(value, "chromium") || strings.HasPrefix(value, "e2e-short") ||
		strings.HasPrefix(value, "e2e-soak") || strings.HasPrefix(value, "release-soak")
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
