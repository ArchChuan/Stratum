package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWritePlanSeparatesLocalAndCIChecks(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "plan.json")
	value := plan{
		Version: 1, Commit: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ManifestDigest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		RiskLevel:      "R3", Mode: "soak", LocalChecks: []string{"e2e-short", "e2e-soak"},
		CIChecks: []string{"static", "unit"},
	}

	require.NoError(t, writePlan(path, value))
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Equal(t, []any{"e2e-short", "e2e-soak"}, got["local_checks"])
	require.Equal(t, []any{"static", "unit"}, got["ci_checks"])
	require.NotContains(t, got, "checks")
	require.NotContains(t, got, "reviews")
}

func TestChangedPathsIncludesWorkingTreeChanges(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "Test")
	require.NoError(t, os.WriteFile(filepath.Join(root, "tracked.go"), []byte("package tracked\n"), 0o600))
	runGit(t, root, "add", "tracked.go")
	runGit(t, root, "commit", "-m", "initial")
	require.NoError(t, os.WriteFile(filepath.Join(root, "tracked.go"), []byte("package changed\n"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "internal", "iam"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "internal", "iam", "new.go"), []byte("package iam\n"), 0o600))

	paths, err := changedPaths(root, "HEAD")
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"tracked.go", "internal/iam/new.go"}, paths)
}

func runGit(t *testing.T, root string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	command.Env = cleanGitEnvironment(os.Environ())
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
}
