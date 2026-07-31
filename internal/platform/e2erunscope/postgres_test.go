package e2erunscope

import (
	"context"
	"errors"
	"testing"
)

type fakeDatabaseAdmin struct {
	exists  bool
	queries []string
	err     error
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
