package e2erunscope

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const (
	registryDirectoryMode = 0o700
	registryMetadataMode  = 0o600
)

type Registry struct {
	Root string
}

type InfrastructureState struct {
	StartedByE2E bool      `json:"started_by_e2e"`
	StartedAt    time.Time `json:"started_at"`
	StartedByRun string    `json:"started_by_run"`
}

type ReleaseResult struct {
	LastReference           bool   `json:"last_reference"`
	StopOwnedInfrastructure bool   `json:"stop_owned_infrastructure"`
	OwnershipRunID          string `json:"ownership_run_id,omitempty"`
}

func (r Registry) Register(scope Scope) error {
	if err := Validate(scope); err != nil {
		return fmt.Errorf("registry: validate scope: %w", err)
	}
	if err := safeRunID(scope.RunID); err != nil {
		return err
	}
	if err := r.ensureDirectories(); err != nil {
		return err
	}
	data, err := json.Marshal(scope)
	if err != nil {
		return fmt.Errorf("registry: encode lease: %w", err)
	}
	path := r.leasePath(scope.RunID)
	if _, err := os.Lstat(path); err == nil {
		return errors.New("registry: lease already exists")
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("registry: inspect lease: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, registryMetadataMode)
	if err != nil {
		return fmt.Errorf("registry: create lease: %w", err)
	}
	if err := writeAndClose(file, data); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("registry: write lease: %w", err)
	}
	return nil
}

func (r Registry) Read(runID string) (Scope, error) {
	if err := safeRunID(runID); err != nil {
		return Scope{}, err
	}
	if err := r.validateDirectories(); err != nil {
		return Scope{}, err
	}
	return readLease(r.leasePath(runID), runID)
}

func (r Registry) MarkInfrastructureOwned(runID string, now time.Time) error {
	if err := safeRunID(runID); err != nil {
		return err
	}
	if _, err := r.Read(runID); err != nil {
		return fmt.Errorf("registry: read owner lease: %w", err)
	}
	state := InfrastructureState{StartedByE2E: true, StartedAt: now.UTC(), StartedByRun: runID}
	return r.writeInfrastructure(state)
}

func (r Registry) Release(runID string) (ReleaseResult, error) {
	if err := safeRunID(runID); err != nil {
		return ReleaseResult{}, err
	}
	if err := r.validateDirectories(); err != nil {
		return ReleaseResult{}, err
	}
	leases, err := r.readAllLeases()
	if err != nil {
		return ReleaseResult{}, err
	}
	if err := requireLease(leases, runID); err != nil {
		return ReleaseResult{}, err
	}
	state, stateExists, err := r.readInfrastructure()
	if err != nil {
		return ReleaseResult{}, err
	}
	path := r.leasePath(runID)
	if _, err := inspectMetadata(path); err != nil {
		return ReleaseResult{}, fmt.Errorf("registry: inspect lease before removal: %w", err)
	}
	if err := os.Remove(path); err != nil {
		return ReleaseResult{}, fmt.Errorf("registry: remove lease: %w", err)
	}
	if len(leases) != 1 {
		return ReleaseResult{}, nil
	}
	return lastReleaseResult(state, stateExists), nil
}

func lastReleaseResult(state InfrastructureState, stateExists bool) ReleaseResult {
	result := ReleaseResult{LastReference: true}
	if stateExists && state.StartedByE2E {
		result.StopOwnedInfrastructure = true
		result.OwnershipRunID = state.StartedByRun
	}
	return result
}

func requireLease(leases []Scope, runID string) error {
	for _, lease := range leases {
		if lease.RunID == runID {
			return nil
		}
	}
	return errors.New("registry: lease not found")
}

func (r Registry) ConfirmInfrastructureStopped(ownershipRunID string) error {
	if err := safeRunID(ownershipRunID); err != nil {
		return err
	}
	if err := r.validateDirectories(); err != nil {
		return err
	}
	state, exists, err := r.readInfrastructure()
	if err != nil {
		return err
	}
	if !exists {
		return errors.New("registry: infrastructure ownership not found")
	}
	if state.StartedByRun != ownershipRunID {
		return errors.New("registry: infrastructure ownership changed")
	}
	path := r.infrastructurePath()
	if _, err := inspectMetadata(path); err != nil {
		return fmt.Errorf("registry: inspect infrastructure before removal: %w", err)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("registry: remove infrastructure metadata: %w", err)
	}
	return nil
}

func (r Registry) Stale(now time.Time, processAlive func(int, string) bool) ([]Scope, error) {
	if processAlive == nil {
		return nil, errors.New("registry: process liveness callback is required")
	}
	if err := r.validateDirectories(); err != nil {
		return nil, err
	}
	leases, err := r.readAllLeases()
	if err != nil {
		return nil, err
	}
	stale := make([]Scope, 0, len(leases))
	for _, lease := range leases {
		if now.After(lease.ExpiresAt) && !processAlive(lease.OwnerPID, lease.Repository) {
			stale = append(stale, lease)
		}
	}
	return stale, nil
}

func (r Registry) ensureDirectories() error {
	if r.Root == "" || !filepath.IsAbs(r.Root) {
		return errors.New("registry: root must be absolute")
	}
	if info, err := os.Lstat(r.Root); err == nil {
		if err := validateDirectory(r.Root, info); err != nil {
			return err
		}
	} else if os.IsNotExist(err) {
		if err := os.Mkdir(r.Root, registryDirectoryMode); err != nil {
			return fmt.Errorf("registry: create root: %w", err)
		}
	} else {
		return fmt.Errorf("registry: inspect root: %w", err)
	}
	runs := r.runsPath()
	if info, err := os.Lstat(runs); err == nil {
		return validateDirectory(runs, info)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("registry: inspect runs directory: %w", err)
	}
	if err := os.Mkdir(runs, registryDirectoryMode); err != nil {
		return fmt.Errorf("registry: create runs directory: %w", err)
	}
	return nil
}

func (r Registry) validateDirectories() error {
	if r.Root == "" || !filepath.IsAbs(r.Root) {
		return errors.New("registry: root must be absolute")
	}
	for _, path := range []string{r.Root, r.runsPath()} {
		info, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("registry: inspect directory: %w", err)
		}
		if err := validateDirectory(path, info); err != nil {
			return err
		}
	}
	return nil
}

func (r Registry) readAllLeases() ([]Scope, error) {
	entries, err := os.ReadDir(r.runsPath())
	if err != nil {
		return nil, fmt.Errorf("registry: list leases: %w", err)
	}
	leases := make([]Scope, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if filepath.Ext(name) != ".json" {
			return nil, fmt.Errorf("registry: unexpected lease entry %q", name)
		}
		runID := name[:len(name)-len(".json")]
		if err := safeRunID(runID); err != nil {
			return nil, fmt.Errorf("registry: invalid lease filename: %w", err)
		}
		lease, err := readLease(filepath.Join(r.runsPath(), name), runID)
		if err != nil {
			return nil, err
		}
		leases = append(leases, lease)
	}
	return leases, nil
}

func (r Registry) writeInfrastructure(state InfrastructureState) error {
	if err := validateInfrastructure(state); err != nil {
		return err
	}
	if err := r.validateDirectories(); err != nil {
		return err
	}
	path := r.infrastructurePath()
	if _, err := os.Lstat(path); err == nil {
		if _, _, readErr := r.readInfrastructure(); readErr != nil {
			return readErr
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("registry: inspect infrastructure: %w", err)
	}
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("registry: encode infrastructure: %w", err)
	}
	return atomicWrite(path, data)
}

func (r Registry) readInfrastructure() (InfrastructureState, bool, error) {
	path := r.infrastructurePath()
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return InfrastructureState{}, false, nil
	} else if err != nil {
		return InfrastructureState{}, false, fmt.Errorf("registry: inspect infrastructure: %w", err)
	}
	var state InfrastructureState
	if err := decodeMetadata(path, &state); err != nil {
		return InfrastructureState{}, false, fmt.Errorf("registry: read infrastructure: %w", err)
	}
	if err := validateInfrastructure(state); err != nil {
		return InfrastructureState{}, false, err
	}
	return state, true, nil
}

func readLease(path string, runID string) (Scope, error) {
	var scope Scope
	if err := decodeMetadata(path, &scope); err != nil {
		return Scope{}, fmt.Errorf("registry: read lease: %w", err)
	}
	if scope.RunID != runID {
		return Scope{}, errors.New("registry: lease filename does not match run ID")
	}
	if err := Validate(scope); err != nil {
		return Scope{}, fmt.Errorf("registry: validate lease: %w", err)
	}
	return scope, nil
}

func decodeMetadata(path string, destination any) error {
	if _, err := inspectMetadata(path); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return fmt.Errorf("open metadata: %w", err)
	}
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	decodeErr := decoder.Decode(destination)
	if decodeErr == nil {
		var trailing any
		if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			decodeErr = errors.New("metadata contains trailing JSON")
		}
	}
	if closeErr := file.Close(); closeErr != nil {
		decodeErr = errors.Join(decodeErr, closeErr)
	}
	return decodeErr
}

func inspectMetadata(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("metadata is not a regular file")
	}
	if info.Mode().Perm()&0o022 != 0 {
		return nil, errors.New("metadata is group or world writable")
	}
	if err := validateUID(info); err != nil {
		return nil, err
	}
	return info, nil
}

func validateDirectory(path string, info os.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("registry: %q is not a real directory", path)
	}
	if info.Mode().Perm() != registryDirectoryMode {
		return fmt.Errorf("registry: directory %q must have mode 0700", path)
	}
	return validateUID(info)
}

func validateUID(info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("registry: file ownership is unavailable")
	}
	if int64(stat.Uid) != int64(os.Geteuid()) {
		return errors.New("registry: metadata is owned by another UID")
	}
	return nil
}

func validateInfrastructure(state InfrastructureState) error {
	if !state.StartedByE2E || state.StartedAt.IsZero() {
		return errors.New("registry: invalid infrastructure ownership")
	}
	return safeRunID(state.StartedByRun)
}

func safeRunID(runID string) error {
	if runID == "" || filepath.Base(runID) != runID || !runIDPattern.MatchString(runID) {
		return errors.New("registry: unsafe run ID")
	}
	return nil
}

func atomicWrite(path string, data []byte) (err error) {
	directory := filepath.Dir(path)
	temp, err := os.CreateTemp(directory, ".registry-*.tmp")
	if err != nil {
		return fmt.Errorf("registry: create temporary metadata: %w", err)
	}
	tempPath := temp.Name()
	defer func() {
		cleanupErr := os.Remove(tempPath)
		if cleanupErr != nil && !os.IsNotExist(cleanupErr) {
			err = errors.Join(err, fmt.Errorf("registry: remove temporary metadata: %w", cleanupErr))
		}
	}()
	if err := temp.Chmod(registryMetadataMode); err != nil {
		_ = temp.Close()
		return fmt.Errorf("registry: chmod temporary metadata: %w", err)
	}
	if err := writeAndClose(temp, data); err != nil {
		return fmt.Errorf("registry: write temporary metadata: %w", err)
	}
	if _, err := os.Lstat(directory); err != nil {
		return fmt.Errorf("registry: inspect destination directory: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("registry: rename metadata: %w", err)
	}
	return nil
}

func writeAndClose(file *os.File, data []byte) error {
	_, writeErr := file.Write(data)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	return errors.Join(writeErr, file.Close())
}

func (r Registry) runsPath() string              { return filepath.Join(r.Root, "runs") }
func (r Registry) leasePath(runID string) string { return filepath.Join(r.runsPath(), runID+".json") }
func (r Registry) infrastructurePath() string    { return filepath.Join(r.Root, "infrastructure.json") }
