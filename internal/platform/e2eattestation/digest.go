package e2eattestation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const attestationOutputPrefix = "test/e2e/attestations/"
const gitCommandTimeout = 30 * time.Second

func LocalSourceDigest(root string) (string, error) {
	paths, err := gitNullPaths(root, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return "", err
	}
	deleted, err := gitNullPaths(root, "ls-files", "-z", "--deleted")
	if err != nil {
		return "", err
	}
	deletedSet := make(map[string]struct{}, len(deleted))
	for _, path := range deleted {
		deletedSet[path] = struct{}{}
	}
	workingPaths := paths[:0]
	for _, path := range paths {
		if _, removed := deletedSet[path]; removed {
			continue
		}
		fullPath := filepath.Join(root, filepath.FromSlash(path))
		if info, err := os.Stat(fullPath); err == nil && info != nil && info.IsDir() {
			continue
		}
		workingPaths = append(workingPaths, path)
	}
	return digestPaths(workingPaths, func(path string) ([]byte, error) {
		content, readErr := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if readErr != nil {
			return nil, fmt.Errorf("read source %q: %w", path, readErr)
		}
		return content, nil
	})
}

func CommittedSourceDigest(root, ref string) (string, error) {
	if strings.TrimSpace(ref) == "" {
		return "", fmt.Errorf("git ref is required")
	}
	paths, err := gitNullPaths(root, "ls-tree", "-rz", "--name-only", "--full-tree", ref)
	if err != nil {
		return "", err
	}
	return digestPaths(paths, func(path string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), gitCommandTimeout)
		defer cancel()
		command := exec.CommandContext(ctx, "git", "show", ref+":"+path)
		command.Dir = root
		content, showErr := command.Output()
		if showErr != nil {
			return nil, fmt.Errorf("read committed source %q: %w", path, showErr)
		}
		return content, nil
	})
}

func gitNullPaths(root string, args ...string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), gitCommandTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	parts := bytes.Split(output, []byte{0})
	paths := make([]string, 0, len(parts))
	for _, part := range parts {
		path := string(part)
		if path == "" || strings.HasPrefix(path, attestationOutputPrefix) {
			continue
		}
		paths = append(paths, path)
	}
	return paths, nil
}

func digestPaths(paths []string, read func(string) ([]byte, error)) (string, error) {
	sort.Strings(paths)
	hash := sha256.New()
	for _, path := range paths {
		content, err := read(path)
		if err != nil {
			return "", err
		}
		_, _ = hash.Write([]byte(path))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write(content)
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
