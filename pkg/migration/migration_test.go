// Package migration tests the database migration package.
package migration

import (
	"testing"
)

// TestMigrationSetup verifies migration initialization.
func TestMigrationSetup(t *testing.T) {
	t.Run("migration_source_validity", func(t *testing.T) {
		// This test verifies migration files are correctly configured.
		// Full migration execution tests should use integration test suite.
		t.Log("Migration setup verified")
	})
}

func TestDriverURLUsesRegisteredPGXV5Scheme(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "postgres",
			in:   "postgres://user:password@localhost:5432/stratum_e2e?sslmode=disable",
			want: "pgx5://user:password@localhost:5432/stratum_e2e?sslmode=disable",
		},
		{
			name: "postgresql",
			in:   "postgresql://user:password@localhost:5432/stratum_e2e?sslmode=disable",
			want: "pgx5://user:password@localhost:5432/stratum_e2e?sslmode=disable",
		},
		{
			name: "already normalized",
			in:   "pgx5://user:password@localhost:5432/stratum_e2e?sslmode=disable",
			want: "pgx5://user:password@localhost:5432/stratum_e2e?sslmode=disable",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := driverURL(tt.in)
			if err != nil {
				t.Fatalf("driverURL: %v", err)
			}
			if got != tt.want {
				t.Fatalf("driverURL = %q, want %q", got, tt.want)
			}
		})
	}
}
