package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/internal/platform/e2erunscope"
)

type fakeAdmin struct {
	exists  bool
	queries []string
	closed  bool
}

func (a *fakeAdmin) Exec(_ context.Context, query string) error {
	a.queries = append(a.queries, query)
	return nil
}
func (a *fakeAdmin) Exists(context.Context, string) (bool, error) { return a.exists, nil }
func (a *fakeAdmin) Close(context.Context) error                  { a.closed = true; return nil }

func testDependencies(now time.Time, admin *fakeAdmin) dependencies {
	return dependencies{
		now: func() time.Time { return now }, pid: func() int { return 4242 },
		random:       bytes.NewReader(bytes.Repeat([]byte{0xab}, 8)),
		lookupEnv:    os.Getenv,
		openAdmin:    func(string) (e2erunscope.DatabaseAdmin, error) { return admin, nil },
		processAlive: func(int, string) bool { return false },
	}
}

func execute(t *testing.T, deps dependencies, args ...string) (string, error) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	err := runWithDependencies(args, &stdout, &stderr, deps)
	return stdout.String(), err
}

func TestAllocateUsesInjectedDefaultsAndCanonicalJSON(t *testing.T) {
	now := time.Date(2026, 7, 30, 1, 2, 3, 0, time.UTC)
	root := filepath.Join(t.TempDir(), "registry")
	repository := t.TempDir()
	out, err := execute(t, testDependencies(now, &fakeAdmin{}), "allocate", "--repository", repository, "--registry", root)
	if err != nil {
		t.Fatal(err)
	}
	var scope e2erunscope.Scope
	if err := json.Unmarshal([]byte(out), &scope); err != nil {
		t.Fatal(err)
	}
	if scope.OwnerPID != 4242 || scope.RunID != "20260730t010203z-abababababababab" {
		t.Fatalf("unexpected scope: %+v", scope)
	}
	ports := []int{scope.Ports.Frontend, scope.Ports.Backend, scope.Ports.OAuth, scope.Ports.Fixture}
	unique := make(map[int]bool, len(ports))
	for _, port := range ports {
		if port <= 0 || unique[port] {
			t.Fatalf("ports=%v, want four distinct non-zero ports", ports)
		}
		unique[port] = true
	}
	want, _ := json.Marshal(scope)
	if out != string(want)+"\n" {
		t.Fatalf("non-canonical JSON: %q", out)
	}
	if _, err := os.Stat(filepath.Join(root, "runs", scope.RunID+".json")); err != nil {
		t.Fatal(err)
	}
}

func TestAllocateOwnerPIDOverrideAndValidation(t *testing.T) {
	deps := testDependencies(time.Date(2026, 7, 30, 1, 2, 3, 0, time.UTC), &fakeAdmin{})
	out, err := execute(
		t, deps, "allocate", "--repository", t.TempDir(), "--registry", filepath.Join(t.TempDir(), "r"),
		"--owner-pid", "99",
	)
	if err != nil {
		t.Fatal(err)
	}
	var scope e2erunscope.Scope
	_ = json.Unmarshal([]byte(out), &scope)
	if scope.OwnerPID != 99 {
		t.Fatalf("owner_pid=%d", scope.OwnerPID)
	}
	_, err = execute(
		t, deps, "allocate", "--repository", t.TempDir(), "--registry", filepath.Join(t.TempDir(), "r"),
		"--owner-pid", "-1",
	)
	if err == nil || !strings.Contains(err.Error(), "positive") {
		t.Fatalf("error=%v", err)
	}
}

func TestPrepareDatabaseLifecycleAndReleaseCommands(t *testing.T) {
	now := time.Date(2026, 7, 30, 1, 2, 3, 0, time.UTC)
	admin := &fakeAdmin{}
	deps := testDependencies(now, admin)
	root := filepath.Join(t.TempDir(), "registry")
	if _, err := execute(t, deps, "prepare-registry", "--registry", root); err != nil {
		t.Fatal(err)
	}
	out, err := execute(t, deps, "allocate", "--repository", t.TempDir(), "--registry", root)
	if err != nil {
		t.Fatal(err)
	}
	var scope e2erunscope.Scope
	_ = json.Unmarshal([]byte(out), &scope)
	scopePath := filepath.Join(t.TempDir(), "scope.json")
	data, _ := json.Marshal(scope)
	if err := os.WriteFile(scopePath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(t, deps, "validate", "--scope", scopePath); err != nil {
		t.Fatal(err)
	}
	invalid := scope
	invalid.DatabaseName = "postgres"
	invalidData, _ := json.Marshal(invalid)
	invalidPath := filepath.Join(t.TempDir(), "invalid-scope.json")
	if err := os.WriteFile(invalidPath, invalidData, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := execute(t, deps, "validate", "--scope", invalidPath); err == nil {
		t.Fatal("validate accepted an unsafe scope")
	}
	if _, err := execute(t, deps, "create-database", "--scope", scopePath, "--base-dsn-env", "IGNORED"); err != nil {
		t.Fatal(err)
	}
	if len(admin.queries) != 1 || !strings.HasPrefix(admin.queries[0], "CREATE DATABASE") || !admin.closed {
		t.Fatalf("admin=%+v", admin)
	}
	admin.exists, admin.closed = true, false
	if _, err := execute(t, deps, "drop-database", "--scope", scopePath, "--base-dsn-env", "IGNORED"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(admin.queries[1], "DROP DATABASE") || !admin.closed {
		t.Fatalf("admin=%+v", admin)
	}
	if _, err := execute(t, deps, "mark-infrastructure-owned", "--scope", scopePath, "--registry", root); err != nil {
		t.Fatal(err)
	}
	release, err := execute(t, deps, "release", "--scope", scopePath, "--registry", root)
	if err != nil {
		t.Fatal(err)
	}
	var result e2erunscope.ReleaseResult
	_ = json.Unmarshal([]byte(release), &result)
	if !result.LastReference || !result.StopOwnedInfrastructure || result.OwnershipRunID != scope.RunID {
		t.Fatalf("result=%+v", result)
	}
	if _, err := execute(
		t, deps, "confirm-infrastructure-stopped", "--ownership-run-id", scope.RunID, "--registry", root,
	); err != nil {
		t.Fatal(err)
	}
}

func TestDatabaseURLAndCredentialRedaction(t *testing.T) {
	t.Setenv("SAFE_DSN", "postgres://user:secret@127.0.0.1:5432/stratum_test?sslmode=disable")
	name := "stratum_e2e_20260730t010203z_abababababababab"
	out, err := execute(
		t, testDependencies(time.Time{}, &fakeAdmin{}), "database-url", "--base-dsn-env", "SAFE_DSN",
		"--database-name", name,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "/"+name) {
		t.Fatalf("url=%q", out)
	}
	t.Setenv("BAD_DSN", "postgres://user:do-not-leak@production.example.com/prod")
	_, err = execute(
		t, testDependencies(time.Time{}, &fakeAdmin{}), "database-url", "--base-dsn-env", "BAD_DSN",
		"--database-name", name,
	)
	if err == nil || strings.Contains(err.Error(), "do-not-leak") {
		t.Fatalf("error=%v", err)
	}
}

func TestReapDropsExpiredDatabaseAndEmitsCanonicalJSON(t *testing.T) {
	created := time.Date(2026, 7, 28, 1, 2, 3, 0, time.UTC)
	admin := &fakeAdmin{exists: true}
	root := filepath.Join(t.TempDir(), "registry")
	deps := testDependencies(created, admin)
	out, err := execute(t, deps, "allocate", "--repository", t.TempDir(), "--registry", root)
	if err != nil {
		t.Fatal(err)
	}
	var scope e2erunscope.Scope
	_ = json.Unmarshal([]byte(out), &scope)
	deps.now = func() time.Time { return created.Add(25 * time.Hour) }
	out, err = execute(t, deps, "reap", "--registry", root, "--base-dsn-env", "IGNORED")
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal([]string{scope.DatabaseName})
	if out != string(want)+"\n" {
		t.Fatalf("output=%q want=%q", out, string(want)+"\n")
	}
	if !admin.closed || len(admin.queries) != 1 {
		t.Fatalf("admin=%+v", admin)
	}
}

func TestCommandErrors(t *testing.T) {
	deps := testDependencies(time.Now(), &fakeAdmin{})
	for _, args := range [][]string{{}, {"unknown"}} {
		if _, err := execute(t, deps, args...); err == nil {
			t.Fatalf("args=%v: expected error", args)
		}
	}
	deps.openAdmin = func(string) (e2erunscope.DatabaseAdmin, error) { return nil, errors.New("open failed") }
	if _, err := execute(t, deps, "reap", "--registry", t.TempDir()); err == nil {
		t.Fatal("expected open error")
	}
}
