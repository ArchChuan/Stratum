package verificationplan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadValidatesVerificationAuthorities(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		policy  string
		levels  string
		wantErr string
	}{
		{
			name:   "accepts separate authorities",
			policy: validAuthorityPolicy(),
			levels: validLevelPolicies(),
		},
		{
			name:    "rejects missing local browser authority",
			policy:  "  merge_authority: ci\n  deployment_authority: release_pipeline\n",
			levels:  validLevelPolicies(),
			wantErr: "verification authorities",
		},
		{
			name:    "rejects browser check owned by CI",
			policy:  validAuthorityPolicy(),
			levels:  strings.Replace(validLevelPolicies(), "ci_checks: [static]", "ci_checks: [e2e-short]", 1),
			wantErr: "browser check",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeManifest(t, tt.policy, tt.levels)
			_, err := Load(path)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestClassifyUsesManifestRulesAndCannotLowerRisk(t *testing.T) {
	t.Parallel()
	manifest := Manifest{
		Risk: RiskPolicy{DefaultLevel: "R2", ReleaseLevel: "R4", Rules: []RiskRule{
			{ID: "docs", Level: "R0", Paths: []string{"docs/**"}},
			{ID: "auth", Level: "R3", Paths: []string{"internal/iam/**"}},
		}},
	}
	tests := []struct {
		name, minimum string
		paths         []string
		release       bool
		want          string
	}{
		{name: "docs", paths: []string{"docs/readme.md"}, want: "R0"},
		{name: "default executable", paths: []string{"web/src/app.tsx"}, want: "R2"},
		{name: "highest matching rule", paths: []string{"docs/readme.md", "internal/iam/auth.go"}, want: "R3"},
		{name: "minimum raises", paths: []string{"docs/readme.md"}, minimum: "R1", want: "R1"},
		{name: "minimum cannot lower", paths: []string{"internal/iam/auth.go"}, minimum: "R1", want: "R3"},
		{name: "release intent", paths: []string{"docs/readme.md"}, release: true, want: "R4"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Classify(manifest, tt.paths, tt.minimum, tt.release)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestMatchGlobSupportsRecursivePaths(t *testing.T) {
	t.Parallel()
	require.True(t, matchGlob("internal/**/persistence/**", "internal/agent/infrastructure/persistence/store.go"))
	require.False(t, matchGlob("docs/**", "internal/docs/service.go"))
}

func TestMatchingRulesPreservesManifestOrder(t *testing.T) {
	t.Parallel()
	manifest := Manifest{
		Risk: RiskPolicy{Rules: []RiskRule{
			{ID: "docs", Level: "R0", Paths: []string{"docs/**"}},
			{ID: "auth", Level: "R3", Paths: []string{"internal/iam/**"}},
			{ID: "memory", Level: "R3", Paths: []string{"internal/memory/**"}},
		}},
	}
	require.Equal(t, []string{"docs", "memory"},
		MatchingRules(manifest, []string{"docs/readme.md", "internal/memory/store.go"}))
	require.Equal(t, []string{"auth"},
		MatchingRules(manifest, []string{"internal/iam/auth.go"}))
	require.Empty(t, MatchingRules(manifest, []string{"internal/unknown/thing.go"}))
	require.Empty(t, MatchingRules(manifest, nil))
}

func validAuthorityPolicy() string {
	return "  browser_e2e_authority: local\n  merge_authority: ci\n  deployment_authority: release_pipeline\n"
}

func validLevelPolicies() string {
	return `  R0: {mode: none, local_checks: [docs-lint], ci_checks: [static]}
  R1: {mode: none, local_checks: [static], ci_checks: [static]}
  R2: {mode: short, local_checks: [static, e2e-short], ci_checks: [static]}
  R3: {mode: soak, local_checks: [static, e2e-short, e2e-soak], ci_checks: [static]}
  R4: {mode: release-soak, local_checks: [static, e2e-short, e2e-soak, release-soak], ci_checks: [static]}
`
}

func writeManifest(t *testing.T, policy, levelPolicies string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "verification.yaml")
	content := "version: 1\npolicy:\n" + policy + `risk:
  default_level: R2
  release_level: R4
  rules:
    - id: docs
      level: R0
      paths: [docs/**]
levels:
` + levelPolicies
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}
