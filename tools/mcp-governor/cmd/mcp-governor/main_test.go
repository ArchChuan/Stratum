package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/tools/mcp-governor/internal/config"
	"github.com/byteBuilderX/stratum/tools/mcp-governor/internal/observe"
	"github.com/byteBuilderX/stratum/tools/mcp-governor/internal/process"
	"github.com/byteBuilderX/stratum/tools/mcp-governor/internal/report"
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

func TestReportWritesAggregateAndPrunesExpiredValidFile(t *testing.T) {
	root := t.TempDir()
	configPath := writeProxyConfig(t, root, "codex", "user")
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(7 * 24 * time.Hour)
	dir := filepath.Join(root, "events", "codex")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeEventFile(t, filepath.Join(dir, "current.jsonl"), []observe.Event{{Version: 1, Kind: observe.KindToolCall,
		At: start.Add(time.Hour), Client: "codex", Service: "fake", Tool: "search", SessionHash: "hashed",
		Outcome: observe.OutcomeSuccess, Effective: true, DurationMS: 9, ResponseBytes: 12, ConcurrentCalls: 1}})
	expired := filepath.Join(dir, "expired.jsonl")
	writeEventFile(t, expired, []observe.Event{{Version: 1, Kind: observe.KindToolCall, At: end.Add(-31 * 24 * time.Hour),
		Client: "codex", Service: "fake", Tool: "old", SessionHash: "old-hash", Outcome: observe.OutcomeSuccess}})
	var stderr bytes.Buffer
	if code := run([]string{"report", "--config", configPath, "--from", start.Format(time.RFC3339), "--to",
		end.Format(time.RFC3339)}, &bytes.Buffer{}, &stderr); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	data := mustReadFile(t, filepath.Join(root, "reports", "report-20260701T000000Z-20260708T000000Z.json"))
	if bytes.Contains(data, []byte("hashed")) {
		t.Fatalf("report leaked identifier: %s", data)
	}
	if _, err := os.Stat(expired); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired file remains: %v", err)
	}
}

func TestReportMalformedInputPreservesPriorReport(t *testing.T) {
	root := t.TempDir()
	configPath := writeProxyConfig(t, root, "codex", "user")
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(7 * 24 * time.Hour)
	dir := filepath.Join(root, "events", "codex")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "bad.jsonl"), []byte(`{"version":1,"version":1}`+"\n"), 0o600)
	output := filepath.Join(root, "prior.json")
	writeFile(t, output, []byte("prior"), 0o600)
	var stderr bytes.Buffer
	code := run([]string{"report", "--config", configPath, "--from", start.Format(time.RFC3339), "--to",
		end.Format(time.RFC3339), "--output", output}, &bytes.Buffer{}, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "duplicate key") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	if got := string(mustReadFile(t, output)); got != "prior" {
		t.Fatalf("prior report replaced: %q", got)
	}
}

func TestSnapshotHistoryFeedsRepeatedProcessStartsAndMemoryPeaks(t *testing.T) {
	root := t.TempDir()
	procRoot := filepath.Join(root, "proc")
	if err := os.Mkdir(procRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	writeProcessFixture(t, procRoot, "42", []string{"catalog"})
	configPath := writeProxyConfig(t, root, "codex", "user")
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	useDependencies(t, start.Add(time.Hour), root, nil)
	if code := run([]string{"snapshot", "--config", configPath, "--proc-root", procRoot}, io.Discard, io.Discard); code != 0 {
		t.Fatalf("first snapshot code=%d", code)
	}
	writeStatFixture(t, procRoot, "42", 456)
	writeFile(t, filepath.Join(procRoot, "42", "smaps_rollup"),
		[]byte("Rss: 30 kB\nPss: 20 kB\nPrivate_Clean: 7 kB\nPrivate_Dirty: 5 kB\n"), 0o600)
	currentTime = func() time.Time { return start.Add(2 * time.Hour) }
	if code := run([]string{"snapshot", "--config", configPath, "--proc-root", procRoot}, io.Discard, io.Discard); code != 0 {
		t.Fatalf("second snapshot code=%d", code)
	}
	var stdout bytes.Buffer
	if code := run([]string{"report", "--config", configPath, "--from", start.Format(time.RFC3339), "--to",
		start.Add(7 * 24 * time.Hour).Format(time.RFC3339), "--output", "-"}, &stdout, io.Discard); code != 0 {
		t.Fatalf("report code=%d", code)
	}
	var got report.Report
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Services) != 1 || got.Services[0].ProcessStarts != 2 || got.Services[0].PeakPSSBytes != 20*1024 {
		t.Fatalf("services=%+v", got.Services)
	}
}

func TestSnapshotHistoryFailurePreservesCurrentSnapshot(t *testing.T) {
	root := t.TempDir()
	procRoot := filepath.Join(root, "proc")
	if err := os.Mkdir(procRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	writeProcessFixture(t, procRoot, "42", []string{"catalog"})
	configPath := writeProxyConfig(t, root, "codex", "user")
	currentPath := filepath.Join(root, "snapshot.json")
	writeFile(t, currentPath, []byte("previous snapshot"), 0o600)
	oldRename := renameFile
	renameFile = func(old, destination string) error {
		if strings.HasSuffix(destination, ".history.jsonl") {
			return errors.New("history publish failed")
		}
		return os.Rename(old, destination)
	}
	t.Cleanup(func() { renameFile = oldRename })
	useDependencies(t, fixedTime, root, nil)
	if code := run([]string{"snapshot", "--config", configPath, "--proc-root", procRoot}, io.Discard, io.Discard); code != 1 {
		t.Fatalf("snapshot code=%d", code)
	}
	if got := string(mustReadFile(t, currentPath)); got != "previous snapshot" {
		t.Fatalf("current snapshot changed before history publication: %q", got)
	}
}

func TestSnapshotHistorySerializesDeduplicatesAndCapsSamples(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "snapshot.json")
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	first := process.Snapshot{Version: 1, Mode: "observe", CapturedAt: base}
	second := process.Snapshot{Version: 1, Mode: "observe", CapturedAt: base.Add(time.Minute)}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, snapshot := range []process.Snapshot{second, first} {
		wg.Add(1)
		go func(item process.Snapshot) {
			defer wg.Done()
			data, _ := json.Marshal(item)
			errs <- publishSnapshotData(output, append(data, '\n'), item, 7)
		}(snapshot)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	history, err := decodeSnapshotHistory(output + ".history.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || !history[0].CapturedAt.Equal(base) || !history[1].CapturedAt.Equal(base.Add(time.Minute)) {
		t.Fatalf("serialized history=%+v", history)
	}
	data, _ := json.Marshal(first)
	if err := publishSnapshotData(output, append(data, '\n'), first, 7); err != nil {
		t.Fatal(err)
	}
	history, err = decodeSnapshotHistory(output + ".history.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 {
		t.Fatalf("duplicate snapshot retained: %d", len(history))
	}

	const maxSamples = 7 * 24 * 60
	var raw bytes.Buffer
	encoder := json.NewEncoder(&raw)
	for i := 0; i < maxSamples+2; i++ {
		item := process.Snapshot{Version: 1, Mode: "observe", CapturedAt: base.Add(time.Duration(i) * time.Minute)}
		if err := encoder.Encode(item); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, output+".history.jsonl", raw.Bytes(), 0o600)
	latest := process.Snapshot{Version: 1, Mode: "observe", CapturedAt: base.Add((maxSamples + 2) * time.Minute)}
	latestData, _ := json.Marshal(latest)
	if err := publishSnapshotData(output, append(latestData, '\n'), latest, 7); err != nil {
		t.Fatal(err)
	}
	history, err = decodeSnapshotHistory(output + ".history.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != maxSamples {
		t.Fatalf("history samples=%d, want cap %d", len(history), maxSamples)
	}
}

func TestSnapshotHistoryOlderPublisherCannotRegressBoundaryOrCurrent(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "snapshot.json")
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	expired := process.Snapshot{Version: 1, Mode: "observe", CapturedAt: base}
	newest := process.Snapshot{Version: 1, Mode: "observe", CapturedAt: base.Add(10 * 24 * time.Hour)}
	olderFinishingLast := process.Snapshot{Version: 1, Mode: "observe", CapturedAt: base.Add(24 * time.Hour)}
	var history bytes.Buffer
	encoder := json.NewEncoder(&history)
	for _, item := range []process.Snapshot{expired, newest} {
		if err := encoder.Encode(item); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, output+".history.jsonl", history.Bytes(), 0o600)
	writeFile(t, output+".history.jsonl.lock", nil, 0o600)
	data, _ := json.Marshal(olderFinishingLast)
	if err := publishSnapshotData(output, append(data, '\n'), olderFinishingLast, 7); err != nil {
		t.Fatal(err)
	}
	gotHistory, err := decodeSnapshotHistory(output + ".history.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if len(gotHistory) != 1 || !gotHistory[0].CapturedAt.Equal(newest.CapturedAt) {
		t.Fatalf("retained history=%+v, want only newest sample", gotHistory)
	}
	var current process.Snapshot
	if err := json.Unmarshal(mustReadFile(t, output), &current); err != nil {
		t.Fatal(err)
	}
	if !current.CapturedAt.Equal(newest.CapturedAt) {
		t.Fatalf("current captured_at=%s, want newest %s", current.CapturedAt, newest.CapturedAt)
	}
}

func TestPruneDoesNotUnlinkFileAppendedAfterReportRead(t *testing.T) {
	root := filepath.Join(t.TempDir(), "events")
	w, err := observe.NewWriter(root, "codex", "session")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	end := time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC)
	expired := observe.Event{Version: 1, Kind: observe.KindToolCall, At: end.Add(-31 * 24 * time.Hour),
		Client: "codex", Service: "fake", Tool: "old", SessionHash: "session", Outcome: observe.OutcomeSuccess}
	if err := w.Write(expired); err != nil {
		t.Fatal(err)
	}
	acc, _ := report.NewAccumulator(end.Add(-7*24*time.Hour), end)
	files, err := readEventFiles(root, acc)
	if err != nil {
		t.Fatal(err)
	}
	current := expired
	current.At = end.Add(-time.Hour)
	if err := w.Write(current); err != nil {
		t.Fatal(err)
	}
	if err := pruneEventFiles(files, end.Add(-30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	data := mustReadFile(t, filepath.Join(root, "codex", "session.jsonl"))
	if bytes.Count(data, []byte{'\n'}) != 2 {
		t.Fatalf("events lost after prune race: %q", data)
	}
}

func TestPruneDoesNotUnlinkIdleActiveWriter(t *testing.T) {
	root := filepath.Join(t.TempDir(), "events")
	w, err := observe.NewWriter(root, "codex", "active")
	if err != nil {
		t.Fatal(err)
	}
	end := time.Date(2026, 7, 8, 0, 0, 0, 0, time.UTC)
	expired := observe.Event{Version: 1, Kind: observe.KindToolCall, At: end.Add(-31 * 24 * time.Hour),
		Client: "codex", Service: "fake", Tool: "old", SessionHash: "active", Outcome: observe.OutcomeSuccess}
	if err := w.Write(expired); err != nil {
		t.Fatal(err)
	}
	acc, _ := report.NewAccumulator(end.Add(-7*24*time.Hour), end)
	files, err := readEventFiles(root, acc)
	if err != nil {
		t.Fatal(err)
	}
	if err := pruneEventFiles(files, end.Add(-30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	current := expired
	current.At = end.Add(-time.Hour)
	if err := w.Write(current); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	data := mustReadFile(t, filepath.Join(root, "codex", "active.jsonl"))
	if bytes.Count(data, []byte{'\n'}) != 2 {
		t.Fatalf("active writer events lost: %q", data)
	}
}

func TestReportRequiresSevenDaysUnlessPartialAndStdoutDoesNotWriteDefault(t *testing.T) {
	root := t.TempDir()
	configPath := writeProxyConfig(t, root, "codex", "user")
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(6 * 24 * time.Hour)
	dir := filepath.Join(root, "events", "codex")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	expired := filepath.Join(dir, "expired.jsonl")
	writeEventFile(t, expired, []observe.Event{{Version: 1, Kind: observe.KindToolCall,
		At: end.Add(-31 * 24 * time.Hour), Client: "codex", Service: "fake", Tool: "old",
		SessionHash: "old", Outcome: observe.OutcomeSuccess}})
	args := []string{"report", "--config", configPath, "--from", start.Format(time.RFC3339), "--to", end.Format(time.RFC3339), "--output", "-"}
	if code := run(args, &bytes.Buffer{}, &bytes.Buffer{}); code != 1 {
		t.Fatalf("partial code=%d", code)
	}
	var stdout bytes.Buffer
	if code := run(append(args, "--allow-partial"), &stdout, &bytes.Buffer{}); code != 0 {
		t.Fatalf("allowed code=%d", code)
	}
	if stdout.Len() == 0 {
		t.Fatal("empty stdout")
	}
	if _, err := os.Stat(filepath.Join(root, "reports")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("reports dir created: %v", err)
	}
	if _, err := os.Stat(expired); err != nil {
		t.Fatalf("stdout report pruned events: %v", err)
	}
}

func TestRunProxyRejectsInvalidInvocation(t *testing.T) {
	root := t.TempDir()
	configPath := writeProxyConfig(t, root, "codex", "repository")
	tests := []struct {
		name string
		args []string
		want string
		code int
	}{
		{name: "missing separator", args: []string{"proxy", "--config", configPath, "--client", "codex", "--service", "fake", "command"}, want: "separator", code: 2},
		{name: "unknown client", args: []string{"proxy", "--config", configPath, "--client", "unknown", "--service", "fake", "--repository", root, "--", "command"}, want: "client", code: 1},
		{name: "unknown service", args: []string{"proxy", "--config", configPath, "--client", "codex", "--service", "unknown", "--repository", root, "--", "command"}, want: "service", code: 1},
		{name: "empty session", args: []string{"proxy", "--config", configPath, "--client", "codex", "--service", "fake", "--session", "", "--repository", root, "--", "command"}, want: "session", code: 2},
		{name: "malformed session", args: []string{"proxy", "--config", configPath, "--client", "codex", "--service", "fake", "--session", "1:0", "--repository", root, "--", "command"}, want: "session", code: 2},
		{name: "repository required", args: []string{"proxy", "--config", configPath, "--client", "codex", "--service", "fake", "--session", "1:2", "--", "command"}, want: "repository", code: 1},
		{name: "empty command", args: []string{"proxy", "--config", configPath, "--client", "codex", "--service", "fake", "--session", "1:2", "--repository", root, "--"}, want: "command", code: 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(test.args, &stdout, &stderr); code != test.code {
				t.Fatalf("run() = %d, want %d; stderr=%q", code, test.code, stderr.String())
			}
			if !strings.Contains(strings.ToLower(stderr.String()), test.want) {
				t.Fatalf("stderr=%q, want %q", stderr.String(), test.want)
			}
		})
	}
}

func TestRunProxyRejectsServiceNotEnabledForClient(t *testing.T) {
	root := t.TempDir()
	configPath := writeProxyConfig(t, root, "claude", "user")
	var stderr bytes.Buffer
	code := run([]string{"proxy", "--config", configPath, "--client", "codex", "--service", "fake",
		"--session", "1:2", "--", "command"}, io.Discard, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "not enabled") {
		t.Fatalf("run()=%d stderr=%q", code, stderr.String())
	}
}

func TestRunProxyRejectsCommandNotClassifiedAsService(t *testing.T) {
	root := t.TempDir()
	configPath := writeProxyConfigWithCommand(t, root, "codex", "user", "unrelated-command", nil)
	var stderr bytes.Buffer
	code := run([]string{"proxy", "--config", configPath, "--client", "codex", "--service", "fake",
		"--session", "1:2", "--", "unrelated-command"}, io.Discard, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "does not match") {
		t.Fatalf("run()=%d stderr=%q", code, stderr.String())
	}
}

func TestRunProxyRejectsCatalogCommandMismatchBeforeChildStart(t *testing.T) {
	root := t.TempDir()
	configPath := writeProxyConfigWithCommand(t, root, "codex", "user", "catalog-command",
		[]string{"catalog-one", "catalog-two"})
	secretCommand := "do-not-print-command"
	var stderr bytes.Buffer
	code := run([]string{"proxy", "--config", configPath, "--client", "codex", "--service", "fake",
		"--session", "1:2", "--", secretCommand, "catalog-one", "catalog-two"}, io.Discard, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "catalog command mismatch") {
		t.Fatalf("run()=%d stderr=%q", code, stderr.String())
	}
	if strings.Contains(stderr.String(), secretCommand) || strings.Contains(stderr.String(), "executable file not found") {
		t.Fatalf("rejected command leaked or started: %q", stderr.String())
	}
}

func TestRunProxyRejectsCatalogArgumentMismatchBeforeChildStart(t *testing.T) {
	root := t.TempDir()
	configPath := writeProxyConfigWithCommand(t, root, "codex", "user", "catalog-command",
		[]string{"catalog-one", "catalog-two"})
	secretArg := "do-not-print-argument"
	tests := []struct {
		name string
		args []string
	}{
		{name: "missing", args: []string{"catalog-one"}},
		{name: "extra", args: []string{"catalog-one", "catalog-two", secretArg}},
		{name: "reordered", args: []string{"catalog-two", "catalog-one"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stderr bytes.Buffer
			args := []string{"proxy", "--config", configPath, "--client", "codex", "--service", "fake",
				"--session", "1:2", "--", "catalog-command"}
			args = append(args, test.args...)
			code := run(args, io.Discard, &stderr)
			if code != 1 || !strings.Contains(stderr.String(), "catalog command mismatch") {
				t.Fatalf("run()=%d stderr=%q", code, stderr.String())
			}
			if strings.Contains(stderr.String(), secretArg) || strings.Contains(stderr.String(), "executable file not found") {
				t.Fatalf("rejected arguments leaked or started: %q", stderr.String())
			}
		})
	}
	if matches, err := filepath.Glob(filepath.Join(root, "events", "*", "*.jsonl")); err != nil || len(matches) != 0 {
		t.Fatalf("rejected command persisted events: matches=%v err=%v", matches, err)
	}
}

func TestRunProxyExecutesCommandAndPersistsOnlyMetadata(t *testing.T) {
	root := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	secretEnv := "do-not-persist-environment"
	childArgs := []string{"-test.run=TestProxyHelperProcess", "--", "catalog"}
	configPath := writeProxyConfigWithCommand(t, root, "codex", "repository", executable, childArgs)
	t.Setenv("MCP_GOVERNOR_TEST_HELPER", "1")
	t.Setenv("MCP_GOVERNOR_SECRET", secretEnv)
	oldStdin := proxyStdin
	proxyStdin = strings.NewReader("{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"initialize\"}\n")
	t.Cleanup(func() { proxyStdin = oldStdin })

	var stdout, stderr bytes.Buffer
	args := []string{"proxy", "--config", configPath, "--client", "codex", "--service", "fake",
		"--session", "123:456", "--repository", filepath.Join(root, "."), "--", executable}
	args = append(args, childArgs...)
	if code := run(args, &stdout, &stderr); code != 0 {
		t.Fatalf("run()=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"protocolVersion":"2025-03-26"`) {
		t.Fatalf("stdout=%q", stdout.String())
	}
	allOutput := stdout.String() + stderr.String()
	if strings.Contains(allOutput, secretEnv) {
		t.Fatalf("secret printed: %q", allOutput)
	}
	events, err := filepath.Glob(filepath.Join(root, "events", "codex", "*.jsonl"))
	if err != nil || len(events) != 1 {
		t.Fatalf("events=%v err=%v", events, err)
	}
	data := mustReadFile(t, events[0])
	if strings.Contains(string(data), secretEnv) || strings.Contains(string(data), root) {
		t.Fatalf("secret or repository path persisted: %s", data)
	}
	var event map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(data), &event); err != nil {
		t.Fatal(err)
	}
	if event["kind"] != "session_ready" || event["client"] != "codex" || event["service"] != "fake" ||
		event["session_hash"] == "123:456" || event["repository_hash"] == "" {
		t.Fatalf("unexpected event: %+v", event)
	}
}

func TestProxyHelperProcess(t *testing.T) {
	if os.Getenv("MCP_GOVERNOR_TEST_HELPER") != "1" {
		return
	}
	_, _ = io.ReadAll(os.Stdin)
	_, _ = fmt.Fprintln(os.Stdout, `{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2025-03-26"}}`)
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

func TestRunRenderConfigWritesStdoutAndPrivateAtomicFile(t *testing.T) {
	root := t.TempDir()
	catalog := filepath.Join("..", "..", "testdata", "catalog.json")
	governor := filepath.Join(root, "mcp-governor")
	writeFile(t, governor, []byte("binary"), 0o700)

	var stdout, stderr bytes.Buffer
	args := []string{"render-config", "--config", catalog, "--client", "codex", "--governor", governor,
		"--output", "-"}
	if code := run(args, &stdout, &stderr); code != 0 {
		t.Fatalf("run()=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "[mcp_servers.alpha]") || strings.Contains(stdout.String(), "claude-only") {
		t.Fatalf("stdout=%q", stdout.String())
	}

	output := filepath.Join(root, "private", "mcp.toml")
	args[len(args)-1] = output
	if code := run(args, io.Discard, &stderr); code != 0 {
		t.Fatalf("run()=%d stderr=%q", code, stderr.String())
	}
	info, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%#o, want 0600", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(filepath.Dir(output))
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("directory mode=%#o, want 0700", dirInfo.Mode().Perm())
	}
}

func TestRunRenderConfigRejectsUnsafeInputsWithoutLeakingArguments(t *testing.T) {
	root := t.TempDir()
	catalog := filepath.Join("..", "..", "testdata", "catalog.json")
	governor := filepath.Join(root, "governor")
	writeFile(t, governor, []byte("binary"), 0o700)
	unsafeOutput := filepath.Join(root, "output.json")
	writeFile(t, unsafeOutput, []byte("existing"), 0o644)
	symlinkOutput := filepath.Join(root, "link.json")
	if err := os.Symlink(unsafeOutput, symlinkOutput); err != nil {
		t.Fatal(err)
	}
	secret := "do-not-print-secret-argument"
	tests := []struct {
		name string
		args []string
		code int
	}{
		{"missing output", []string{"render-config", "--config", catalog, "--client", "codex", "--governor", governor}, 2},
		{"unknown client", []string{"render-config", "--config", catalog, "--client", "unknown", "--governor", governor, "--output", "-"}, 2},
		{"missing governor", []string{"render-config", "--config", catalog, "--client", "codex", "--governor", filepath.Join(root, secret), "--output", "-"}, 1},
		{"unsafe mode", []string{"render-config", "--config", catalog, "--client", "codex", "--governor", governor, "--output", unsafeOutput}, 1},
		{"symlink", []string{"render-config", "--config", catalog, "--client", "codex", "--governor", governor, "--output", symlinkOutput}, 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stderr bytes.Buffer
			if code := run(test.args, io.Discard, &stderr); code != test.code {
				t.Fatalf("run()=%d stderr=%q", code, stderr.String())
			}
			if strings.Contains(stderr.String(), secret) {
				t.Fatalf("stderr leaked argument: %q", stderr.String())
			}
		})
	}
}

func TestRunRenderConfigRejectsIncompleteCatalogWithoutPartialOutput(t *testing.T) {
	root := t.TempDir()
	governor := filepath.Join(root, "governor")
	writeFile(t, governor, []byte("binary"), 0o700)
	secretCommand := "do-not-print-secret-command"
	tests := []struct {
		name, service, reason string
		mutate                func(*config.ServiceRule)
	}{
		{"unavailable client", "claude-only", "not enabled", func(service *config.ServiceRule) {
			service.Clients = []config.Client{config.ClientClaude}
		}},
		{"repository scope", "repository-index", "repository", func(service *config.ServiceRule) {
			service.Scope = config.ScopeRepository
			service.SessionPolicy = config.SessionPolicyIsolated
		}},
		{"non stdio", "remote", "stdio", func(service *config.ServiceRule) {
			service.Transport = config.TransportStreamableHTTP
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			catalog := writeRenderCatalog(t, root, test.service, secretCommand, test.mutate)
			for _, output := range []string{"-", filepath.Join(root, test.service+".json")} {
				var stdout, stderr bytes.Buffer
				args := []string{"render-config", "--config", catalog, "--client", "codex", "--governor", governor,
					"--output", output}
				if code := run(args, &stdout, &stderr); code != 1 {
					t.Fatalf("run()=%d stderr=%q", code, stderr.String())
				}
				if stdout.Len() != 0 {
					t.Fatalf("partial stdout=%q", stdout.String())
				}
				if output != "-" {
					if _, err := os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("partial output exists: %v", err)
					}
				}
				message := strings.ToLower(stderr.String())
				if !strings.Contains(message, test.service) || !strings.Contains(message, test.reason) {
					t.Fatalf("stderr=%q, want service %q and reason %q", stderr.String(), test.service, test.reason)
				}
				if strings.Contains(stderr.String(), secretCommand) {
					t.Fatalf("stderr leaked command: %q", stderr.String())
				}
			}
		})
	}
}

func writeRenderCatalog(t *testing.T, root, name, command string, mutate func(*config.ServiceRule)) string {
	t.Helper()
	fixture, err := os.Open(filepath.Join("..", "..", "testdata", "catalog.json"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, decodeErr := config.Decode(fixture)
	closeErr := fixture.Close()
	if decodeErr != nil || closeErr != nil {
		t.Fatalf("decode fixture: %v; close: %v", decodeErr, closeErr)
	}
	service := cfg.Services[0]
	service.Name, service.Command = name, command
	mutate(&service)
	cfg.Services = append(cfg.Services, service)
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, name+"-catalog.json")
	writeFile(t, path, data, 0o600)
	return path
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

func writeProxyConfig(t *testing.T, dir, client, scope string) string {
	return writeProxyConfigWithCommand(t, dir, client, scope, "catalog-command", []string{"catalog-arg"})
}

func writeProxyConfigWithCommand(t *testing.T, dir, client, scope, command string, args []string) string {
	t.Helper()
	saltPath := filepath.Join(dir, "salt")
	writeFile(t, saltPath, bytes.Repeat([]byte{'s'}, 32), 0o600)
	path := filepath.Join(dir, "proxy-config.json")
	data, err := json.Marshal(map[string]any{
		"version": 2, "output_path": filepath.Join(dir, "snapshot.json"),
		"registry_path": filepath.Join(dir, "registry.json"),
		"observation": map[string]any{
			"events_dir": filepath.Join(dir, "events"), "reports_dir": filepath.Join(dir, "reports"),
			"salt_path": saltPath, "raw_retention_days": 30,
		},
		"services": []map[string]any{{
			"name": "fake", "command": command, "args": args, "cwd": dir,
			"transport": "stdio", "scope": scope, "session_policy": "isolated",
			"clients": []string{client}, "all_args_contain": []string{"catalog"},
		}},
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

func writeEventFile(t *testing.T, path string, events []observe.Event) {
	t.Helper()
	var data bytes.Buffer
	encoder := json.NewEncoder(&data)
	for _, event := range events {
		if err := encoder.Encode(event); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, path, data.Bytes(), 0o600)
}
