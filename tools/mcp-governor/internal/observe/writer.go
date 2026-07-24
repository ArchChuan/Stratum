package observe

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sync"
	"syscall"
	"time"
)

const (
	privateDirectoryMode = 0o700
	privateFileMode      = 0o600
	// DefaultMaxSegmentBytes bounds a single append-only event segment. A
	// segment is also rotated at the event's local calendar date boundary.
	// Keeping the default modest limits the amount of data a long-lived MCP
	// session can pin while retaining the existing metadata-only format.
	DefaultMaxSegmentBytes int64 = 4 << 20
)

type WriterOptions struct {
	MaxSegmentBytes int64
	RotateDaily     bool
}

type Writer struct {
	mu              sync.Mutex
	file            *os.File
	lifecycle       *os.File
	clientDirFD     int
	client          string
	sessionHash     string
	segment         int
	segmentDay      string
	maxSegmentBytes int64
	rotateDaily     bool
	closed          bool
}

func NewWriter(root, client, sessionHash string) (*Writer, error) {
	return NewWriterWithOptions(root, client, sessionHash, WriterOptions{})
}

func NewWriterWithOptions(root, client, sessionHash string, options WriterOptions) (*Writer, error) {
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

	maxSegmentBytes := options.MaxSegmentBytes
	if maxSegmentBytes <= 0 {
		maxSegmentBytes = DefaultMaxSegmentBytes
	}
	filename := sessionHash + ".jsonl"
	lockname := sessionHash + ".lock"
	lockFlags := syscall.O_CREAT | syscall.O_RDWR | syscall.O_CLOEXEC | syscall.O_NOFOLLOW
	lockFD, err := syscall.Openat(clientFD, lockname, lockFlags, privateFileMode)
	if err != nil {
		_ = syscall.Close(clientFD)
		return nil, fmt.Errorf("open session lifecycle lock: %w", err)
	}
	if err := validateDescriptor(lockFD, syscall.S_IFREG, privateFileMode); err != nil {
		_ = syscall.Close(lockFD)
		_ = syscall.Close(clientFD)
		return nil, fmt.Errorf("validate session lifecycle lock: %w", err)
	}
	if err := syscall.Flock(lockFD, syscall.LOCK_SH); err != nil {
		_ = syscall.Close(lockFD)
		_ = syscall.Close(clientFD)
		return nil, fmt.Errorf("lock session lifecycle: %w", err)
	}
	flags := syscall.O_CREAT | syscall.O_APPEND | syscall.O_WRONLY | syscall.O_CLOEXEC | syscall.O_NOFOLLOW
	fileFD, err := syscall.Openat(clientFD, filename, flags, privateFileMode)
	if err != nil {
		_ = syscall.Flock(lockFD, syscall.LOCK_UN)
		_ = syscall.Close(lockFD)
		_ = syscall.Close(clientFD)
		return nil, fmt.Errorf("open session event file: %w", err)
	}
	if err := validateDescriptor(fileFD, syscall.S_IFREG, privateFileMode); err != nil {
		_ = syscall.Close(fileFD)
		_ = syscall.Flock(lockFD, syscall.LOCK_UN)
		_ = syscall.Close(lockFD)
		_ = syscall.Close(clientFD)
		return nil, fmt.Errorf("validate session event file: %w", err)
	}
	return &Writer{
		file: os.NewFile(uintptr(fileFD), filename), lifecycle: os.NewFile(uintptr(lockFD), lockname),
		clientDirFD: clientFD, client: client, sessionHash: sessionHash,
		maxSegmentBytes: maxSegmentBytes, rotateDaily: options.RotateDaily,
	}, nil
}

func (w *Writer) Write(event Event) error {
	return w.WriteContext(context.Background(), event)
}

// WriteContext is the cancellation-aware form used by the proxy's
// asynchronous observation sink. It keeps an observation write from holding
// the writer mutex or event-file lock past the sink's shutdown budget.
func (w *Writer) WriteContext(ctx context.Context, event Event) error {
	if ctx == nil {
		ctx = context.Background()
	}
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

	if err := lockWriterMutex(ctx, &w.mu); err != nil {
		return fmt.Errorf("lock writer: %w", err)
	}
	defer w.mu.Unlock()
	if w.closed {
		return fmt.Errorf("writer is closed")
	}
	if err := flockContext(ctx, int(w.file.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock event file: %w", err)
	}
	if err := w.rotateIfNeeded(event, int64(len(record))); err != nil {
		_ = syscall.Flock(int(w.file.Fd()), syscall.LOCK_UN)
		return fmt.Errorf("rotate event segment: %w", err)
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

// rotateIfNeeded is called while the writer mutex and current file lock are
// held. The lifecycle lock is held for the complete session lifetime, so
// report/prune cannot unlink any segment while the writer is active.
func (w *Writer) rotateIfNeeded(event Event, nextBytes int64) error {
	day := event.At.Format("2006-01-02")
	stat, err := w.file.Stat()
	if err != nil {
		return err
	}
	if stat.Size() == 0 {
		w.segmentDay = day
		return nil
	}
	if (w.rotateDaily && w.segmentDay != "" && w.segmentDay != day) ||
		(w.maxSegmentBytes > 0 && stat.Size()+nextBytes > w.maxSegmentBytes) {
		if err := w.openNextSegment(day); err != nil {
			return err
		}
	}
	return nil
}

func (w *Writer) openNextSegment(day string) error {
	if err := w.file.Close(); err != nil {
		return fmt.Errorf("close current event segment: %w", err)
	}
	for {
		w.segment++
		name := fmt.Sprintf("%s.%06d.jsonl", w.sessionHash, w.segment)
		fd, err := syscall.Openat(w.clientDirFD, name,
			syscall.O_CREAT|syscall.O_EXCL|syscall.O_APPEND|syscall.O_WRONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW,
			privateFileMode)
		if err == syscall.EEXIST {
			continue
		}
		if err != nil {
			return fmt.Errorf("open next event segment: %w", err)
		}
		if err := validateDescriptor(fd, syscall.S_IFREG, privateFileMode); err != nil {
			_ = syscall.Close(fd)
			return fmt.Errorf("validate next event segment: %w", err)
		}
		if err := syscall.Flock(fd, syscall.LOCK_EX); err != nil {
			_ = syscall.Close(fd)
			return fmt.Errorf("lock next event segment: %w", err)
		}
		w.file = os.NewFile(uintptr(fd), name)
		w.segmentDay = day
		return nil
	}
}

func lockWriterMutex(ctx context.Context, mutex *sync.Mutex) error {
	for {
		if mutex.TryLock() {
			return nil
		}
		timer := time.NewTimer(time.Millisecond)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func flockContext(ctx context.Context, fd int, operation int) error {
	for {
		err := syscall.Flock(fd, operation|syscall.LOCK_NB)
		if err == nil {
			return nil
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			return err
		}
		timer := time.NewTimer(time.Millisecond)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
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
	if w.clientDirFD >= 0 {
		_ = syscall.Close(w.clientDirFD)
		w.clientDirFD = -1
	}
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
