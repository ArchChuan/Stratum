package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/tools/mcp-governor/internal/observe"
)

func TestRunForwardsBytesAndEmitsMetadataOnlyEvent(t *testing.T) {
	server := buildFakeServer(t)
	secret := "unique-proxy-request-secret"
	request := fmt.Sprintf(
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"echo","arguments":{"secret":%q}}}`+"\n",
		secret,
	)
	var stdout, stderr bytes.Buffer
	events := &eventCollector{}
	err := Run(context.Background(), Options{
		Command: server, Args: []string{"rpc"}, Stdin: strings.NewReader(request),
		Stdout: &stdout, Stderr: &stderr, Tracker: newTracker(t), Events: events,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(stdout.String(), `"id":1`) {
		t.Fatalf("stdout = %q, want response", stdout.String())
	}
	if stderr.String() != "fake-server: rpc ready\n" {
		t.Fatalf("stderr = %q", stderr.String())
	}
	got := events.Events()
	if len(got) != 1 || got[0].Kind != observe.KindToolCall || got[0].Tool != "echo" ||
		got[0].Outcome != observe.OutcomeSuccess {
		t.Fatalf("events = %+v", got)
	}
	encoded, marshalErr := json.Marshal(got)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if bytes.Contains(encoded, []byte(secret)) {
		t.Fatalf("event leaked secret: %s", encoded)
	}
}

func TestRunHandlesPartialReadsMultipleLinesAndFinalLineWithoutNewline(t *testing.T) {
	server := buildFakeServer(t)
	want := "first\nsecond\nfinal"
	reader := &chunkReader{data: []byte(want), sizes: []int{1, 2, 5, 3, 1}}
	var stdout bytes.Buffer
	if err := Run(context.Background(), Options{
		Command: server, Args: []string{"echo"}, Stdin: io.NopCloser(reader), Stdout: &stdout, Stderr: io.Discard,
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
}

func TestRunAcceptsLargeLineAndRejectsLineOverLimit(t *testing.T) {
	server := buildFakeServer(t)
	t.Run("over 64 KiB", func(t *testing.T) {
		line := strings.Repeat("x", 70<<10) + "\n"
		var stdout bytes.Buffer
		if err := Run(context.Background(), Options{
			Command: server, Args: []string{"echo"}, Stdin: strings.NewReader(line),
			Stdout: &stdout, Stderr: io.Discard,
		}); err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if stdout.String() != line {
			t.Fatalf("forwarded %d bytes, want %d", stdout.Len(), len(line))
		}
	})

	t.Run("default maximum", func(t *testing.T) {
		line := strings.Repeat("z", defaultMaxMessageBytes-1) + "\n"
		var stdout bytes.Buffer
		if err := Run(context.Background(), Options{
			Command: server, Args: []string{"echo"}, Stdin: strings.NewReader(line),
			Stdout: &stdout, Stderr: io.Discard,
		}); err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if stdout.Len() != len(line) {
			t.Fatalf("forwarded %d bytes, want %d", stdout.Len(), len(line))
		}
	})

	t.Run("configured limit", func(t *testing.T) {
		err := Run(context.Background(), Options{
			Command: server, Args: []string{"echo"}, Stdin: strings.NewReader("123456789"),
			Stdout: io.Discard, Stderr: io.Discard, maxMessageBytes: 8,
		})
		if !errors.Is(err, ErrMessageTooLarge) {
			t.Fatalf("Run() error = %v, want ErrMessageTooLarge", err)
		}
		if got, want := err.Error(), "client message exceeds 8 byte limit: message too large"; !strings.Contains(got, want) {
			t.Fatalf("error = %q, want stable text containing %q", got, want)
		}
	})
}

func TestRunClientEOFAndChildExit(t *testing.T) {
	server := buildFakeServer(t)
	if err := Run(context.Background(), Options{
		Command: server, Args: []string{"exit"}, Stdin: strings.NewReader(""),
		Stdout: io.Discard, Stderr: io.Discard,
	}); err != nil {
		t.Fatalf("clean child exit error = %v", err)
	}

	err := Run(context.Background(), Options{
		Command: server, Args: []string{"error"}, Stdin: strings.NewReader("line\n"),
		Stdout: io.Discard, Stderr: io.Discard,
	})
	if err == nil || !strings.Contains(err.Error(), "child process") {
		t.Fatalf("failed child error = %v", err)
	}
}

func TestRunCancellationFlushesPendingCall(t *testing.T) {
	server := buildFakeServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	request := `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"sleep","arguments":{"secret":"hidden"}}}` + "\n"
	events := &eventCollector{}
	err := Run(ctx, Options{
		Command: server, Args: []string{"sleep"}, Stdin: strings.NewReader(request),
		Stdout: io.Discard, Stderr: io.Discard, Tracker: newTracker(t), Events: events,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run() error = %v, want deadline exceeded", err)
	}
	got := events.Events()
	if len(got) != 1 || got[0].Outcome != observe.OutcomeCancelled {
		t.Fatalf("events = %+v, want one cancelled event", got)
	}
}

func TestRunDisconnectFlushesPendingCall(t *testing.T) {
	server := buildFakeServer(t)
	request := `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"work"}}` + "\n"
	events := &eventCollector{}
	err := Run(context.Background(), Options{
		Command: server, Args: []string{"disconnect"}, Stdin: strings.NewReader(request),
		Stdout: io.Discard, Stderr: io.Discard, Tracker: newTracker(t), Events: events,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	got := events.Events()
	if len(got) != 1 || got[0].Outcome != observe.OutcomeDisconnected {
		t.Fatalf("events = %+v, want one disconnected event", got)
	}
}

func TestRunPropagatesEventWriterError(t *testing.T) {
	server := buildFakeServer(t)
	request := `{"id":1,"method":"tools/call","params":{"name":"echo"}}` + "\n"
	want := errors.New("event storage unavailable")
	err := Run(context.Background(), Options{
		Command: server, Args: []string{"echo"}, Stdin: strings.NewReader(request),
		Stdout: io.Discard, Stderr: io.Discard, Tracker: newTracker(t), Events: failingEvents{err: want},
	})
	if !errors.Is(err, want) {
		t.Fatalf("Run() error = %v, want event writer error", err)
	}
}

func TestRunPassesEnvironmentAndDirectory(t *testing.T) {
	server := buildFakeServer(t)
	dir := t.TempDir()
	var stdout bytes.Buffer
	err := Run(context.Background(), Options{
		Command: server, Args: []string{"env"}, Env: append(os.Environ(), "FAKE_VALUE=present"), Dir: dir,
		Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: io.Discard,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	want := "present\n" + dir + "\n"
	if stdout.String() != want {
		t.Fatalf("stdout = %q, want %q", stdout.String(), want)
	}
}

func TestRunRejectsBlockingStdinThatCannotBeInterrupted(t *testing.T) {
	server := buildFakeServer(t)
	reader := &blockingReader{}
	done := make(chan error, 1)
	go func() {
		done <- Run(context.Background(), Options{
			Command: server, Args: []string{"sleep"}, Stdin: reader,
			Stdout: io.Discard, Stderr: io.Discard,
		})
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrUninterruptibleStdin) {
			t.Fatalf("Run() error = %v, want ErrUninterruptibleStdin", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() hung with uninterruptible stdin")
	}
	if reader.reads.Load() != 0 {
		t.Fatalf("blocking reader Read() calls = %d, want 0", reader.reads.Load())
	}
}

func TestRunJoinsPrimaryAndCleanupErrors(t *testing.T) {
	server := buildFakeServer(t)
	stdinCloseErr := errors.New("close child stdin")
	stdoutCloseErr := errors.New("close child stdout")
	waitErr := errors.New("wait child cleanup")
	clientCloseErr := errors.New("close client stdin")
	err := Run(context.Background(), Options{
		Command: server, Args: []string{"sleep"},
		Stdin:  &errorReadCloser{ReadCloser: io.NopCloser(strings.NewReader("123456789")), err: clientCloseErr},
		Stdout: io.Discard, Stderr: io.Discard, maxMessageBytes: 8,
		wrapChildStdin: func(closer io.WriteCloser) io.WriteCloser {
			return &errorWriteCloser{WriteCloser: closer, err: stdinCloseErr}
		},
		wrapChildStdout: func(closer io.ReadCloser) io.ReadCloser {
			return &errorReadCloser{ReadCloser: closer, err: stdoutCloseErr}
		},
		waitChild: func(command *exec.Cmd) error {
			_ = command.Wait()
			return waitErr
		},
	})
	for _, want := range []error{ErrMessageTooLarge, clientCloseErr, stdinCloseErr, stdoutCloseErr, waitErr} {
		if !errors.Is(err, want) {
			t.Errorf("Run() error = %v, want joined %v", err, want)
		}
	}
}

type eventCollector struct {
	mu     sync.Mutex
	events []observe.Event
}

func (c *eventCollector) Write(event observe.Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, event)
	return nil
}

func (c *eventCollector) Events() []observe.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]observe.Event(nil), c.events...)
}

type failingEvents struct{ err error }

func (f failingEvents) Write(observe.Event) error { return f.err }

type blockingReader struct{ reads atomic.Int32 }

func (r *blockingReader) Read([]byte) (int, error) {
	r.reads.Add(1)
	select {}
}

type errorWriteCloser struct {
	io.WriteCloser
	err error
}

func (c *errorWriteCloser) Close() error { return errors.Join(c.WriteCloser.Close(), c.err) }

type errorReadCloser struct {
	io.ReadCloser
	err error
}

func (c *errorReadCloser) Close() error { return errors.Join(c.ReadCloser.Close(), c.err) }

type chunkReader struct {
	data  []byte
	sizes []int
	n     int
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	size := r.sizes[r.n%len(r.sizes)]
	r.n++
	if size > len(p) {
		size = len(p)
	}
	if size > len(r.data) {
		size = len(r.data)
	}
	copy(p, r.data[:size])
	r.data = r.data[size:]
	return size, nil
}

func newTracker(t *testing.T) *observe.Tracker {
	t.Helper()
	tracker, err := observe.NewTracker(time.Now, observe.Metadata{
		Client: "test-client", Service: "fake", SessionHash: "session",
	})
	if err != nil {
		t.Fatal(err)
	}
	return tracker
}

func buildFakeServer(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "testdata", "e2e", "fake-mcp-server.go"))
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "fake-mcp-server")
	command := exec.Command("/usr/local/go/bin/go", "build", "-o", output, root)
	if combined, buildErr := command.CombinedOutput(); buildErr != nil {
		t.Fatalf("build fake server: %v\n%s", buildErr, combined)
	}
	return output
}
