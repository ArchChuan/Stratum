package e2erunscope

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeDatabaseAdmin struct {
	exists  bool
	queries []string
	err     error
}

func TestReapDatabasesDropsThenReleasesStaleLease(t *testing.T) {
	now := time.Date(2026, time.July, 31, 12, 0, 0, 0, time.UTC)
	r := Registry{Root: filepath.Join(t.TempDir(), "registry")}
	scope := registryTestScopeAt(t, "20260730t100000z-a1b2c3d4e5f60718", 101, now.Add(-25*time.Hour))
	if err := r.Register(scope); err != nil {
		t.Fatal(err)
	}
	admin := &fakeDatabaseAdmin{exists: true}
	removed, err := ReapDatabases(context.Background(), r, admin, now, func(int, string) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0] != scope.DatabaseName {
		t.Fatalf("removed = %v, want %q", removed, scope.DatabaseName)
	}
	if _, err := r.Read(scope.RunID); err == nil {
		t.Fatal("stale lease remains after successful drop")
	}
}

func TestReapDatabasesPreservesLeaseWhenDropFails(t *testing.T) {
	now := time.Date(2026, time.July, 31, 12, 0, 0, 0, time.UTC)
	r := Registry{Root: filepath.Join(t.TempDir(), "registry")}
	scope := registryTestScopeAt(t, "20260730t100000z-a1b2c3d4e5f60718", 101, now.Add(-25*time.Hour))
	if err := r.Register(scope); err != nil {
		t.Fatal(err)
	}
	admin := &fakeDatabaseAdmin{exists: true, err: errors.New("drop failed")}
	if _, err := ReapDatabases(context.Background(), r, admin, now, func(int, string) bool { return false }); err == nil {
		t.Fatal("ReapDatabases() error = nil")
	}
	if _, err := r.Read(scope.RunID); err != nil {
		t.Fatalf("lease removed after failed drop: %v", err)
	}
}

func TestReapDatabasesPropagatesReleaseFailure(t *testing.T) {
	now := time.Date(2026, time.July, 31, 12, 0, 0, 0, time.UTC)
	r := Registry{Root: filepath.Join(t.TempDir(), "registry")}
	scope := registryTestScopeAt(t, "20260730t100000z-a1b2c3d4e5f60718", 101, now.Add(-25*time.Hour))
	if err := r.Register(scope); err != nil {
		t.Fatal(err)
	}
	owner := registryTestScopeAt(t, "20260731t110000z-b1b2c3d4e5f60718", 102, now.Add(-time.Hour))
	if err := r.Register(owner); err != nil {
		t.Fatal(err)
	}
	if err := r.MarkInfrastructureOwned(owner.RunID, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	originalHook := releaseSnapshotHook
	t.Cleanup(func() { releaseSnapshotHook = originalHook })
	releaseSnapshotHook = func() {
		if err := os.WriteFile(filepath.Join(r.Root, "infrastructure.json"), []byte("{"), 0o600); err != nil {
			panic(err)
		}
	}
	admin := &fakeDatabaseAdmin{exists: true}
	removed, err := ReapDatabases(context.Background(), r, admin, now, func(int, string) bool { return false })
	if err == nil || !strings.Contains(err.Error(), "release stale run") {
		t.Fatalf("ReapDatabases() error = %v, want release failure", err)
	}
	if len(removed) != 0 {
		t.Fatalf("removed = %v, want no successfully reaped database", removed)
	}
}

func (f *fakeDatabaseAdmin) Exec(_ context.Context, query string) error {
	f.queries = append(f.queries, query)
	return f.err
}
func (f *fakeDatabaseAdmin) Exists(context.Context, string) (bool, error) { return f.exists, f.err }
func (f *fakeDatabaseAdmin) Close(context.Context) error                  { return nil }

func TestCreateDatabase(t *testing.T) {
	name := "stratum_e2e_20260730t120102z_a1b2c3d4e5f60718"
	admin := &fakeDatabaseAdmin{}
	if err := CreateDatabase(context.Background(), admin, name); err != nil {
		t.Fatal(err)
	}
	if got, want := admin.queries, []string{`CREATE DATABASE "` + name + `"`}; len(got) != 1 || got[0] != want[0] {
		t.Fatalf("queries = %v, want %v", got, want)
	}
	admin.exists = true
	if err := CreateDatabase(context.Background(), admin, name); err == nil {
		t.Fatal("expected existing database error")
	}
}

func TestDropDatabase(t *testing.T) {
	name := "stratum_e2e_20260730t120102z_a1b2c3d4e5f60718"
	admin := &fakeDatabaseAdmin{exists: true}
	if err := DropDatabase(context.Background(), admin, name); err != nil {
		t.Fatal(err)
	}
	if got := admin.queries[0]; got != `DROP DATABASE "`+name+`" WITH (FORCE)` {
		t.Fatalf("query = %q", got)
	}
	for _, protected := range []string{"postgres", "template0", "template1", "stratum"} {
		if err := DropDatabase(context.Background(), admin, protected); err == nil {
			t.Fatalf("DropDatabase(%q) succeeded", protected)
		}
	}
}

func TestDatabaseErrorsPropagate(t *testing.T) {
	admin := &fakeDatabaseAdmin{err: errors.New("boom")}
	if err := CreateDatabase(context.Background(), admin, "stratum_e2e_20260730t120102z_a1b2c3d4e5f60718"); err == nil {
		t.Fatal("expected error")
	}
}
