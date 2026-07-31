package e2erunscope

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestRegistryRegisterAndReadSecureMetadata(t *testing.T) {
	registry := Registry{Root: filepath.Join(t.TempDir(), "registry")}
	scope := registryTestScope(t, "20260730t120102z-a1b2c3d4e5f60718", 101)
	if err := registry.Register(scope); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	assertMode(t, registry.Root, 0o700)
	assertMode(t, filepath.Join(registry.Root, "runs"), 0o700)
	leasePath := filepath.Join(registry.Root, "runs", scope.RunID+".json")
	assertMode(t, leasePath, 0o600)
	got, err := registry.Read(scope.RunID)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if got.RunID != scope.RunID || got.OwnerPID != scope.OwnerPID {
		t.Fatalf("Read() = %+v, want run %q PID %d", got, scope.RunID, scope.OwnerPID)
	}

	entries, err := os.ReadDir(filepath.Dir(leasePath))
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(leasePath) {
		t.Fatalf("runs entries = %v, want only atomically renamed lease", entries)
	}
}

func TestRegistryRejectsUnsafePaths(t *testing.T) {
	scope := registryTestScope(t, "20260730t120102z-a1b2c3d4e5f60718", 101)
	tests := []struct {
		name    string
		prepare func(*testing.T) Registry
		run     func(Registry) error
	}{
		{
			name: "symlink root",
			prepare: func(t *testing.T) Registry {
				target := t.TempDir()
				root := filepath.Join(t.TempDir(), "registry")
				if err := os.Symlink(target, root); err != nil {
					t.Fatal(err)
				}
				return Registry{Root: root}
			},
			run: func(r Registry) error { return r.Register(scope) },
		},
		{
			name: "symlink root ancestor",
			prepare: func(t *testing.T) Registry {
				target := t.TempDir()
				ancestor := filepath.Join(t.TempDir(), "ancestor")
				if err := os.Symlink(target, ancestor); err != nil {
					t.Fatal(err)
				}
				return Registry{Root: filepath.Join(ancestor, "registry")}
			},
			run: func(r Registry) error { return r.Register(scope) },
		},
		{
			name:    "unsafe run ID",
			prepare: func(t *testing.T) Registry { return Registry{Root: filepath.Join(t.TempDir(), "registry")} },
			run:     func(r Registry) error { _, err := r.Read("../escape"); return err },
		},
		{
			name: "symlink lease",
			prepare: func(t *testing.T) Registry {
				r := Registry{Root: filepath.Join(t.TempDir(), "registry")}
				if err := r.Register(scope); err != nil {
					t.Fatal(err)
				}
				target := filepath.Join(t.TempDir(), "target")
				if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
					t.Fatal(err)
				}
				lease := filepath.Join(r.Root, "runs", scope.RunID+".json")
				if err := os.Remove(lease); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, lease); err != nil {
					t.Fatal(err)
				}
				return r
			},
			run: func(r Registry) error { _, err := r.Read(scope.RunID); return err },
		},
		{
			name: "group writable lease",
			prepare: func(t *testing.T) Registry {
				r := Registry{Root: filepath.Join(t.TempDir(), "registry")}
				if err := r.Register(scope); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(filepath.Join(r.Root, "runs", scope.RunID+".json"), 0o620); err != nil {
					t.Fatal(err)
				}
				return r
			},
			run: func(r Registry) error { _, err := r.Read(scope.RunID); return err },
		},
		{
			name: "non-exact lease mode",
			prepare: func(t *testing.T) Registry {
				r := Registry{Root: filepath.Join(t.TempDir(), "registry")}
				if err := r.Register(scope); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(filepath.Join(r.Root, "runs", scope.RunID+".json"), 0o400); err != nil {
					t.Fatal(err)
				}
				return r
			},
			run: func(r Registry) error { _, err := r.Read(scope.RunID); return err },
		},
		{
			name: "world writable infrastructure",
			prepare: func(t *testing.T) Registry {
				r := Registry{Root: filepath.Join(t.TempDir(), "registry")}
				if err := r.Register(scope); err != nil {
					t.Fatal(err)
				}
				if err := r.MarkInfrastructureOwned(scope.RunID, time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(filepath.Join(r.Root, "infrastructure.json"), 0o602); err != nil {
					t.Fatal(err)
				}
				return r
			},
			run: func(r Registry) error { _, err := r.Release(scope.RunID); return err },
		},
		{
			name: "symlink infrastructure",
			prepare: func(t *testing.T) Registry {
				r := Registry{Root: filepath.Join(t.TempDir(), "registry")}
				if err := r.Register(scope); err != nil {
					t.Fatal(err)
				}
				target := filepath.Join(t.TempDir(), "infrastructure.json")
				if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, filepath.Join(r.Root, "infrastructure.json")); err != nil {
					t.Fatal(err)
				}
				return r
			},
			run: func(r Registry) error { _, err := r.Release(scope.RunID); return err },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := tt.prepare(t)
			if err := tt.run(r); err == nil {
				t.Fatal("error = nil, want unsafe metadata rejection")
			}
		})
	}
}

func TestRegistryRejectsWrongUID(t *testing.T) {
	info, err := os.Lstat(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	wrongUID := uint32(0)
	if int64(wrongUID) == int64(os.Geteuid()) {
		wrongUID = 1
	}
	if err := validateUID(fileInfoWithStat{FileInfo: info, stat: &syscall.Stat_t{Uid: wrongUID}}); err == nil {
		t.Fatal("validateUID() error = nil, want wrong UID rejection")
	}

	if runtime.GOOS == "windows" || os.Geteuid() != 0 {
		t.Skip("changing file ownership requires root")
	}
	r := Registry{Root: filepath.Join(t.TempDir(), "registry")}
	scope := registryTestScope(t, "20260730t120102z-a1b2c3d4e5f60718", 101)
	if err := r.Register(scope); err != nil {
		t.Fatal(err)
	}
	lease := filepath.Join(r.Root, "runs", scope.RunID+".json")
	if err := os.Chown(lease, 65534, -1); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Read(scope.RunID); err == nil {
		t.Fatal("Read() error = nil, want wrong UID rejection")
	}
}

func TestRegistryRegisterRejectsDuplicateLease(t *testing.T) {
	r := Registry{Root: filepath.Join(t.TempDir(), "registry")}
	scope := registryTestScope(t, "20260730t120102z-a1b2c3d4e5f60718", 101)
	if err := r.Register(scope); err != nil {
		t.Fatal(err)
	}
	changed := scope
	changed.OwnerPID = 202
	if err := r.Register(changed); err == nil {
		t.Fatal("second Register() error = nil, want exclusive-create failure")
	}
	got, err := r.Read(scope.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if got.OwnerPID != scope.OwnerPID {
		t.Fatalf("OwnerPID = %d, want original %d", got.OwnerPID, scope.OwnerPID)
	}
}

func TestRegistryRegisterPublishesCompleteLeaseExclusively(t *testing.T) {
	r := Registry{Root: filepath.Join(t.TempDir(), "registry")}
	scope := registryTestScope(t, "20260730t120102z-a1b2c3d4e5f60718", 101)
	scope.Repository = "/" + strings.Repeat("r", 4<<10)
	leasePath := r.leasePath(scope.RunID)
	data, err := json.Marshal(scope)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Register(scope); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	info, err := os.Lstat(leasePath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != int64(len(data)) {
		t.Fatalf("visible lease size = %d, want complete size %d", info.Size(), len(data))
	}
	entries, err := os.ReadDir(filepath.Dir(leasePath))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(leasePath) {
		t.Fatalf("published entries = %v, want only final lease", entries)
	}

	changed := scope
	changed.OwnerPID = 202
	if err := r.Register(changed); err == nil {
		t.Fatal("second Register() error = nil, want exclusive-create failure")
	}
	got, err := r.Read(scope.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if got.OwnerPID != scope.OwnerPID {
		t.Fatalf("OwnerPID = %d, want original %d", got.OwnerPID, scope.OwnerPID)
	}
}

func TestRegistryPropagatesDirectorySyncFailures(t *testing.T) {
	r := Registry{Root: filepath.Join(t.TempDir(), "registry")}
	first := registryTestScope(t, "20260730t120102z-a1b2c3d4e5f60718", 101)
	if err := r.Register(first); err != nil {
		t.Fatal(err)
	}

	original := syncRegistryDirectory
	t.Cleanup(func() { syncRegistryDirectory = original })
	syncRegistryDirectory = func(*os.File) error { return errors.New("injected directory sync failure") }
	second := registryTestScope(t, "20260730t120103z-b1b2c3d4e5f60718", 202)
	if err := r.Register(second); err == nil || !strings.Contains(err.Error(), "sync") {
		t.Fatalf("Register() error = %v, want directory sync failure", err)
	}
}

func TestRegistryReleasePropagatesDirectorySyncFailure(t *testing.T) {
	r := Registry{Root: filepath.Join(t.TempDir(), "registry")}
	scope := registryTestScope(t, "20260730t120102z-a1b2c3d4e5f60718", 101)
	if err := r.Register(scope); err != nil {
		t.Fatal(err)
	}

	original := syncRegistryDirectory
	t.Cleanup(func() { syncRegistryDirectory = original })
	syncRegistryDirectory = func(*os.File) error { return errors.New("injected directory sync failure") }
	if _, err := r.Release(scope.RunID); err == nil || !strings.Contains(err.Error(), "sync") {
		t.Fatalf("Release() error = %v, want directory sync failure", err)
	}
}

func TestRegistryRegisterRejectsRacingDuplicateLease(t *testing.T) {
	r := Registry{Root: filepath.Join(t.TempDir(), "registry")}
	first := registryTestScope(t, "20260730t120102z-a1b2c3d4e5f60718", 101)
	second := first
	second.OwnerPID = 202
	start := make(chan struct{})
	results := make(chan struct {
		pid int
		err error
	}, 2)
	for _, scope := range []Scope{first, second} {
		go func() {
			<-start
			results <- struct {
				pid int
				err error
			}{pid: scope.OwnerPID, err: r.Register(scope)}
		}()
	}
	close(start)

	winnerPID := 0
	for range 2 {
		result := <-results
		if result.err == nil {
			if winnerPID != 0 {
				t.Fatal("both racing Register() calls succeeded")
			}
			winnerPID = result.pid
		}
	}
	if winnerPID == 0 {
		t.Fatal("both racing Register() calls failed")
	}
	got, err := r.Read(first.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if got.OwnerPID != winnerPID {
		t.Fatalf("OwnerPID = %d, want winning PID %d", got.OwnerPID, winnerPID)
	}
}

func TestRegistryRenameNoReplacePreservesExistingDestination(t *testing.T) {
	directory := t.TempDir()
	tempPath := filepath.Join(directory, "lease.tmp")
	finalPath := filepath.Join(directory, "lease.json")
	if err := os.WriteFile(tempPath, []byte("complete lease"), 0o600); err != nil {
		t.Fatal(err)
	}
	hostile := []byte("hostile lease")
	if err := os.WriteFile(finalPath, hostile, 0o600); err != nil {
		t.Fatal(err)
	}

	err := renameNoReplace(tempPath, finalPath)
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("renameNoReplace() error = %v, want already exists", err)
	}
	got, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(hostile) {
		t.Fatalf("destination = %q, want preserved %q", got, hostile)
	}
}

func TestRegistryStale(t *testing.T) {
	now := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		age   time.Duration
		alive bool
		want  int
	}{
		{name: "live PID retained after TTL", age: 25 * time.Hour, alive: true, want: 0},
		{name: "exactly 24 hours retained", age: 24 * time.Hour, alive: false, want: 0},
		{name: "older than 24 hours stale", age: 24*time.Hour + time.Nanosecond, alive: false, want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := Registry{Root: filepath.Join(t.TempDir(), "registry")}
			scope := registryTestScopeAt(t, "20260730t120102z-a1b2c3d4e5f60718", 101, now.Add(-tt.age))
			if err := r.Register(scope); err != nil {
				t.Fatal(err)
			}
			got, err := r.Stale(now, func(pid int, repository string) bool {
				if pid != scope.OwnerPID || repository != scope.Repository {
					t.Fatalf("processAlive(%d, %q)", pid, repository)
				}
				return tt.alive
			})
			if err != nil {
				t.Fatalf("Stale() error = %v", err)
			}
			if len(got) != tt.want {
				t.Fatalf("len(Stale()) = %d, want %d", len(got), tt.want)
			}
		})
	}
}

func TestRegistryReleaseLifecycle(t *testing.T) {
	r := Registry{Root: filepath.Join(t.TempDir(), "registry")}
	a := registryTestScope(t, "20260730t120102z-a1b2c3d4e5f60718", 101)
	b := registryTestScope(t, "20260730t120103z-b1b2c3d4e5f60718", 102)
	if err := r.Register(a); err != nil {
		t.Fatal(err)
	}
	if err := r.MarkInfrastructureOwned(a.RunID, a.CreatedAt); err != nil {
		t.Fatal(err)
	}
	assertMode(t, filepath.Join(r.Root, "infrastructure.json"), 0o600)
	if err := r.Register(b); err != nil {
		t.Fatal(err)
	}

	first, err := r.Release(a.RunID)
	if err != nil {
		t.Fatalf("Release(A) error = %v", err)
	}
	if first != (ReleaseResult{}) {
		t.Fatalf("Release(A) = %+v, want zero result", first)
	}
	if _, err := r.Read(b.RunID); err != nil {
		t.Fatalf("Read(B) error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(r.Root, "infrastructure.json")); err != nil {
		t.Fatalf("infrastructure removed early: %v", err)
	}

	last, err := r.Release(b.RunID)
	if err != nil {
		t.Fatalf("Release(B) error = %v", err)
	}
	want := ReleaseResult{LastReference: true, StopOwnedInfrastructure: true, OwnershipRunID: a.RunID}
	if last != want {
		t.Fatalf("Release(B) = %+v, want %+v", last, want)
	}
	if _, err := os.Stat(filepath.Join(r.Root, "infrastructure.json")); err != nil {
		t.Fatalf("infrastructure removed before confirmation: %v", err)
	}

	if err := r.ConfirmInfrastructureStopped(b.RunID); err == nil {
		t.Fatal("ConfirmInfrastructureStopped(wrong owner) error = nil")
	}
	if err := r.ConfirmInfrastructureStopped(a.RunID); err != nil {
		t.Fatalf("ConfirmInfrastructureStopped() error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(r.Root, "infrastructure.json")); !os.IsNotExist(err) {
		t.Fatalf("infrastructure metadata still exists: %v", err)
	}
}

func TestRegistryReleaseSerializesConcurrentRegister(t *testing.T) {
	r := Registry{Root: filepath.Join(t.TempDir(), "registry")}
	a := registryTestScope(t, "20260730t120102z-a1b2c3d4e5f60718", 101)
	b := registryTestScope(t, "20260730t120103z-b1b2c3d4e5f60718", 102)
	if err := r.Register(a); err != nil {
		t.Fatal(err)
	}
	if err := r.MarkInfrastructureOwned(a.RunID, a.CreatedAt); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{}, 1)
	proceed := make(chan struct{})
	original := releaseSnapshotHook
	t.Cleanup(func() { releaseSnapshotHook = original })
	releaseSnapshotHook = func() {
		entered <- struct{}{}
		<-proceed
	}
	releaseDone := make(chan struct {
		result ReleaseResult
		err    error
	}, 1)
	go func() {
		result, err := r.Release(a.RunID)
		releaseDone <- struct {
			result ReleaseResult
			err    error
		}{result: result, err: err}
	}()
	<-entered
	registerDone := make(chan error, 1)
	go func() { registerDone <- r.Register(b) }()
	select {
	case err := <-registerDone:
		t.Fatalf("Register(B) completed during Release(A) transaction: %v", err)
	case <-time.After(250 * time.Millisecond):
	}
	close(proceed)
	released := <-releaseDone
	if released.err != nil {
		t.Fatal(released.err)
	}
	if !released.result.LastReference || !released.result.StopOwnedInfrastructure {
		t.Fatalf("Release(A) = %+v, want last owned reference", released.result)
	}
	if err := <-registerDone; err != nil {
		t.Fatalf("Register(B) error = %v", err)
	}
}

func TestRegistryReleaseSerializesConcurrentReleases(t *testing.T) {
	r := Registry{Root: filepath.Join(t.TempDir(), "registry")}
	a := registryTestScope(t, "20260730t120102z-a1b2c3d4e5f60718", 101)
	b := registryTestScope(t, "20260730t120103z-b1b2c3d4e5f60718", 102)
	if err := r.Register(a); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(b); err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{}, 2)
	proceed := make(chan struct{})
	original := releaseSnapshotHook
	t.Cleanup(func() { releaseSnapshotHook = original })
	var first sync.Once
	releaseSnapshotHook = func() {
		entered <- struct{}{}
		first.Do(func() { <-proceed })
	}
	results := make(chan ReleaseResult, 2)
	errors := make(chan error, 2)
	for _, runID := range []string{a.RunID, b.RunID} {
		go func() {
			result, err := r.Release(runID)
			results <- result
			errors <- err
		}()
	}
	<-entered
	select {
	case <-entered:
		t.Fatal("both Release transactions took concurrent lease snapshots")
	case <-time.After(250 * time.Millisecond):
	}
	close(proceed)
	lastReferences := 0
	for range 2 {
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
		if (<-results).LastReference {
			lastReferences++
		}
	}
	if lastReferences != 1 {
		t.Fatalf("last-reference results = %d, want exactly one", lastReferences)
	}
}

func TestRegistryMalformedMetadataFailsClosed(t *testing.T) {
	tests := []struct {
		name    string
		corrupt func(*testing.T, Registry, Scope)
		action  func(Registry, Scope) error
	}{
		{
			name: "lease blocks release",
			corrupt: func(t *testing.T, r Registry, scope Scope) {
				other := registryTestScope(t, "20260730t120103z-b1b2c3d4e5f60718", 102)
				if err := r.Register(other); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(r.Root, "runs", other.RunID+".json"), []byte("{"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			action: func(r Registry, scope Scope) error { _, err := r.Release(scope.RunID); return err },
		},
		{
			name: "infrastructure blocks release",
			corrupt: func(t *testing.T, r Registry, scope Scope) {
				if err := r.MarkInfrastructureOwned(scope.RunID, scope.CreatedAt); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(r.Root, "infrastructure.json"), []byte("{"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			action: func(r Registry, scope Scope) error { _, err := r.Release(scope.RunID); return err },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := Registry{Root: filepath.Join(t.TempDir(), "registry")}
			scope := registryTestScope(t, "20260730t120102z-a1b2c3d4e5f60718", 101)
			if err := r.Register(scope); err != nil {
				t.Fatal(err)
			}
			tt.corrupt(t, r, scope)
			if err := tt.action(r, scope); err == nil {
				t.Fatal("action error = nil, want malformed metadata error")
			}
			if _, err := os.Lstat(filepath.Join(r.Root, "runs", scope.RunID+".json")); err != nil {
				t.Fatalf("requested lease changed on failure: %v", err)
			}
		})
	}
}

func registryTestScope(t *testing.T, runID string, pid int) Scope {
	t.Helper()
	return registryTestScopeAt(t, runID, pid, time.Date(2026, time.July, 30, 12, 1, 2, 0, time.UTC))
}

func registryTestScopeAt(t *testing.T, runID string, pid int, createdAt time.Time) Scope {
	t.Helper()
	scope := Scope{
		SchemaVersion: 1, RunID: runID, OwnerPID: pid, CreatedAt: createdAt, ExpiresAt: createdAt.Add(24 * time.Hour),
		Repository: t.TempDir(), DatabaseName: "stratum_e2e_" + runID[:16] + "_" + runID[17:],
		Ports:          Ports{Frontend: 21001, Backend: 21002, OAuth: 21003, Fixture: 21004},
		Infrastructure: InfrastructureLease{LeaseID: runID},
	}
	if err := Validate(scope); err != nil {
		t.Fatalf("test scope invalid: %v", err)
	}
	return scope
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("Lstat(%q) error = %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("mode(%q) = %04o, want %04o", path, got, want)
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && int64(stat.Uid) != int64(os.Geteuid()) {
		t.Errorf("uid(%q) = %d, want %d", path, stat.Uid, os.Geteuid())
	}
}

type fileInfoWithStat struct {
	os.FileInfo
	stat *syscall.Stat_t
}

func (f fileInfoWithStat) Sys() any { return f.stat }
