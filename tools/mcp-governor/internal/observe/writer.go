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
	mu     sync.Mutex
	file   *os.File
	closed bool
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
	flags := syscall.O_CREAT | syscall.O_APPEND | syscall.O_WRONLY | syscall.O_CLOEXEC | syscall.O_NOFOLLOW
	fileFD, err := syscall.Openat(clientFD, filename, flags, privateFileMode)
	if err != nil {
		return nil, fmt.Errorf("open session event file: %w", err)
	}
	if err := validateDescriptor(fileFD, syscall.S_IFREG, privateFileMode); err != nil {
		_ = syscall.Close(fileFD)
		return nil, fmt.Errorf("validate session event file: %w", err)
	}
	return &Writer{file: os.NewFile(uintptr(fileFD), filename)}, nil
}

func (w *Writer) Write(event Event) error {
	if err := event.Validate(); err != nil {
		return fmt.Errorf("validate event: %w", err)
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
	written, err := w.file.Write(record)
	if err != nil {
		return fmt.Errorf("append event: %w", err)
	}
	if written != len(record) {
		return fmt.Errorf("append event: %w", io.ErrShortWrite)
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
	if err := w.file.Close(); err != nil {
		return fmt.Errorf("close event file: %w", err)
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
	if stat.Mode&0o777 != wantMode {
		return fmt.Errorf("mode is %#o, want %#o", stat.Mode&0o777, wantMode)
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
