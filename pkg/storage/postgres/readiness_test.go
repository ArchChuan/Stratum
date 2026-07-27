package postgres

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v2"
)

const defaultTenantReadinessQueryPattern = `SELECT EXISTS \(.*FROM information_schema\.tables.*FROM public\.tenants.*`

func TestCheckDefaultTenantReadiness(t *testing.T) {
	tests := []struct {
		name       string
		row        *bool
		queryErr   error
		wantErr    error
		wantDetail string
	}{
		{name: "healthy", row: boolPtr(true)},
		{name: "default tenant missing", queryErr: pgx.ErrNoRows, wantErr: ErrDefaultTenantMissing},
		{name: "default tenant schema missing", row: boolPtr(false), wantErr: ErrDefaultTenantSchemaMissing},
		{
			name:       "query failure",
			queryErr:   errors.New("database unavailable"),
			wantDetail: "database unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool, err := pgxmock.NewPool()
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()

			expectation := pool.ExpectQuery(regexp.MustCompile(defaultTenantReadinessQueryPattern).String())
			if tt.queryErr != nil {
				expectation.WillReturnError(tt.queryErr)
			} else {
				expectation.WillReturnRows(pgxmock.NewRows([]string{"agents_table_exists"}).AddRow(*tt.row))
			}

			err = CheckDefaultTenantReadiness(context.Background(), pool)
			if tt.wantErr == nil && tt.wantDetail == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("error = %v, want wrapped %v", err, tt.wantErr)
			}
			if tt.wantDetail != "" && (err == nil || !strings.Contains(err.Error(), tt.wantDetail)) {
				t.Fatalf("error = %v, want detail %q", err, tt.wantDetail)
			}
			if err := pool.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func boolPtr(value bool) *bool { return &value }
