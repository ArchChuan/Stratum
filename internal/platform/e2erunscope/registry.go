package e2erunscope

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	registryDirectoryMode = 0o700
	registryMetadataMode  = 0o600
	registryTempAttempts  = 16
	registryLockName      = ".registry.lock"
)

var syncRegistryDirectory = func(directory *os.File) error { return directory.Sync() }
var releaseSnapshotHook = func() {}

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

type registryDirectories struct {
	root *os.File
	runs *os.File
}

func (r Registry) Prepare() error {
	directories, err := r.openDirectories(true)
	if err != nil {
		return err
	}
	directories.close()
	return nil
}

func (r Registry) Register(scope Scope) error {
	if err := Validate(scope); err != nil {
		return fmt.Errorf("registry: validate scope: %w", err)
	}
	if err := safeRunID(scope.RunID); err != nil {
		return err
	}
	directories, err := r.openDirectories(true)
	if err != nil {
		return err
	}
	defer directories.close()
	data, err := json.Marshal(scope)
	if err != nil {
		return fmt.Errorf("registry: encode lease: %w", err)
	}
	return withRegistryMutation(directories.root, func() error {
		leases, err := readAllLeasesAt(directories.runs)
		if err != nil {
			return err
		}
		if err := rejectPortConflicts(scope, leases); err != nil {
			return err
		}
		return atomicCreateAt(directories.root, directories.runs, scope.RunID+".json", data)
	})
}

func rejectPortConflicts(candidate Scope, leases []Scope) error {
	used := make(map[int]struct{}, len(leases)*6)
	for _, lease := range leases {
		for _, port := range scopePorts(lease.Ports) {
			used[port] = struct{}{}
		}
	}
	for _, port := range scopePorts(candidate.Ports) {
		if _, exists := used[port]; exists {
			return fmt.Errorf("registry: port conflict: %d", port)
		}
	}
	return nil
}

func scopePorts(ports Ports) []int {
	return []int{
		ports.Frontend, ports.Backend, ports.OAuth, ports.Fixture,
	}
}

func (r Registry) Read(runID string) (Scope, error) {
	if err := safeRunID(runID); err != nil {
		return Scope{}, err
	}
	directories, err := r.openDirectories(false)
	if err != nil {
		return Scope{}, err
	}
	defer directories.close()
	return readLeaseAt(directories.runs, runID+".json", runID)
}

func (r Registry) MarkInfrastructureOwned(runID string, now time.Time) error {
	if err := safeRunID(runID); err != nil {
		return err
	}
	directories, err := r.openDirectories(false)
	if err != nil {
		return err
	}
	defer directories.close()
	return withRegistryMutation(directories.root, func() error {
		if _, err := readLeaseAt(directories.runs, runID+".json", runID); err != nil {
			return fmt.Errorf("registry: read owner lease: %w", err)
		}
		state := InfrastructureState{StartedByE2E: true, StartedAt: now.UTC(), StartedByRun: runID}
		return writeInfrastructureAt(directories.root, state)
	})
}

func (r Registry) Release(runID string) (ReleaseResult, error) {
	if err := safeRunID(runID); err != nil {
		return ReleaseResult{}, err
	}
	directories, err := r.openDirectories(false)
	if err != nil {
		return ReleaseResult{}, err
	}
	defer directories.close()
	var result ReleaseResult
	err = withRegistryMutation(directories.root, func() error {
		var releaseErr error
		result, releaseErr = releaseAt(directories, runID)
		return releaseErr
	})
	return result, err
}

func releaseAt(directories *registryDirectories, runID string) (ReleaseResult, error) {
	leases, err := readAllLeasesAt(directories.runs)
	if err != nil {
		return ReleaseResult{}, err
	}
	releaseSnapshotHook()
	if err := requireLease(leases, runID); err != nil {
		return ReleaseResult{}, err
	}
	state, stateExists, err := readInfrastructureAt(directories.root)
	if err != nil {
		return ReleaseResult{}, err
	}
	if err := removeMetadataAt(directories.runs, runID+".json"); err != nil {
		return ReleaseResult{}, fmt.Errorf("registry: remove lease: %w", err)
	}
	if len(leases) != 1 {
		return ReleaseResult{}, nil
	}
	return lastReleaseResult(state, stateExists), nil
}

func (r Registry) ConfirmInfrastructureStopped(ownershipRunID string) error {
	if err := safeRunID(ownershipRunID); err != nil {
		return err
	}
	directories, err := r.openDirectories(false)
	if err != nil {
		return err
	}
	defer directories.close()
	return withRegistryMutation(directories.root, func() error {
		state, exists, err := readInfrastructureAt(directories.root)
		if err != nil {
			return err
		}
		if !exists {
			return errors.New("registry: infrastructure ownership not found")
		}
		if state.StartedByRun != ownershipRunID {
			return errors.New("registry: infrastructure ownership changed")
		}
		if err := removeMetadataAt(directories.root, "infrastructure.json"); err != nil {
			return fmt.Errorf("registry: remove infrastructure metadata: %w", err)
		}
		return nil
	})
}

func (r Registry) Stale(now time.Time, processAlive func(int, string) bool) ([]Scope, error) {
	if processAlive == nil {
		return nil, errors.New("registry: process liveness callback is required")
	}
	directories, err := r.openDirectories(false)
	if err != nil {
		return nil, err
	}
	defer directories.close()
	leases, err := readAllLeasesAt(directories.runs)
	if err != nil {
		return nil, err
	}
	state, stateExists, err := readInfrastructureAt(directories.root)
	if err != nil {
		return nil, err
	}
	stale := make([]Scope, 0, len(leases))
	for _, lease := range leases {
		if stateExists && lease.RunID == state.StartedByRun {
			continue
		}
		if now.After(lease.ExpiresAt) && !processAlive(lease.OwnerPID, lease.Repository) {
			stale = append(stale, lease)
		}
	}
	return stale, nil
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

func (r Registry) openDirectories(create bool) (*registryDirectories, error) {
	root, err := openRegistryRoot(r.Root, create)
	if err != nil {
		return nil, err
	}
	runs, err := openRegistrySubdirectory(root, "runs", create)
	if err != nil {
		_ = root.Close()
		return nil, err
	}
	return &registryDirectories{root: root, runs: runs}, nil
}

func (d *registryDirectories) close() {
	_ = d.runs.Close()
	_ = d.root.Close()
}

func withRegistryMutation(root *os.File, operation func() error) (err error) {
	lock, err := acquireRegistryMutationLock(root)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, releaseRegistryMutationLock(lock)) }()
	return operation()
}

func acquireRegistryMutationLock(root *os.File) (*os.File, error) {
	lock, created, err := openRegistryMutationLock(root)
	if err != nil {
		return nil, err
	}
	if created {
		if err := syncRegistryDirectory(root); err != nil {
			_ = lock.Close()
			return nil, fmt.Errorf("registry: sync mutation lock directory: %w", err)
		}
	}
	if err := lockRegistryFile(lock); err != nil {
		_ = lock.Close()
		return nil, err
	}
	return lock, nil
}

func openRegistryMutationLock(root *os.File) (*os.File, bool, error) {
	how := &unix.OpenHow{
		Flags:   unix.O_RDWR | unix.O_CREAT | unix.O_EXCL | unix.O_CLOEXEC | unix.O_NOFOLLOW,
		Mode:    registryMetadataMode,
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
	}
	fd, err := unix.Openat2(fileDescriptor(root), registryLockName, how)
	created := err == nil
	if errors.Is(err, unix.EEXIST) {
		how.Flags = unix.O_RDWR | unix.O_CLOEXEC | unix.O_NOFOLLOW
		how.Mode = 0
		fd, err = unix.Openat2(fileDescriptor(root), registryLockName, how)
	}
	if err != nil {
		return nil, false, fmt.Errorf("registry: open mutation lock: %w", err)
	}
	lock := fileFromDescriptor(fd, registryLockName)
	info, err := lock.Stat()
	if err == nil {
		err = validateMetadataFile(info)
	}
	if err != nil {
		_ = lock.Close()
		return nil, false, fmt.Errorf("registry: validate mutation lock: %w", err)
	}
	return lock, created, nil
}

func lockRegistryFile(lock *os.File) error {
	for {
		err := unix.Flock(fileDescriptor(lock), unix.LOCK_EX)
		if !errors.Is(err, unix.EINTR) {
			if err != nil {
				return fmt.Errorf("registry: lock mutation transaction: %w", err)
			}
			return nil
		}
	}
}

func releaseRegistryMutationLock(lock *os.File) error {
	unlockErr := unix.Flock(fileDescriptor(lock), unix.LOCK_UN)
	closeErr := lock.Close()
	if unlockErr != nil {
		unlockErr = fmt.Errorf("registry: unlock mutation transaction: %w", unlockErr)
	}
	if closeErr != nil {
		closeErr = fmt.Errorf("registry: close mutation lock: %w", closeErr)
	}
	return errors.Join(unlockErr, closeErr)
}

func openRegistryRoot(path string, create bool) (*os.File, error) {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) == "/" {
		return nil, errors.New("registry: root must be an absolute non-root path")
	}
	filesystemRoot, err := os.Open("/")
	if err != nil {
		return nil, fmt.Errorf("registry: open filesystem root: %w", err)
	}
	defer filesystemRoot.Close()
	relative := strings.TrimPrefix(filepath.Clean(path), "/")
	root, err := openDirectoryAt(filesystemRoot, relative, path)
	if err == nil || !create || !errors.Is(err, os.ErrNotExist) {
		return root, err
	}
	return createRegistryRoot(filesystemRoot, relative, path)
}

func createRegistryRoot(filesystemRoot *os.File, relative string, path string) (*os.File, error) {
	parentName, base := filepath.Split(relative)
	parentName = strings.TrimSuffix(parentName, string(filepath.Separator))
	parent, err := openUnvalidatedDirectoryAt(filesystemRoot, parentName, filepath.Dir(path))
	if err != nil {
		return nil, fmt.Errorf("registry: open root parent: %w", err)
	}
	defer parent.Close()
	err = unix.Mkdirat(fileDescriptor(parent), base, registryDirectoryMode)
	if err != nil && !errors.Is(err, unix.EEXIST) {
		return nil, fmt.Errorf("registry: create root: %w", err)
	}
	if err := syncRegistryDirectory(parent); err != nil {
		return nil, fmt.Errorf("registry: sync root parent: %w", err)
	}
	return openDirectoryAt(parent, base, path)
}

func openRegistrySubdirectory(root *os.File, name string, create bool) (*os.File, error) {
	directory, err := openDirectoryAt(root, name, name)
	if err == nil || !create || !errors.Is(err, os.ErrNotExist) {
		return directory, err
	}
	if err := unix.Mkdirat(fileDescriptor(root), name, registryDirectoryMode); err != nil && !errors.Is(err, unix.EEXIST) {
		return nil, fmt.Errorf("registry: create %s directory: %w", name, err)
	}
	if err := syncRegistryDirectory(root); err != nil {
		return nil, fmt.Errorf("registry: sync root directory: %w", err)
	}
	return openDirectoryAt(root, name, name)
}

func openDirectoryAt(parent *os.File, name string, displayPath string) (*os.File, error) {
	directory, err := openUnvalidatedDirectoryAt(parent, name, displayPath)
	if err != nil {
		return nil, err
	}
	info, err := directory.Stat()
	if err == nil {
		err = validateDirectory(displayPath, info)
	}
	if err != nil {
		_ = directory.Close()
		return nil, err
	}
	return directory, nil
}

func openUnvalidatedDirectoryAt(parent *os.File, name string, displayPath string) (*os.File, error) {
	how := &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
	}
	fd, err := unix.Openat2(fileDescriptor(parent), name, how)
	if err != nil {
		return nil, fmt.Errorf("registry: open directory %q: %w", displayPath, err)
	}
	return fileFromDescriptor(fd, displayPath), nil
}

func readAllLeasesAt(runs *os.File) ([]Scope, error) {
	fd, err := unix.Dup(fileDescriptor(runs))
	if err != nil {
		return nil, fmt.Errorf("registry: duplicate runs directory: %w", err)
	}
	directory := fileFromDescriptor(fd, "runs")
	entries, readErr := directory.ReadDir(-1)
	closeErr := directory.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, fmt.Errorf("registry: list leases: %w", err)
	}
	leases := make([]Scope, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if filepath.Ext(name) != ".json" {
			return nil, fmt.Errorf("registry: unexpected lease entry %q", name)
		}
		runID := strings.TrimSuffix(name, ".json")
		if err := safeRunID(runID); err != nil {
			return nil, fmt.Errorf("registry: invalid lease filename: %w", err)
		}
		lease, err := readLeaseAt(runs, name, runID)
		if err != nil {
			return nil, err
		}
		leases = append(leases, lease)
	}
	return leases, nil
}

func writeInfrastructureAt(root *os.File, state InfrastructureState) error {
	if err := validateInfrastructure(state); err != nil {
		return err
	}
	if _, _, err := readInfrastructureAt(root); err != nil {
		return err
	}
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("registry: encode infrastructure: %w", err)
	}
	return atomicWriteAt(root, "infrastructure.json", data)
}

func readInfrastructureAt(root *os.File) (InfrastructureState, bool, error) {
	var state InfrastructureState
	err := decodeMetadataAt(root, "infrastructure.json", &state)
	if errors.Is(err, os.ErrNotExist) {
		return InfrastructureState{}, false, nil
	}
	if err != nil {
		return InfrastructureState{}, false, fmt.Errorf("registry: read infrastructure: %w", err)
	}
	if err := validateInfrastructure(state); err != nil {
		return InfrastructureState{}, false, err
	}
	return state, true, nil
}

func readLeaseAt(runs *os.File, name string, runID string) (Scope, error) {
	var scope Scope
	if err := decodeMetadataAt(runs, name, &scope); err != nil {
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

func decodeMetadataAt(directory *os.File, name string, destination any) error {
	file, err := openMetadataAt(directory, name)
	if err != nil {
		return err
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
	return errors.Join(decodeErr, file.Close())
}

func openMetadataAt(directory *os.File, name string) (*os.File, error) {
	how := &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW,
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
	}
	fd, err := unix.Openat2(fileDescriptor(directory), name, how)
	if err != nil {
		return nil, fmt.Errorf("open metadata: %w", err)
	}
	file := fileFromDescriptor(fd, name)
	info, err := file.Stat()
	if err == nil {
		err = validateMetadataFile(info)
	}
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func validateMetadataFile(info os.FileInfo) error {
	if !info.Mode().IsRegular() {
		return errors.New("metadata is not a regular file")
	}
	if info.Mode().Perm() != registryMetadataMode {
		return fmt.Errorf("metadata mode %04o is not %04o", info.Mode().Perm(), registryMetadataMode)
	}
	return validateUID(info)
}

func validateDirectory(path string, info os.FileInfo) error {
	if !info.IsDir() {
		return fmt.Errorf("registry: %q is not a directory", path)
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

func atomicWriteAt(directory *os.File, name string, data []byte) (err error) {
	temp, tempName, err := createTemporaryMetadataAt(directory)
	if err != nil {
		return err
	}
	renamed := false
	defer func() { err = cleanupTemporaryMetadataAt(directory, tempName, renamed, err) }()
	if err := writeAndClose(temp, data); err != nil {
		return fmt.Errorf("registry: write temporary metadata: %w", err)
	}
	if err := unix.Renameat(fileDescriptor(directory), tempName, fileDescriptor(directory), name); err != nil {
		return fmt.Errorf("registry: rename metadata: %w", err)
	}
	renamed = true
	if err := syncRegistryDirectory(directory); err != nil {
		return fmt.Errorf("registry: sync metadata directory: %w", err)
	}
	return nil
}

func atomicCreateAt(source *os.File, destination *os.File, name string, data []byte) (err error) {
	temp, tempName, err := createTemporaryMetadataAt(source)
	if err != nil {
		return err
	}
	renamed := false
	defer func() { err = cleanupTemporaryMetadataAt(source, tempName, renamed, err) }()
	if err := writeAndClose(temp, data); err != nil {
		return fmt.Errorf("registry: write temporary metadata: %w", err)
	}
	if err := renameAtNoReplace(source, tempName, destination, name); errors.Is(err, os.ErrExist) {
		return fmt.Errorf("registry: lease already exists: %w", err)
	} else if err != nil {
		return fmt.Errorf("registry: rename lease: %w", err)
	}
	renamed = true
	sourceSyncErr := syncRegistryDirectory(source)
	destinationSyncErr := syncRegistryDirectory(destination)
	if err := errors.Join(sourceSyncErr, destinationSyncErr); err != nil {
		return fmt.Errorf("registry: sync lease namespace: %w", err)
	}
	return nil
}

func createTemporaryMetadataAt(directory *os.File) (*os.File, string, error) {
	for range registryTempAttempts {
		name, err := randomTemporaryName()
		if err != nil {
			return nil, "", fmt.Errorf("registry: generate temporary metadata name: %w", err)
		}
		fd, err := unix.Openat(
			fileDescriptor(directory), name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC,
			registryMetadataMode,
		)
		if err == nil {
			return fileFromDescriptor(fd, name), name, nil
		}
		if !errors.Is(err, unix.EEXIST) {
			return nil, "", fmt.Errorf("registry: create temporary metadata: %w", err)
		}
	}
	return nil, "", errors.New("registry: create temporary metadata: name attempts exhausted")
}

func randomTemporaryName() (string, error) {
	var suffix [16]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", err
	}
	return ".registry-" + hex.EncodeToString(suffix[:]) + ".tmp", nil
}

func removeMetadataAt(directory *os.File, name string) error {
	file, err := openMetadataAt(directory, name)
	if err != nil {
		return err
	}
	defer file.Close()
	quarantineName, err := unusedTemporaryNameAt(directory)
	if err != nil {
		return err
	}
	if err := renameAtNoReplace(directory, name, directory, quarantineName); err != nil {
		return fmt.Errorf("quarantine metadata before removal: %w", err)
	}
	quarantined, err := openMetadataAt(directory, quarantineName)
	if err != nil {
		return restoreQuarantinedMetadata(directory, quarantineName, name, err)
	}
	sameErr := requireSameFile(file, quarantined)
	closeErr := quarantined.Close()
	if err := errors.Join(sameErr, closeErr); err != nil {
		return restoreQuarantinedMetadata(directory, quarantineName, name, err)
	}
	if err := unix.Unlinkat(fileDescriptor(directory), quarantineName, 0); err != nil {
		return err
	}
	if err := syncRegistryDirectory(directory); err != nil {
		return fmt.Errorf("sync metadata directory: %w", err)
	}
	return nil
}

func unusedTemporaryNameAt(directory *os.File) (string, error) {
	for range registryTempAttempts {
		name, err := randomTemporaryName()
		if err != nil {
			return "", fmt.Errorf("registry: generate quarantine name: %w", err)
		}
		var stat unix.Stat_t
		err = unix.Fstatat(fileDescriptor(directory), name, &stat, unix.AT_SYMLINK_NOFOLLOW)
		if errors.Is(err, unix.ENOENT) {
			return name, nil
		}
		if err != nil {
			return "", fmt.Errorf("registry: inspect quarantine name: %w", err)
		}
	}
	return "", errors.New("registry: generate quarantine name: attempts exhausted")
}

func requireSameFile(first *os.File, second *os.File) error {
	firstInfo, firstErr := first.Stat()
	secondInfo, secondErr := second.Stat()
	if err := errors.Join(firstErr, secondErr); err != nil {
		return fmt.Errorf("stat metadata identity: %w", err)
	}
	firstStat, firstOK := firstInfo.Sys().(*syscall.Stat_t)
	secondStat, secondOK := secondInfo.Sys().(*syscall.Stat_t)
	if !firstOK || !secondOK || firstStat.Dev != secondStat.Dev || firstStat.Ino != secondStat.Ino {
		return errors.New("metadata changed before removal")
	}
	return nil
}

func restoreQuarantinedMetadata(directory *os.File, quarantineName string, name string, cause error) error {
	restoreErr := renameAtNoReplace(directory, quarantineName, directory, name)
	syncErr := syncRegistryDirectory(directory)
	if restoreErr != nil {
		restoreErr = fmt.Errorf("restore quarantined metadata: %w", restoreErr)
	}
	if syncErr != nil {
		syncErr = fmt.Errorf("sync restored metadata directory: %w", syncErr)
	}
	return errors.Join(cause, restoreErr, syncErr)
}

func renameAtNoReplace(oldDirectory *os.File, oldName string, newDirectory *os.File, newName string) error {
	return unix.Renameat2(
		fileDescriptor(oldDirectory), oldName, fileDescriptor(newDirectory), newName, unix.RENAME_NOREPLACE,
	)
}

func renameNoReplace(oldPath string, newPath string) error {
	return unix.Renameat2(unix.AT_FDCWD, oldPath, unix.AT_FDCWD, newPath, unix.RENAME_NOREPLACE)
}

func cleanupTemporaryMetadataAt(directory *os.File, name string, renamed bool, err error) error {
	cleanupErr := unix.Unlinkat(fileDescriptor(directory), name, 0)
	if cleanupErr == nil || (renamed && errors.Is(cleanupErr, unix.ENOENT)) {
		return err
	}
	return errors.Join(err, fmt.Errorf("registry: remove temporary metadata: %w", cleanupErr))
}

func writeAndClose(file *os.File, data []byte) error {
	_, writeErr := file.Write(data)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	return errors.Join(writeErr, file.Close())
}

func fileDescriptor(file *os.File) int {
	// #nosec G115 -- Unix file descriptors are non-negative int values exposed by os.File as uintptr.
	return int(file.Fd())
}

func fileFromDescriptor(fd int, name string) *os.File {
	// #nosec G115 -- successful Unix open/dup calls return non-negative int file descriptors.
	return os.NewFile(uintptr(fd), name)
}

func (r Registry) runsPath() string              { return filepath.Join(r.Root, "runs") }
func (r Registry) leasePath(runID string) string { return filepath.Join(r.runsPath(), runID+".json") }
