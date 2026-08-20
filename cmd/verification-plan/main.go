package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
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
	result := plan{Version: 1, Commit: commit, ManifestDigest: "sha256:" + digest, RiskLevel: risk,
		Mode: policy.Mode, LocalChecks: policy.LocalChecks, CIChecks: policy.CIChecks,
		MatchedRules: verificationplan.MatchingRules(manifest, paths)}
	return writePlan(*output, result)
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
