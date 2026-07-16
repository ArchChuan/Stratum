package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
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

func TestRunOnlyResolvesHomeForPlaceholderPaths(t *testing.T) {
	root := t.TempDir()
	writeProcessFixture(t, root, "42", []string{"chroma-mcp", "--client-type"})
	configPath := writeConfig(t, root, filepath.Join(root, "registry.json"), "%h/unused-config-output.json")
	oldHome := userHomeDir
	userHomeDir = func() (string, error) { return "", errors.New("home unavailable") }
	t.Cleanup(func() { userHomeDir = oldHome })

	if code := run([]string{"snapshot", "--config", configPath, "--proc-root", root, "--output", "-"}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("absolute paths unexpectedly required home: exit %d", code)
	}
	placeholderConfig := filepath.Join(root, "placeholder.json")
	data := strings.ReplaceAll(string(mustReadFile(t, configPath)), filepath.Join(root, "registry.json"), "%h/registry.json")
	writeFile(t, placeholderConfig, []byte(data), 0o600)
	var stderr bytes.Buffer
	if code := run([]string{"snapshot", "--config", placeholderConfig, "--proc-root", root, "--output", "-"}, &bytes.Buffer{}, &stderr); code != 1 || !strings.Contains(stderr.String(), "resolve home directory") {
		t.Fatalf("placeholder exit=%d stderr=%q", code, stderr.String())
	}
}

func TestRunProducesIdenticalJSONForReversedInputs(t *testing.T) {
	root := t.TempDir()
	writeProcessFixture(t, root, "42", []string{"chroma-mcp", "--client-type"})
	writeProcessFixture(t, root, "43", []string{"chroma-mcp", "--client-type"})
	for _, pid := range []string{"42", "43"} {
		if err := os.Remove(filepath.Join(root, pid, "smaps_rollup")); err != nil {
			t.Fatal(err)
		}
	}
	registryPath := filepath.Join(root, "registry.json")
	configPath := writeConfig(t, root, registryPath, "ignored")
	registrations := []process.Registration{
		registration(process.Identity{PID: 42, StartTicks: 123}, process.Identity{PID: 100, StartTicks: 1}),
		registration(process.Identity{PID: 43, StartTicks: 123}, process.Identity{PID: 101, StartTicks: 1}),
	}
	useDependencies(t, fixedTime, "/unused", nil)

	runOrdered := func(pids []int, registrations []process.Registration) []byte {
		writeRegistry(t, registryPath, registrations)
		oldFactory := newScanner
		newScanner = func(string) scanner { return &orderedScanner{pids: pids, proc: process.NewProcFS(root)} }
		defer func() { newScanner = oldFactory }()
		var stdout, stderr bytes.Buffer
		if code := run([]string{"snapshot", "--config", configPath, "--proc-root", root, "--output", "-"}, &stdout, &stderr); code != 0 {
			t.Fatalf("run returned %d: %s", code, stderr.String())
		}
		return append([]byte(nil), stdout.Bytes()...)
	}
	forward := runOrdered([]int{42, 43}, registrations)
	reverse := runOrdered([]int{43, 42}, []process.Registration{registrations[1], registrations[0]})
	if !bytes.Equal(forward, reverse) {
		t.Fatalf("JSON differs by input order:\n%s\n%s", forward, reverse)
	}
}

func TestRunUsesAllSuccessfulProcessesForLiveClientIdentity(t *testing.T) {
	root := t.TempDir()
	writeProcessFixture(t, root, "42", []string{"chroma-mcp", "--client-type"})
	writeProcessFixture(t, root, "90", []string{"unclassified-client"})
	registryPath := filepath.Join(root, "registry.json")
	configPath := writeConfig(t, root, registryPath, "ignored")
	reg := registration(process.Identity{PID: 42, StartTicks: 123}, process.Identity{PID: 90, StartTicks: 123})
	writeRegistry(t, registryPath, []process.Registration{reg})
	useDependencies(t, fixedTime, "/unused", nil)

	read := func() process.Process {
		var stdout bytes.Buffer
		if code := run([]string{"snapshot", "--config", configPath, "--proc-root", root, "--output", "-"}, &stdout, &bytes.Buffer{}); code != 0 {
			t.Fatalf("run returned %d", code)
		}
		var snapshot process.Snapshot
		if err := json.Unmarshal(stdout.Bytes(), &snapshot); err != nil {
			t.Fatal(err)
		}
		return snapshot.Processes[0]
	}
	child := read()
	if !child.Registered || child.Orphan {
		t.Fatalf("exact live client: %+v", child)
	}
	writeStatFixture(t, root, "90", 999)
	child = read()
	if !child.Registered || !child.Orphan {
		t.Fatalf("reused client PID: %+v", child)
	}
}

func TestRunResolvesRegisteredClientIdentityWhenFullScanFails(t *testing.T) {
	root := t.TempDir()
	writeProcessFixture(t, root, "42", []string{"chroma-mcp", "--client-type"})
	writeProcessFixture(t, root, "90", []string{"unclassified-client"})
	writeFile(t, filepath.Join(root, "90", "status"), []byte("malformed\n"), 0o600)
	registryPath := filepath.Join(root, "registry.json")
	writeRegistry(t, registryPath, []process.Registration{registration(
		process.Identity{PID: 42, StartTicks: 123},
		process.Identity{PID: 90, StartTicks: 123},
	)})
	configPath := writeConfig(t, root, registryPath, "ignored")
	useDependencies(t, fixedTime, "/unused", nil)

	got := runSnapshot(t, configPath, root)
	if !got.Processes[0].Registered || got.Processes[0].Orphan {
		t.Fatalf("registered child with identity-readable client: %+v", got.Processes[0])
	}
	if len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0], "process 90") {
		t.Fatalf("full scan warning missing: %q", got.Warnings)
	}
}

func TestRunConservativelyTreatsIndeterminateRegisteredClientAsLive(t *testing.T) {
	root := t.TempDir()
	writeProcessFixture(t, root, "42", []string{"chroma-mcp", "--client-type"})
	writeProcessFixture(t, root, "90", []string{"unclassified-client", "--token=do-not-leak"})
	writeFile(t, filepath.Join(root, "90", "stat"), []byte("malformed --token=do-not-leak"), 0o600)
	registryPath := filepath.Join(root, "registry.json")
	writeRegistry(t, registryPath, []process.Registration{registration(
		process.Identity{PID: 42, StartTicks: 123},
		process.Identity{PID: 90, StartTicks: 123},
	)})
	configPath := writeConfig(t, root, registryPath, "ignored")
	useDependencies(t, fixedTime, "/unused", nil)

	got := runSnapshot(t, configPath, root)
	if !got.Processes[0].Registered || got.Processes[0].Orphan {
		t.Fatalf("registered child with indeterminate client: %+v", got.Processes[0])
	}
	joined := strings.Join(got.Warnings, "\n")
	if !strings.Contains(joined, "registered client process 90") || !strings.Contains(joined, "malformed") || strings.Contains(joined, "do-not-leak") {
		t.Fatalf("unexpected privacy-safe warning: %q", got.Warnings)
	}
}

func TestRunMarksRegisteredChildOrphanWhenClientDefinitelyMissing(t *testing.T) {
	root := t.TempDir()
	writeProcessFixture(t, root, "42", []string{"chroma-mcp", "--client-type"})
	registryPath := filepath.Join(root, "registry.json")
	writeRegistry(t, registryPath, []process.Registration{registration(
		process.Identity{PID: 42, StartTicks: 123},
		process.Identity{PID: 90, StartTicks: 123},
	)})
	configPath := writeConfig(t, root, registryPath, "ignored")
	useDependencies(t, fixedTime, "/unused", nil)

	got := runSnapshot(t, configPath, root)
	if !got.Processes[0].Registered || !got.Processes[0].Orphan {
		t.Fatalf("registered child with missing client: %+v", got.Processes[0])
	}
}

func TestRunRejectsUnknownRegistryServiceWithoutReplacingSnapshot(t *testing.T) {
	root := t.TempDir()
	writeProcessFixture(t, root, "42", []string{"chroma-mcp", "--client-type"})
	registryPath := filepath.Join(root, "registry.json")
	reg := registration(process.Identity{PID: 42, StartTicks: 123}, process.Identity{PID: 90, StartTicks: 123})
	reg.Service = "unknown"
	writeRegistry(t, registryPath, []process.Registration{reg})
	outputPath := filepath.Join(root, "snapshot.json")
	writeFile(t, outputPath, []byte("previous snapshot"), 0o600)
	configPath := writeConfig(t, root, registryPath, outputPath)
	useDependencies(t, fixedTime, "/unused", nil)

	var stderr bytes.Buffer
	if code := run([]string{"snapshot", "--config", configPath, "--proc-root", root}, &bytes.Buffer{}, &stderr); code != 1 {
		t.Fatalf("exit = %d, want 1; stderr=%q", code, stderr.String())
	}
	if got := string(mustReadFile(t, outputPath)); got != "previous snapshot" {
		t.Fatalf("snapshot replaced on invalid registry: %q", got)
	}
	if !strings.Contains(stderr.String(), "unknown") {
		t.Fatalf("missing registry service context: %q", stderr.String())
	}
}

func TestRunSnapshotNeverPersistsArguments(t *testing.T) {
	root := t.TempDir()
	secret := "--client-type=super-secret-value"
	writeProcessFixture(t, root, "42", []string{"chroma-mcp", secret, "--client-type"})
	configPath := writeConfig(t, root, filepath.Join(root, "missing-registry"), "ignored")
	useDependencies(t, fixedTime, "/unused", nil)

	var stdout bytes.Buffer
	if code := run([]string{"snapshot", "--config", configPath, "--proc-root", root, "--output", "-"}, &stdout, &bytes.Buffer{}); code != 0 {
		t.Fatalf("run returned %d", code)
	}
	if strings.Contains(stdout.String(), secret) || strings.Contains(stdout.String(), `"args"`) {
		t.Fatalf("snapshot leaked argv: %s", stdout.String())
	}

	outputPath := filepath.Join(root, "snapshot.json")
	if code := run([]string{"snapshot", "--config", configPath, "--proc-root", root, "--output", outputPath}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatalf("atomic snapshot run returned %d", code)
	}
	persisted := string(mustReadFile(t, outputPath))
	if strings.Contains(persisted, secret) || strings.Contains(persisted, `"args"`) {
		t.Fatalf("atomic snapshot leaked argv: %s", persisted)
	}
}

func TestWriteAtomicRenameFailurePreservesDestinationAndCleansTemp(t *testing.T) {
	dir := t.TempDir()
	destination := filepath.Join(dir, "snapshot.json")
	writeFile(t, destination, []byte("old"), 0o600)
	oldCreate, oldRename := createTempFile, renameFile
	var tempDir string
	createTempFile = func(dir, pattern string) (*os.File, error) {
		tempDir = dir
		return os.CreateTemp(dir, pattern)
	}
	renameFile = func(string, string) error { return errors.New("rename failed") }
	t.Cleanup(func() { createTempFile, renameFile = oldCreate, oldRename })

	if err := writeAtomic(destination, []byte("new")); err == nil {
		t.Fatal("writeAtomic succeeded")
	}
	if tempDir != dir || string(mustReadFile(t, destination)) != "old" {
		t.Fatalf("tempDir=%q destination=%q", tempDir, mustReadFile(t, destination))
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "snapshot.json" {
		t.Fatalf("temporary file leaked: %+v", entries)
	}
}

func TestWriteAtomicSyncsFileBeforeRenameAndDirectoryAfter(t *testing.T) {
	dir := t.TempDir()
	destination := filepath.Join(dir, "snapshot.json")
	oldSyncFile, oldRename, oldSyncDir := syncFile, renameFile, syncDirectory
	var operations []string
	syncFile = func(file *os.File) error { operations = append(operations, "file-sync"); return file.Sync() }
	renameFile = func(old, new string) error { operations = append(operations, "rename"); return os.Rename(old, new) }
	syncDirectory = func(path string) error {
		operations = append(operations, "directory-sync")
		return syncDirectoryOS(path)
	}
	t.Cleanup(func() { syncFile, renameFile, syncDirectory = oldSyncFile, oldRename, oldSyncDir })

	if err := writeAtomic(destination, []byte("snapshot")); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(operations, ","); got != "file-sync,rename,directory-sync" {
		t.Fatalf("operation order = %q", got)
	}
}

type fakeScanner struct {
	pids []int
	proc *process.ProcFS
}

type orderedScanner struct {
	pids []int
	proc *process.ProcFS
}

func (s *orderedScanner) ListPIDs() ([]int, error) { return s.pids, nil }
func (s *orderedScanner) ReadIdentity(pid int) (process.Identity, error) {
	return s.proc.ReadIdentity(pid)
}
func (s *orderedScanner) ReadProcess(pid int) (process.Process, []string, error) {
	return s.proc.ReadProcess(pid)
}

func (f *fakeScanner) ListPIDs() ([]int, error) { return f.pids, nil }
func (f *fakeScanner) ReadIdentity(pid int) (process.Identity, error) {
	return f.proc.ReadIdentity(pid)
}
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
	writeStatFixture(t, root, pid, 123)
	writeFile(t, filepath.Join(dir, "status"), []byte("Name:\tnode\nPPid:\t1\nVmRSS:\t10 kB\n"), 0o600)
	writeFile(t, filepath.Join(dir, "cmdline"), []byte(strings.Join(args, "\x00")+"\x00"), 0o600)
	writeFile(t, filepath.Join(dir, "smaps_rollup"), []byte("Rss: 10 kB\nPss: 8 kB\nPrivate_Clean: 2 kB\nPrivate_Dirty: 3 kB\n"), 0o600)
}

func writeStatFixture(t *testing.T, root, pid string, startTicks uint64) {
	t.Helper()
	data := fmt.Sprintf("%s (node) S 1 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 %d 0\n", pid, startTicks)
	writeFile(t, filepath.Join(root, pid, "stat"), []byte(data), 0o600)
}

func registration(identity, client process.Identity) process.Registration {
	return process.Registration{Identity: identity, Client: client, Service: "chroma", ConnectedAt: fixedTime.Add(-time.Minute)}
}

func writeRegistry(t *testing.T, path string, registrations []process.Registration) {
	t.Helper()
	data, err := json.Marshal(process.Registry{Version: 1, Registrations: registrations})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, data, 0o600)
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func runSnapshot(t *testing.T, configPath, procRoot string) process.Snapshot {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"snapshot", "--config", configPath, "--proc-root", procRoot, "--output", "-"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run returned %d: %s", code, stderr.String())
	}
	var got process.Snapshot
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func writeFile(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
}
