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

func TestCollectEvalPointsSupportsNestedLayout(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTestFile(t, root, "knowledge/retrieval/points/retrieval.yaml", "kind: knowledge\n")
	writeTestFile(t, root, "knowledge/retrieval/golden/cases.yaml", "cases:\n")
	writeTestFile(t, root, "mcp/points/weather-mcp.yaml", "kind: mcp\n")
	writeTestFile(t, root, "agent/points/planner-agent.yaml", "kind: agent\n")
	writeTestFile(t, root, "attestations/run/result.yaml", "irrelevant: true\n")

	points, err := collectEvalPoints(root)
	require.NoError(t, err)
	require.Equal(t, []string{"agent/planner-agent", "knowledge/retrieval", "mcp/weather-mcp"}, points)
}

func TestCollectEvalPointsMissingRootYieldsEmpty(t *testing.T) {
	t.Parallel()
	points, err := collectEvalPoints(filepath.Join(t.TempDir(), "does-not-exist"))
	require.NoError(t, err)
	require.Empty(t, points)
}

func TestAppendIfMissing(t *testing.T) {
	t.Parallel()
	require.Equal(t, []string{"unit", "eval"}, appendIfMissing([]string{"unit"}, "eval"))
	require.Equal(t, []string{"eval"}, appendIfMissing([]string{"eval"}, "eval"))
}

func TestRunAddsEvalPointsAndCICheckWhenEvalTouched(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "Test")
	require.NoError(t, os.WriteFile(filepath.Join(root, "README.md"), []byte("hello\n"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".test"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".test", "verification.yaml"), []byte(minimalEvalManifest()), 0o600))
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-m", "initial")

	// 未跟踪的嵌套 eval point 触发 eval-touched（changedPaths 含 ls-files --others）
	pointPath := filepath.Join(root, "test", "e2e", "knowledge", "retrieval", "points", "retrieval.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(pointPath), 0o700))
	require.NoError(t, os.WriteFile(pointPath, []byte("kind: knowledge\npoint: retrieval\n"), 0o600))

	output := filepath.Join(root, "plan.json")
	require.NoError(t, run([]string{"--root", root, "--base-ref", "HEAD", "--output", output}))
	raw, err := os.ReadFile(output)
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Contains(t, got["matched_rules"], "eval-touched")
	require.Contains(t, got["eval_points"], "knowledge/retrieval")
	require.Contains(t, got["ci_checks"], "eval")
}

func writeTestFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

// minimalEvalManifest passes verificationplan.Load validation and declares an
// eval-touched rule so the plan assembly test can trigger eval_points wiring.
func minimalEvalManifest() string {
	return `version: 1
policy:
  browser_e2e_authority: local
  merge_authority: ci
  deployment_authority: release_pipeline
  fail_closed: true
  default_mode: short
risk:
  default_level: R2
  release_level: R4
  rules:
    - id: eval-touched
      level: R3
      paths: [test/e2e/**]
levels:
  R0:
    mode: none
    local_checks: [docs-lint]
    ci_checks: [static]
  R1:
    mode: none
    local_checks: [static]
    ci_checks: [static]
  R2:
    mode: short
    local_checks: [static, unit]
    ci_checks: [static, unit]
  R3:
    mode: soak
    local_checks: [static, unit]
    ci_checks: [static, unit]
  R4:
    mode: release-soak
    local_checks: [static, unit]
    ci_checks: [static, unit]
`
}

func runGit(t *testing.T, root string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	command.Env = cleanGitEnvironment(os.Environ())
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
}
