package main

import (
	"errors"
	"testing"

	"go.uber.org/zap"
)

func TestRunUsesEnvironmentDatabaseURLAndRequestedSQLDirectory(t *testing.T) {
	t.Parallel()

	var gotURL string
	var gotDir string
	err := run([]string{"--sql-dir", "/repo/pkg/migration/sql"}, func(key string) string {
		if key == "POSTGRES_URL" {
			return "postgres://user:password@localhost:5432/stratum_e2e?sslmode=disable"
		}
		return ""
	}, zap.NewNop(), func(databaseURL, sqlDir string, _ *zap.Logger) error {
		gotURL = databaseURL
		gotDir = sqlDir
		return nil
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if gotURL == "" {
		t.Fatal("database URL was not forwarded")
	}
	if gotDir != "/repo/pkg/migration/sql" {
		t.Fatalf("sql dir = %q", gotDir)
	}
}

func TestRunRejectsMissingDatabaseURL(t *testing.T) {
	t.Parallel()

	err := run(nil, func(string) string { return "" }, zap.NewNop(), nil)
	if err == nil || err.Error() != "POSTGRES_URL is required" {
		t.Fatalf("error = %v", err)
	}
}

func TestRunPropagatesMigrationFailure(t *testing.T) {
	t.Parallel()

	want := errors.New("migration failed")
	err := run(nil, func(key string) string {
		if key == "POSTGRES_URL" {
			return "postgres://user:password@localhost:5432/stratum_e2e?sslmode=disable"
		}
		return ""
	}, zap.NewNop(), func(string, string, *zap.Logger) error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want wrapped %v", err, want)
	}
}
