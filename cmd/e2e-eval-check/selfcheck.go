package main

import (
	"fmt"
	"io"
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
			p, err := loadPoint(path)
			if err != nil {
				_, _ = fmt.Fprintf(stdout, "POINT FAIL %s: %v\n", path, err)
				failed++
				continue
			}
			goldenPath, err := resolveRelative(p.Dir, p.Golden)
			if err != nil {
				_, _ = fmt.Fprintf(stdout, "GOLDEN FAIL %s: %v\n", path, err)
				failed++
				continue
			}
			_, _ = fmt.Fprintf(stdout, "POINT OK %s -> golden %s\n", path, goldenPath)
		}
	}
	if failed > 0 {
		_, _ = fmt.Fprintf(stdout, "self-check failed for %d point(s)\n", failed)
		return exitFailed, nil
	}
	_, _ = fmt.Fprintln(stdout, "self-check passed")
	return exitPassed, nil
}
