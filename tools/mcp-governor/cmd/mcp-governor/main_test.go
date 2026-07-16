package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/tools/mcp-governor/internal/process"
)

var fixedTime = time.Date(2026, 7, 16, 10, 11, 12, 0, time.UTC)

func TestRunSnapshotWritesDeterministicJSONToStdout(t *testing.T) {
	root := t.TempDir()
	writeProcessFixture(t, root, "42", []string{"node", "chroma-mcp", "--client-type", "stdio"})
	configPath := writeConfig(t, root, filepath.Join(root, "missing-registry.json"), "ignored.json")
	useDependencies(t, fixedTime, "/home/tester", nil)

	var stdout, stderr bytes.Buffer
	if code := run([]string{"snapshot", "--config", configPath, "--proc-root", root, "--output", "-"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run returned %d, stderr=%q", code, stderr.String())
	}
	var snapshot process.Snapshot
	if err := json.Unmarshal(stdout.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode output: %v\n%s", err, stdout.String())
	}
	if snapshot.Version != 1 || snapshot.Mode != "observe" || !snapshot.CapturedAt.Equal(fixedTime) {
		t.Fatalf("unexpected metadata: %+v", snapshot)
	}
	if len(snapshot.Processes) != 1 || snapshot.Processes[0].Service != "chroma" {
		t.Fatalf("unexpected processes: %+v", snapshot.Processes)
	}
	if !strings.HasSuffix(stdout.String(), "\n") || stderr.Len() != 0 {
		t.Fatalf("stdout newline=%v stderr=%q", strings.HasSuffix(stdout.String(), "\n"), stderr.String())
	}
}

func TestRunRejectsInvalidInvocation(t *testing.T) {
	tests := [][]string{
		nil,
		{"unknown"},
		{"snapshot"},
		{"snapshot", "--config"},
		{"snapshot", "--config", "x", "--unknown"},
		{"snapshot", "--config", "x", "extra"},
	}
	for _, args := range tests {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			if code := run(args, &bytes.Buffer{}, &bytes.Buffer{}); code != 2 {
				t.Fatalf("run(%q) = %d, want 2", args, code)
			}
		})
	}
}

func TestRunReportsConfigAndProcRootErrors(t *testing.T) {
	root := t.TempDir()
	malformed := filepath.Join(root, "bad.json")
	writeFile(t, malformed, []byte(`{"version": 1}`), 0o600)
	for _, args := range [][]string{
		{"snapshot", "--config", filepath.Join(root, "absent.json"), "--output", "-"},
		{"snapshot", "--config", malformed, "--output", "-"},
		{"snapshot", "--config", writeConfig(t, root, filepath.Join(root, "registry"), "out"), "--proc-root", filepath.Join(root, "no-proc"), "--output", "-"},
	} {
		if code := run(args, &bytes.Buffer{}, &bytes.Buffer{}); code != 1 {
			t.Fatalf("run(%q) = %d, want 1", args, code)
		}
	}
}

func TestRunRegistryHandling(t *testing.T) {
	root := t.TempDir()
	writeProcessFixture(t, root, "42", []string{"chroma-mcp", "--client-type"})
	registry := filepath.Join(root, "registry.json")
	configPath := writeConfig(t, root, registry, "ignored")
	useDependencies(t, fixedTime, "/home/tester", nil)

	if code := run([]string{"snapshot", "--config", configPath, "--proc-root", root, "--output", "-"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("absent registry returned %d", code)
	}
	writeFile(t, registry, []byte(`{"version":1,"registrations":`), 0o600)
	if code := run([]string{"snapshot", "--config", configPath, "--proc-root", root, "--output", "-"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 1 {
		t.Fatalf("malformed registry returned %d, want 1", code)
	}
}

func TestRunSkipsGoneAndWarnsForMalformedProcess(t *testing.T) {
	root := t.TempDir()
	writeProcessFixture(t, root, "42", []string{"chroma-mcp", "--client-type"})
	writeProcessFixture(t, root, "43", []string{"chroma-mcp", "--client-type"})
	writeFile(t, filepath.Join(root, "43", "stat"), []byte("malformed"), 0o600)
	configPath := writeConfig(t, root, filepath.Join(root, "missing-registry"), "ignored")
	useDependencies(t, fixedTime, "/home/tester", func(string) scanner {
		return &fakeScanner{pids: []int{41, 42, 43}, proc: process.NewProcFS(root)}
	})

	var stdout, stderr bytes.Buffer
	if code := run([]string{"snapshot", "--config", configPath, "--proc-root", root, "--output", "-"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run returned %d: %s", code, stderr.String())
	}
	var snapshot process.Snapshot
	if err := json.Unmarshal(stdout.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Processes) != 1 || len(snapshot.Warnings) != 1 || !strings.Contains(snapshot.Warnings[0], "process 43") {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
}

func TestRunPreservesSmapsWarningWithServiceContext(t *testing.T) {
	root := t.TempDir()
	writeProcessFixture(t, root, "42", []string{"chroma-mcp", "--client-type"})
	if err := os.Remove(filepath.Join(root, "42", "smaps_rollup")); err != nil {
		t.Fatal(err)
	}
	configPath := writeConfig(t, root, filepath.Join(root, "missing-registry"), "ignored")
	useDependencies(t, fixedTime, "/home/tester", nil)

	var stdout bytes.Buffer
	if code := run([]string{"snapshot", "--config", configPath, "--proc-root", root, "--output", "-"}, &stdout, &bytes.Buffer{}); code != 0 {
		t.Fatalf("run returned %d", code)
	}
	var snapshot process.Snapshot
	if err := json.Unmarshal(stdout.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Warnings) != 1 || !strings.Contains(snapshot.Warnings[0], "service chroma") || !strings.Contains(snapshot.Warnings[0], "process 42") {
		t.Fatalf("warning lacks context: %q", snapshot.Warnings)
	}
}

func TestRunExpandsHomeAndAtomicallyWritesPrivateFile(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	writeProcessFixture(t, root, "42", []string{"chroma-mcp", "--client-type"})
	writeFile(t, filepath.Join(home, "registry.json"), []byte(`{"version":1,"registrations":[]}`), 0o600)
	configPath := writeConfig(t, root, "%h/registry.json", "%h/state/snapshot.json")
	useDependencies(t, fixedTime, home, nil)

	var stderr bytes.Buffer
	if code := run([]string{"snapshot", "--config", configPath, "--proc-root", root}, &bytes.Buffer{}, &stderr); code != 0 {
		t.Fatalf("run returned %d: %s", code, stderr.String())
	}
	output := filepath.Join(home, "state", "snapshot.json")
	info, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("output mode = %o", info.Mode().Perm())
	}
	entries, err := os.ReadDir(filepath.Dir(output))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "snapshot.json" {
		t.Fatalf("unexpected output directory entries: %+v", entries)
	}
}

type fakeScanner struct {
	pids []int
	proc *process.ProcFS
}

func (f *fakeScanner) ListPIDs() ([]int, error) { return f.pids, nil }
func (f *fakeScanner) ReadProcess(pid int) (process.Process, []string, error) {
	if pid == 41 {
		return process.Process{}, nil, &process.ProcessGoneError{PID: pid, Err: errors.New("gone")}
	}
	return f.proc.ReadProcess(pid)
}

func useDependencies(t *testing.T, now time.Time, home string, factory func(string) scanner) {
	t.Helper()
	oldNow, oldHome, oldFactory := currentTime, userHomeDir, newScanner
	currentTime = func() time.Time { return now }
	userHomeDir = func() (string, error) { return home, nil }
	if factory != nil {
		newScanner = factory
	}
	t.Cleanup(func() { currentTime, userHomeDir, newScanner = oldNow, oldHome, oldFactory })
}

func writeConfig(t *testing.T, dir, registry, output string) string {
	t.Helper()
	path := filepath.Join(dir, "config.json")
	data, err := json.Marshal(map[string]any{
		"version": 1, "output_path": output, "registry_path": registry,
		"services": []map[string]any{{"name": "chroma", "all_args_contain": []string{"chroma-mcp", "--client-type"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, data, 0o600)
	return path
}

func writeProcessFixture(t *testing.T, root, pid string, args []string) {
	t.Helper()
	dir := filepath.Join(root, pid)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "stat"), []byte(pid+" (node) S 1 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 123 0\n"), 0o600)
	writeFile(t, filepath.Join(dir, "status"), []byte("Name:\tnode\nPPid:\t1\nVmRSS:\t10 kB\n"), 0o600)
	writeFile(t, filepath.Join(dir, "cmdline"), []byte(strings.Join(args, "\x00")+"\x00"), 0o600)
	writeFile(t, filepath.Join(dir, "smaps_rollup"), []byte("Rss: 10 kB\nPss: 8 kB\nPrivate_Clean: 2 kB\nPrivate_Dirty: 3 kB\n"), 0o600)
}

func writeFile(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
}
