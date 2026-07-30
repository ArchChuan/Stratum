package e2erunscope

import (
	"bytes"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestNewScope(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 30, 20, 1, 2, 345, time.FixedZone("UTC+8", 8*60*60))
	repository := t.TempDir()
	scope, err := NewScope(repository, 12345, now, bytes.NewReader([]byte{0xa1, 0xb2, 0xc3, 0xd4, 0xe5, 0xf6, 0x07, 0x18}))
	if err != nil {
		t.Fatalf("NewScope() error = %v", err)
	}

	if scope.SchemaVersion != 1 {
		t.Errorf("SchemaVersion = %d, want 1", scope.SchemaVersion)
	}
	if got, want := scope.RunID, "20260730t120102z-a1b2c3d4e5f60718"; got != want {
		t.Errorf("RunID = %q, want %q", got, want)
	}
	if got, want := scope.DatabaseName, "stratum_e2e_20260730t120102z_a1b2c3d4e5f60718"; got != want {
		t.Errorf("DatabaseName = %q, want %q", got, want)
	}
	if !regexp.MustCompile(`^[0-9]{8}t[0-9]{6}z-[a-f0-9]{16}$`).MatchString(scope.RunID) {
		t.Errorf("RunID %q does not match the safe grammar", scope.RunID)
	}
	if !regexp.MustCompile(`^stratum_e2e_[0-9]{8}t[0-9]{6}z_[a-f0-9]{16}$`).MatchString(scope.DatabaseName) {
		t.Errorf("DatabaseName %q does not match the safe grammar", scope.DatabaseName)
	}
	if !scope.CreatedAt.Equal(now.UTC()) || scope.CreatedAt.Location() != time.UTC {
		t.Errorf("CreatedAt = %v, want UTC %v", scope.CreatedAt, now.UTC())
	}
	if got, want := scope.ExpiresAt, now.UTC().Add(24*time.Hour); !got.Equal(want) {
		t.Errorf("ExpiresAt = %v, want %v", got, want)
	}
	if scope.Repository != repository || !filepath.IsAbs(scope.Repository) {
		t.Errorf("Repository = %q, want absolute %q", scope.Repository, repository)
	}
	if scope.OwnerPID != 12345 {
		t.Errorf("OwnerPID = %d, want 12345", scope.OwnerPID)
	}
	if scope.Infrastructure.LeaseID != scope.RunID || scope.Infrastructure.StartedByE2E {
		t.Errorf("Infrastructure = %+v, want lease ID %q owned externally", scope.Infrastructure, scope.RunID)
	}
	assertValidPorts(t, scope.Ports)
}

func TestNewScopeRejectsInvalidInputs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		repository string
		ownerPID   int
		random     *bytes.Reader
	}{
		{name: "relative repository", repository: "relative/path", ownerPID: 1, random: bytes.NewReader(make([]byte, 8))},
		{name: "zero owner PID", repository: t.TempDir(), ownerPID: 0, random: bytes.NewReader(make([]byte, 8))},
		{name: "short randomness", repository: t.TempDir(), ownerPID: 1, random: bytes.NewReader(make([]byte, 7))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewScope(tt.repository, tt.ownerPID, time.Now(), tt.random); err == nil {
				t.Fatal("NewScope() error = nil, want error")
			}
		})
	}
}

func TestAllocatePorts(t *testing.T) {
	t.Parallel()

	ports, err := AllocatePorts()
	if err != nil {
		t.Fatalf("AllocatePorts() error = %v", err)
	}
	assertValidPorts(t, ports)
}

func TestURLs(t *testing.T) {
	t.Parallel()

	got := URLs(Ports{Frontend: 21001, Backend: 21002, OAuth: 21003, Fixture: 21004})
	want := RuntimeURLs{
		Frontend: "http://127.0.0.1:21001",
		Backend:  "http://127.0.0.1:21002",
		OAuth:    "http://127.0.0.1:21003",
		Fixture:  "http://127.0.0.1:21004",
	}
	if got != want {
		t.Errorf("URLs() = %+v, want %+v", got, want)
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()

	valid := validTestScope(t)
	tests := []struct {
		name   string
		mutate func(*Scope)
	}{
		{name: "valid", mutate: func(*Scope) {}},
		{name: "schema version", mutate: func(s *Scope) { s.SchemaVersion = 2 }},
		{name: "run ID grammar", mutate: func(s *Scope) { s.RunID = "unsafe" }},
		{name: "database grammar", mutate: func(s *Scope) { s.DatabaseName = "postgres" }},
		{name: "lease mismatch", mutate: func(s *Scope) { s.Infrastructure.LeaseID = "other" }},
		{name: "owner PID", mutate: func(s *Scope) { s.OwnerPID = -1 }},
		{name: "relative repository", mutate: func(s *Scope) { s.Repository = "relative" }},
		{name: "zero created timestamp", mutate: func(s *Scope) { s.CreatedAt = time.Time{} }},
		{name: "wrong expiry", mutate: func(s *Scope) { s.ExpiresAt = s.ExpiresAt.Add(time.Second) }},
		{name: "port below range", mutate: func(s *Scope) { s.Ports.Frontend = 0 }},
		{name: "port above range", mutate: func(s *Scope) { s.Ports.Backend = 65536 }},
		{name: "duplicate port", mutate: func(s *Scope) { s.Ports.OAuth = s.Ports.Backend }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scope := valid
			tt.mutate(&scope)
			err := Validate(scope)
			if tt.name == "valid" && err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
			if tt.name != "valid" && err == nil {
				t.Fatal("Validate() error = nil, want error")
			}
		})
	}
}

func TestDatabaseURL(t *testing.T) {
	t.Parallel()

	const target = "stratum_e2e_20260730t120102z_a1b2c3d4e5f60718"
	tests := []struct {
		name string
		base string
		want string
	}{
		{
			name: "postgres credentials port and TLS query",
			base: "postgres://e2e-user:p%40ss@127.0.0.1:5432/stratum_test?sslmode=require&application_name=e2e",
			want: "postgres://e2e-user:p%40ss@127.0.0.1:5432/" + target + "?sslmode=require&application_name=e2e",
		},
		{
			name: "postgresql localhost",
			base: "postgresql://user:secret@localhost/e2e_base?sslmode=disable",
			want: "postgresql://user:secret@localhost/" + target + "?sslmode=disable",
		},
		{
			name: "compose postgres host",
			base: "postgres://user:secret@postgres:5432/stratum_e2e",
			want: "postgres://user:secret@postgres:5432/" + target,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DatabaseURL(tt.base, target)
			if err != nil {
				t.Fatalf("DatabaseURL() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("DatabaseURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDatabaseURLRejectsUnsafeInputsWithoutLeakingCredentials(t *testing.T) {
	t.Parallel()

	const secret = "do-not-leak"
	tests := []struct {
		name   string
		base   string
		target string
	}{
		{name: "unsupported scheme", base: "mysql://user:" + secret + "@127.0.0.1/stratum_test", target: safeDatabaseName()},
		{name: "remote host", base: "postgres://user:" + secret + "@db.example.com/stratum_test", target: safeDatabaseName()},
		{name: "non E2E base database", base: "postgres://user:" + secret + "@127.0.0.1/production", target: safeDatabaseName()},
		{name: "fragment", base: "postgres://user:" + secret + "@127.0.0.1/stratum_test#fragment", target: safeDatabaseName()},
		{name: "unsafe target", base: "postgres://user:" + secret + "@127.0.0.1/stratum_test", target: "unsafe;drop database"},
		{name: "missing host", base: "postgres:///stratum_test", target: safeDatabaseName()},
		{name: "malformed", base: "://user:" + secret, target: safeDatabaseName()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DatabaseURL(tt.base, tt.target)
			if err == nil {
				t.Fatal("DatabaseURL() error = nil, want error")
			}
			if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), tt.base) {
				t.Errorf("DatabaseURL() error leaks DSN credentials: %v", err)
			}
		})
	}
}

func TestMaintenanceURL(t *testing.T) {
	t.Parallel()

	base := "postgres://e2e-user:p%40ss@127.0.0.1:5432/stratum_test?sslmode=verify-full&connect_timeout=5"
	got, err := MaintenanceURL(base)
	if err != nil {
		t.Fatalf("MaintenanceURL() error = %v", err)
	}
	want := "postgres://e2e-user:p%40ss@127.0.0.1:5432/postgres?sslmode=verify-full&connect_timeout=5"
	if got != want {
		t.Errorf("MaintenanceURL() = %q, want %q", got, want)
	}
}

func TestMaintenanceURLRejectsUnsafeBaseWithoutLeakingCredentials(t *testing.T) {
	t.Parallel()

	const secret = "maintenance-secret"
	tests := []struct {
		name string
		base string
	}{
		{name: "remote host", base: "postgres://user:" + secret + "@remote/stratum_test"},
		{name: "production database", base: "postgres://user:" + secret + "@localhost/production"},
		{name: "fragment", base: "postgres://user:" + secret + "@localhost/e2e#fragment"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := MaintenanceURL(tt.base)
			if err == nil {
				t.Fatal("MaintenanceURL() error = nil, want error")
			}
			if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), tt.base) {
				t.Errorf("MaintenanceURL() error leaks DSN credentials: %v", err)
			}
		})
	}
}

func validTestScope(t *testing.T) Scope {
	t.Helper()
	createdAt := time.Date(2026, time.July, 30, 12, 1, 2, 0, time.UTC)
	runID := "20260730t120102z-a1b2c3d4e5f60718"
	return Scope{
		SchemaVersion: 1,
		RunID:         runID,
		OwnerPID:      12345,
		CreatedAt:     createdAt,
		ExpiresAt:     createdAt.Add(24 * time.Hour),
		Repository:    t.TempDir(),
		DatabaseName:  "stratum_e2e_20260730t120102z_a1b2c3d4e5f60718",
		Ports:         Ports{Frontend: 21001, Backend: 21002, OAuth: 21003, Fixture: 21004},
		Infrastructure: InfrastructureLease{
			LeaseID: runID,
		},
	}
}

func safeDatabaseName() string {
	return "stratum_e2e_20260730t120102z_a1b2c3d4e5f60718"
}

func assertValidPorts(t *testing.T, ports Ports) {
	t.Helper()
	values := []int{ports.Frontend, ports.Backend, ports.OAuth, ports.Fixture}
	seen := make(map[int]struct{}, len(values))
	for _, port := range values {
		if port < 1 || port > 65535 {
			t.Errorf("port = %d, want a non-zero TCP port", port)
		}
		if _, exists := seen[port]; exists {
			t.Errorf("duplicate port %d in %+v", port, ports)
		}
		seen[port] = struct{}{}
	}
}

func TestDatabaseURLReplacesOnlyPath(t *testing.T) {
	t.Parallel()

	const base = "postgres://user:password@localhost:5433/my_test_db?sslmode=require&search_path=public"
	got, err := DatabaseURL(base, safeDatabaseName())
	if err != nil {
		t.Fatalf("DatabaseURL() error = %v", err)
	}
	baseURL, _ := url.Parse(base)
	gotURL, _ := url.Parse(got)
	baseURL.Path = gotURL.Path
	if gotURL.String() != baseURL.String() {
		t.Errorf("DatabaseURL() changed fields besides path: got %q, reconstructed base %q", gotURL, baseURL)
	}
}
