package e2erunscope

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

type fakeDatabaseAdmin struct {
	exists  bool
	queries []string
	err     error
	execErr error
}

func (f *fakeDatabaseAdmin) Exec(_ context.Context, query string) error {
	f.queries = append(f.queries, query)
	if f.execErr != nil {
		return f.execErr
	}
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

func TestReapDatabasesKeepsLeaseUntilDatabaseDropSucceeds(t *testing.T) {
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		execErr     error
		wantErr     bool
		wantLease   bool
		wantRemoved int
	}{
		{name: "releases lease after dropping database", wantRemoved: 1},
		{name: "keeps lease when database drop fails", execErr: errors.New("drop failed"), wantErr: true, wantLease: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			registry := Registry{Root: filepath.Join(t.TempDir(), "registry")}
			scope := registryTestScopeAt(t, "20260730t120102z-a1b2c3d4e5f60718", 101, now.Add(-25*time.Hour))
			if err := registry.Register(scope); err != nil {
				t.Fatal(err)
			}
			admin := &fakeDatabaseAdmin{exists: true, execErr: tc.execErr}
			processAlive := func(int, string) bool { return false }
			removed, err := ReapDatabases(context.Background(), registry, admin, now, processAlive)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ReapDatabases() error = %v, wantErr %v", err, tc.wantErr)
			}
			if len(removed) != tc.wantRemoved {
				t.Fatalf("removed = %v, want length %d", removed, tc.wantRemoved)
			}
			_, readErr := registry.Read(scope.RunID)
			if (readErr == nil) != tc.wantLease {
				t.Fatalf("lease exists = %v, want %v", readErr == nil, tc.wantLease)
			}
		})
	}
}
