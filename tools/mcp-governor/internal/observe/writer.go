package observe

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
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
	segment, err := discoverLatestSegment(clientFD, sessionHash)
	if err != nil {
		_ = syscall.Close(clientFD)
		return nil, fmt.Errorf("discover latest event segment: %w", err)
	}
	filename := eventSegmentName(sessionHash, segment)
	lockname := eventSegmentLockName(filename)
	lockFD, err := openSegmentLifecycle(clientFD, lockname)
	if err != nil {
		_ = syscall.Close(clientFD)
		return nil, fmt.Errorf("open event segment lifecycle lock: %w", err)
	}
	flags := syscall.O_CREAT | syscall.O_APPEND | syscall.O_WRONLY | syscall.O_CLOEXEC | syscall.O_NOFOLLOW
	fileFD, err := syscall.Openat(clientFD, filename, flags, privateFileMode)
	if err != nil {
		_ = syscall.Flock(int(lockFD.Fd()), syscall.LOCK_UN)
		_ = lockFD.Close()
		_ = syscall.Close(clientFD)
		return nil, fmt.Errorf("open session event file: %w", err)
	}
	if err := validateDescriptor(fileFD, syscall.S_IFREG, privateFileMode); err != nil {
		_ = syscall.Close(fileFD)
		_ = syscall.Flock(int(lockFD.Fd()), syscall.LOCK_UN)
		_ = lockFD.Close()
		_ = syscall.Close(clientFD)
		return nil, fmt.Errorf("validate session event file: %w", err)
	}
	segmentDay, err := readSegmentDay(clientFD, filename, maxSegmentBytes)
	if err != nil {
		_ = syscall.Close(fileFD)
		_ = syscall.Flock(int(lockFD.Fd()), syscall.LOCK_UN)
		_ = lockFD.Close()
		_ = syscall.Close(clientFD)
		return nil, fmt.Errorf("read latest event segment: %w", err)
	}
	return &Writer{
		file: os.NewFile(uintptr(fileFD), filename), lifecycle: lockFD,
		clientDirFD: clientFD, client: client, sessionHash: sessionHash,
		segment: segment, segmentDay: segmentDay,
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
	currentFile := w.file
	if err := flockContext(ctx, int(currentFile.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock event file: %w", err)
	}
	if err := w.rotateIfNeeded(ctx, event, int64(len(record))); err != nil {
		_ = syscall.Flock(int(currentFile.Fd()), syscall.LOCK_UN)
		return fmt.Errorf("rotate event segment: %w", err)
	}
	activeFile := w.file
	written, err := activeFile.Write(record)
	if err != nil {
		_ = syscall.Flock(int(activeFile.Fd()), syscall.LOCK_UN)
		return fmt.Errorf("append event: %w", err)
	}
	if written != len(record) {
		_ = syscall.Flock(int(activeFile.Fd()), syscall.LOCK_UN)
		return fmt.Errorf("append event: %w", io.ErrShortWrite)
	}
	if err := syscall.Flock(int(activeFile.Fd()), syscall.LOCK_UN); err != nil {
		return fmt.Errorf("unlock event file: %w", err)
	}
	return nil
}

// rotateIfNeeded is called while the writer mutex and current file lock are
// held. The lifecycle lock belongs to the current segment only, allowing
// report/prune to remove closed expired segments from a still-active session.
func (w *Writer) rotateIfNeeded(ctx context.Context, event Event, nextBytes int64) error {
	day := event.At.Format("2006-01-02")
	currentFile := w.file
	stat, err := currentFile.Stat()
	if err != nil {
		return err
	}
	if stat.Size() == 0 {
		w.segmentDay = day
		return nil
	}
	if (w.rotateDaily && w.segmentDay != "" && w.segmentDay != day) ||
		(w.maxSegmentBytes > 0 && stat.Size()+nextBytes > w.maxSegmentBytes) {
		if err := w.openNextSegment(ctx, day); err != nil {
			return err
		}
	}
	return nil
}

func (w *Writer) openNextSegment(ctx context.Context, day string) error {
	oldFile := w.file
	oldLifecycle := w.lifecycle
	for {
		w.segment++
		name := fmt.Sprintf("%s.%06d.jsonl", w.sessionHash, w.segment)
		lockName := eventSegmentLockName(name)
		lockFD, err := syscall.Openat(w.clientDirFD, lockName,
			syscall.O_CREAT|syscall.O_RDWR|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, privateFileMode)
		if err != nil {
			return fmt.Errorf("open next event segment lifecycle lock: %w", err)
		}
		if err := validateDescriptor(lockFD, syscall.S_IFREG, privateFileMode); err != nil {
			_ = syscall.Close(lockFD)
			return fmt.Errorf("validate next event segment lifecycle lock: %w", err)
		}
		if err := flockContext(ctx, lockFD, syscall.LOCK_SH); err != nil {
			_ = syscall.Close(lockFD)
			return fmt.Errorf("lock next event segment lifecycle: %w", err)
		}
		fd, err := syscall.Openat(w.clientDirFD, name,
			syscall.O_CREAT|syscall.O_EXCL|syscall.O_APPEND|syscall.O_WRONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW,
			privateFileMode)
		if err == syscall.EEXIST {
			_ = syscall.Flock(lockFD, syscall.LOCK_UN)
			_ = syscall.Close(lockFD)
			continue
		}
		if err != nil {
			_ = syscall.Flock(lockFD, syscall.LOCK_UN)
			_ = syscall.Close(lockFD)
			return fmt.Errorf("open next event segment: %w", err)
		}
		if err := validateDescriptor(fd, syscall.S_IFREG, privateFileMode); err != nil {
			_ = syscall.Close(fd)
			_ = syscall.Flock(lockFD, syscall.LOCK_UN)
			_ = syscall.Close(lockFD)
			return fmt.Errorf("validate next event segment: %w", err)
		}
		if err := flockContext(ctx, fd, syscall.LOCK_EX); err != nil {
			_ = syscall.Close(fd)
			_ = syscall.Flock(lockFD, syscall.LOCK_UN)
			_ = syscall.Close(lockFD)
			return fmt.Errorf("lock next event segment: %w", err)
		}
		newFile := os.NewFile(uintptr(fd), name)
		newLifecycle := os.NewFile(uintptr(lockFD), lockName)
		if err := oldFile.Close(); err != nil {
			_ = newFile.Close()
			_ = syscall.Flock(lockFD, syscall.LOCK_UN)
			_ = newLifecycle.Close()
			return fmt.Errorf("close current event segment: %w", err)
		}
		if err := syscall.Flock(int(oldLifecycle.Fd()), syscall.LOCK_UN); err != nil {
			_ = newFile.Close()
			_ = syscall.Flock(lockFD, syscall.LOCK_UN)
			_ = newLifecycle.Close()
			return fmt.Errorf("unlock current event segment lifecycle: %w", err)
		}
		if err := oldLifecycle.Close(); err != nil {
			_ = newFile.Close()
			_ = syscall.Flock(lockFD, syscall.LOCK_UN)
			_ = newLifecycle.Close()
			return fmt.Errorf("close current event segment lifecycle: %w", err)
		}
		w.file = newFile
		w.lifecycle = newLifecycle
		w.segmentDay = day
		return nil
	}
}

func eventSegmentName(session string, segment int) string {
	if segment == 0 {
		return session + ".jsonl"
	}
	return fmt.Sprintf("%s.%06d.jsonl", session, segment)
}

func eventSegmentLockName(name string) string {
	return strings.TrimSuffix(name, ".jsonl") + ".lock"
}

func parseEventSegmentName(name, session string) (int, bool) {
	if name == session+".jsonl" {
		return 0, true
	}
	prefix := session + "."
	suffix := ".jsonl"
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
		return 0, false
	}
	digits := strings.TrimSuffix(strings.TrimPrefix(name, prefix), suffix)
	if len(digits) != 6 {
		return 0, false
	}
	value := 0
	for _, char := range digits {
		if char < '0' || char > '9' {
			return 0, false
		}
		value = value*10 + int(char-'0')
	}
	if value == 0 {
		return 0, false
	}
	return value, true
}

func discoverLatestSegment(dirFD int, session string) (int, error) {
	dupFD, err := syscall.Openat(dirFD, ".", syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return 0, err
	}
	var names []string
	buffer := make([]byte, 16*1024)
	for {
		n, readErr := syscall.ReadDirent(dupFD, buffer)
		if readErr != nil {
			_ = syscall.Close(dupFD)
			return 0, readErr
		}
		if n == 0 {
			break
		}
		_, _, names = syscall.ParseDirent(buffer[:n], -1, names)
	}
	closeErr := syscall.Close(dupFD)
	if closeErr != nil {
		return 0, closeErr
	}
	latest := -1
	for _, name := range names {
		segment, ok := parseEventSegmentName(name, session)
		if !ok {
			continue
		}
		fd, openErr := syscall.Openat(dirFD, name, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
		if errors.Is(openErr, syscall.ENOENT) {
			continue
		}
		if openErr != nil {
			return 0, fmt.Errorf("open event segment %q: %w", name, openErr)
		}
		validateErr := validateDescriptor(fd, syscall.S_IFREG, privateFileMode)
		_ = syscall.Close(fd)
		if validateErr != nil {
			return 0, fmt.Errorf("validate event segment %q: %w", name, validateErr)
		}
		if segment > latest {
			latest = segment
		}
	}
	if latest < 0 {
		return 0, nil
	}
	return latest, nil
}

func openSegmentLifecycle(dirFD int, name string) (*os.File, error) {
	fd, err := syscall.Openat(dirFD, name,
		syscall.O_CREAT|syscall.O_RDWR|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, privateFileMode)
	if err != nil {
		return nil, err
	}
	if err := validateDescriptor(fd, syscall.S_IFREG, privateFileMode); err != nil {
		_ = syscall.Close(fd)
		return nil, err
	}
	if err := syscall.Flock(fd, syscall.LOCK_SH); err != nil {
		_ = syscall.Close(fd)
		return nil, err
	}
	return os.NewFile(uintptr(fd), name), nil
}

func readSegmentDay(dirFD int, name string, maxSegmentBytes int64) (string, error) {
	fd, err := syscall.Openat(dirFD, name, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return "", err
	}
	if err := validateDescriptor(fd, syscall.S_IFREG, privateFileMode); err != nil {
		_ = syscall.Close(fd)
		return "", err
	}
	if err := syscall.Flock(fd, syscall.LOCK_SH); err != nil {
		_ = syscall.Close(fd)
		return "", err
	}
	defer func() { _ = syscall.Flock(fd, syscall.LOCK_UN) }()
	file := os.NewFile(uintptr(fd), name)
	defer file.Close()
	limit := maxScannerBytes(maxSegmentBytes)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), limit)
	day := ""
	for line := 1; scanner.Scan(); line++ {
		data := scanner.Bytes()
		if len(strings.TrimSpace(string(data))) == 0 {
			return "", fmt.Errorf("line %d is empty", line)
		}
		var event Event
		if err := json.Unmarshal(data, &event); err != nil {
			return "", fmt.Errorf("decode line %d: %w", line, err)
		}
		if err := event.Validate(); err != nil {
			return "", fmt.Errorf("validate line %d: %w", line, err)
		}
		day = event.At.Format("2006-01-02")
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return day, nil
}

func maxScannerBytes(maxSegmentBytes int64) int {
	maxInt := int64(^uint(0) >> 1)
	if maxSegmentBytes <= 0 || maxSegmentBytes >= maxInt-1 {
		return int(maxInt)
	}
	return int(maxSegmentBytes + 1)
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
