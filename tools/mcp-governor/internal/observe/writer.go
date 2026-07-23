package observe

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"syscall"
)

const (
	privateDirectoryMode = 0o700
	privateFileMode      = 0o600
)

type Writer struct {
	mu          sync.Mutex
	file        *os.File
	lifecycle   *os.File
	client      string
	sessionHash string
	closed      bool
}

func NewWriter(root, client, sessionHash string) (*Writer, error) {
	if root == "" {
		return nil, fmt.Errorf("events root is required")
	}
	if !safePathComponent(client) {
		return nil, fmt.Errorf("unsafe client path component")
	}
	if !safePathComponent(sessionHash) {
		return nil, fmt.Errorf("unsafe session path component")
	}

	if err := syscall.Mkdir(root, privateDirectoryMode); err != nil && err != syscall.EEXIST {
		return nil, fmt.Errorf("create events root: %w", err)
	}
	rootFD, err := openPrivateDirectory(root)
	if err != nil {
		return nil, fmt.Errorf("open events root: %w", err)
	}
	defer syscall.Close(rootFD)

	if err := syscall.Mkdirat(rootFD, client, privateDirectoryMode); err != nil && err != syscall.EEXIST {
		return nil, fmt.Errorf("create client events directory: %w", err)
	}
	clientFD, err := openPrivateDirectoryAt(rootFD, client)
	if err != nil {
		return nil, fmt.Errorf("open client events directory: %w", err)
	}
	defer syscall.Close(clientFD)

	filename := sessionHash + ".jsonl"
	lockname := sessionHash + ".lock"
	lockFlags := syscall.O_CREAT | syscall.O_RDWR | syscall.O_CLOEXEC | syscall.O_NOFOLLOW
	lockFD, err := syscall.Openat(clientFD, lockname, lockFlags, privateFileMode)
	if err != nil {
		return nil, fmt.Errorf("open session lifecycle lock: %w", err)
	}
	if err := validateDescriptor(lockFD, syscall.S_IFREG, privateFileMode); err != nil {
		_ = syscall.Close(lockFD)
		return nil, fmt.Errorf("validate session lifecycle lock: %w", err)
	}
	if err := syscall.Flock(lockFD, syscall.LOCK_SH); err != nil {
		_ = syscall.Close(lockFD)
		return nil, fmt.Errorf("lock session lifecycle: %w", err)
	}
	flags := syscall.O_CREAT | syscall.O_APPEND | syscall.O_WRONLY | syscall.O_CLOEXEC | syscall.O_NOFOLLOW
	fileFD, err := syscall.Openat(clientFD, filename, flags, privateFileMode)
	if err != nil {
		_ = syscall.Flock(lockFD, syscall.LOCK_UN)
		_ = syscall.Close(lockFD)
		return nil, fmt.Errorf("open session event file: %w", err)
	}
	if err := validateDescriptor(fileFD, syscall.S_IFREG, privateFileMode); err != nil {
		_ = syscall.Close(fileFD)
		_ = syscall.Flock(lockFD, syscall.LOCK_UN)
		_ = syscall.Close(lockFD)
		return nil, fmt.Errorf("validate session event file: %w", err)
	}
	return &Writer{
		file: os.NewFile(uintptr(fileFD), filename), lifecycle: os.NewFile(uintptr(lockFD), lockname),
		client: client, sessionHash: sessionHash,
	}, nil
}

func (w *Writer) Write(event Event) error {
	if err := event.Validate(); err != nil {
		return fmt.Errorf("validate event: %w", err)
	}
	if event.Client != w.client || event.SessionHash != w.sessionHash {
		return fmt.Errorf("event client and session hash do not match writer")
	}
	record, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	record = append(record, '\n')

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return fmt.Errorf("writer is closed")
	}
	if err := syscall.Flock(int(w.file.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock event file: %w", err)
	}
	written, err := w.file.Write(record)
	if err != nil {
		_ = syscall.Flock(int(w.file.Fd()), syscall.LOCK_UN)
		return fmt.Errorf("append event: %w", err)
	}
	if written != len(record) {
		_ = syscall.Flock(int(w.file.Fd()), syscall.LOCK_UN)
		return fmt.Errorf("append event: %w", io.ErrShortWrite)
	}
	if err := syscall.Flock(int(w.file.Fd()), syscall.LOCK_UN); err != nil {
		return fmt.Errorf("unlock event file: %w", err)
	}
	return nil
}

func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	fileErr := w.file.Close()
	unlockErr := syscall.Flock(int(w.lifecycle.Fd()), syscall.LOCK_UN)
	lockCloseErr := w.lifecycle.Close()
	if fileErr != nil {
		return fmt.Errorf("close event file: %w", fileErr)
	}
	if unlockErr != nil {
		return fmt.Errorf("unlock session lifecycle: %w", unlockErr)
	}
	if lockCloseErr != nil {
		return fmt.Errorf("close session lifecycle lock: %w", lockCloseErr)
	}
	return nil
}

func openPrivateDirectory(path string) (int, error) {
	flags := syscall.O_RDONLY | syscall.O_DIRECTORY | syscall.O_CLOEXEC | syscall.O_NOFOLLOW
	fd, err := syscall.Open(path, flags, 0)
	if err != nil {
		return -1, err
	}
	if err := validateDescriptor(fd, syscall.S_IFDIR, privateDirectoryMode); err != nil {
		_ = syscall.Close(fd)
		return -1, err
	}
	return fd, nil
}

func openPrivateDirectoryAt(parentFD int, name string) (int, error) {
	flags := syscall.O_RDONLY | syscall.O_DIRECTORY | syscall.O_CLOEXEC | syscall.O_NOFOLLOW
	fd, err := syscall.Openat(parentFD, name, flags, 0)
	if err != nil {
		return -1, err
	}
	if err := validateDescriptor(fd, syscall.S_IFDIR, privateDirectoryMode); err != nil {
		_ = syscall.Close(fd)
		return -1, err
	}
	return fd, nil
}

func validateDescriptor(fd int, wantType uint32, wantMode uint32) error {
	var stat syscall.Stat_t
	if err := syscall.Fstat(fd, &stat); err != nil {
		return err
	}
	if stat.Mode&syscall.S_IFMT != wantType {
		return fmt.Errorf("unexpected file type")
	}
	const permissionMask = syscall.S_ISUID | syscall.S_ISGID | syscall.S_ISVTX | 0o777
	if stat.Mode&permissionMask != wantMode {
		return fmt.Errorf("mode is %#o, want %#o", stat.Mode&permissionMask, wantMode)
	}
	return nil
}

func safePathComponent(value string) bool {
	if value == "" || value == "." || value == ".." {
		return false
	}
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' ||
			char == '-' || char == '_' || char == '.' {
			continue
		}
		return false
	}
	return true
}
