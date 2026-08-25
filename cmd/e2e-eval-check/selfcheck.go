package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// selfCheck validates every point and its referenced golden wiring without
// touching the network. With --kind it scans that kind's points directory;
// without it, it scans all four kinds. CI uses it as the deterministic gate
// for kinds that need real LLMs or heavy infra, and as a sanity layer for all
// kinds before the real executors run. Per-kind dataset loading/validation is
// layered in by Tasks 2-5; this skeleton confirms point wiring resolution.
func selfCheck(o options, stdout io.Writer) (int, error) {
	kinds := []string{o.kind}
	if o.kind == "" {
		kinds = []string{"skill", "agent", "mcp", "knowledge"}
	}
	failed := 0
	for _, kind := range kinds {
		pointsDir := filepath.Join("test", "e2e", kind, "points")
		entries, err := filepath.Glob(filepath.Join(pointsDir, "*.yaml"))
		if err != nil {
			return exitInfraFailed, fmt.Errorf("glob points %s: %w", kind, err)
		}
		if len(entries) == 0 {
			_, _ = fmt.Fprintf(stdout, "kind=%s no points (nothing to self-check)\n", kind)
			continue
		}
		for _, path := range entries {
			if selfCheckPoint(path, stdout) {
				failed++
			}
		}
	}
	if failed > 0 {
		_, _ = fmt.Fprintf(stdout, "self-check failed for %d point(s)\n", failed)
		return exitFailed, nil
	}
	_, _ = fmt.Fprintln(stdout, "self-check passed")
	return exitPassed, nil
}

// selfCheckPoint validates one point's golden wiring without touching the
// network. It reports POINT OK on success and POINT FAIL / GOLDEN FAIL on
// failure, returning whether the point failed. The golden path is verified to
// exist on disk so a missing golden file fails the self-check instead of being
// silently accepted.
func selfCheckPoint(path string, stdout io.Writer) bool {
	p, err := loadPoint(path)
	if err != nil {
		_, _ = fmt.Fprintf(stdout, "POINT FAIL %s: %v\n", path, err)
		return true
	}
	goldenPath, err := resolveRelative(p.Dir, p.Golden)
	if err != nil {
		_, _ = fmt.Fprintf(stdout, "GOLDEN FAIL %s: %v\n", path, err)
		return true
	}
	if _, err := os.Stat(goldenPath); err != nil {
		_, _ = fmt.Fprintf(stdout, "GOLDEN FAIL %s: golden %s not found: %v\n", path, goldenPath, err)
		return true
	}
	_, _ = fmt.Fprintf(stdout, "POINT OK %s -> golden %s\n", path, goldenPath)
	return false
}
