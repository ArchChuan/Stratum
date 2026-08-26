package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/byteBuilderX/stratum/internal/platform/verificationplan"
)

const gitCommandTimeout = 10 * time.Second

type plan struct {
	Version        int      `json:"version"`
	Commit         string   `json:"commit"`
	ManifestDigest string   `json:"manifest_digest"`
	RiskLevel      string   `json:"risk_level"`
	Mode           string   `json:"mode"`
	LocalChecks    []string `json:"local_checks"`
	CIChecks       []string `json:"ci_checks"`
	MatchedRules   []string `json:"matched_rules"`
	EvalPoints     []string `json:"eval_points,omitempty"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("verification-plan", flag.ContinueOnError)
	root := flags.String("root", ".", "repository root")
	manifestPath := flags.String("manifest", ".test/verification.yaml", "verification manifest")
	baseRef := flags.String("base-ref", "origin/main", "comparison ref")
	minimumRisk := flags.String("minimum-risk", "", "minimum risk level")
	release := flags.Bool("release", false, "classify explicit release intent")
	output := flags.String("output", "", "plan output path")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *output == "" {
		return errors.New("--output is required")
	}
	manifestFile := *root + "/" + *manifestPath
	manifest, err := verificationplan.Load(manifestFile)
	if err != nil {
		return err
	}
	paths, err := changedPaths(*root, *baseRef)
	if err != nil {
		return err
	}
	risk, err := verificationplan.Classify(manifest, paths, *minimumRisk, *release)
	if err != nil {
		return err
	}
	commit, err := gitOutput(*root, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	digest, err := fileDigest(manifestFile)
	if err != nil {
		return err
	}
	policy := manifest.Levels[risk]
	matched := verificationplan.MatchingRules(manifest, paths)
	result := plan{Version: 1, Commit: commit, ManifestDigest: "sha256:" + digest, RiskLevel: risk,
		Mode: policy.Mode, LocalChecks: policy.LocalChecks, CIChecks: slices.Clone(policy.CIChecks),
		MatchedRules: matched}
	if slices.Contains(matched, "eval-touched") {
		// The eval check is CI-owned when the deterministic eval job covers it;
		// declare it in ci_checks so CI_OWNED local runs skip rather than fail.
		result.CIChecks = appendIfMissing(result.CIChecks, "eval")
		evalPoints, err := collectEvalPoints(filepath.Join(*root, "test", "e2e"))
		if err != nil {
			return fmt.Errorf("collect eval points: %w", err)
		}
		result.EvalPoints = evalPoints
	}
	return writePlan(*output, result)
}

// collectEvalPoints walks test/e2e recursively and returns every
// points/*.yaml as <kind>/<key>. The recursive walk covers nested layouts such
// as test/e2e/knowledge/retrieval/points/retrieval.yaml; the `points` segment
// must be a middle path segment so unrelated yaml files are never picked up.
func collectEvalPoints(root string) ([]string, error) {
	var points []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".yaml") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		segs := strings.Split(filepath.ToSlash(rel), "/")
		for i := 1; i < len(segs)-1; i++ {
			if segs[i] == "points" {
				points = append(points, segs[0]+"/"+strings.TrimSuffix(segs[len(segs)-1], ".yaml"))
				break
			}
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	slices.Sort(points)
	return points, nil
}

// appendIfMissing appends value to list unless it is already present.
func appendIfMissing(list []string, value string) []string {
	for _, item := range list {
		if item == value {
			return list
		}
	}
	return append(list, value)
}

func changedPaths(root, baseRef string) ([]string, error) {
	commands := [][]string{
		{"diff", "--name-only", baseRef + "...HEAD"},
		{"diff", "--name-only", "HEAD"},
		{"ls-files", "--others", "--exclude-standard"},
	}
	paths := make(map[string]struct{})
	for _, args := range commands {
		output, err := gitOutput(root, args...)
		if err != nil {
			return nil, err
		}
		for _, path := range splitPaths(output) {
			if strings.HasPrefix(path, "test/e2e/attestations/") {
				continue
			}
			paths[path] = struct{}{}
		}
	}
	result := make([]string, 0, len(paths))
	for path := range paths {
		result = append(result, path)
	}
	return result, nil
}

func splitPaths(output string) []string {
	if output == "" {
		return nil
	}
	return strings.Split(output, "\n")
}

func gitOutput(root string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitCommandTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	command.Env = cleanGitEnvironment(os.Environ())
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(output)), nil
}

func cleanGitEnvironment(environment []string) []string {
	return slices.DeleteFunc(environment, func(value string) bool {
		return strings.HasPrefix(value, "GIT_")
	})
}

func fileDigest(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read manifest digest: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func writePlan(path string, value plan) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode verification plan: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write verification plan: %w", err)
	}
	return nil
}
