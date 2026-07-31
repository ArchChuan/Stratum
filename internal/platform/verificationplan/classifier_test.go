package verificationplan

import (
	"testing"

	"github.com/stretchr/testify/require"
)

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
