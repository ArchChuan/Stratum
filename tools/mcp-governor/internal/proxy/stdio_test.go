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
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/tools/mcp-governor/internal/observe"
)

var fakeServerPath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "mcp-governor-fake-server-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	root, err := filepath.Abs(filepath.Join("..", "..", "testdata", "e2e", "fake-mcp-server.go"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fakeServerPath = filepath.Join(dir, "fake-mcp-server")
	buildCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	command := exec.CommandContext(buildCtx, filepath.Join(runtime.GOROOT(), "bin", "go"), "build", "-o", fakeServerPath, root)
	combined, buildErr := command.CombinedOutput()
	cancel()
	if buildErr != nil {
		fmt.Fprintf(os.Stderr, "build fake server: %v\n%s", buildErr, combined)
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

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
		Command: server, Args: []string{"echo"}, Stdin: reader, Stdout: &stdout, Stderr: io.Discard,
		InterruptStdin: func() error { return nil },
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

func TestRunStopsReadingAfterMessageLimit(t *testing.T) {
	server := buildFakeServer(t)
	source := &countingByteReader{remaining: 64 << 20, value: 'x'}
	err := Run(context.Background(), Options{
		Command: server, Args: []string{"echo"}, Stdin: source,
		Stdout: io.Discard, Stderr: io.Discard, maxMessageBytes: 1024,
		InterruptStdin: func() error { return nil },
	})
	if !errors.Is(err, ErrMessageTooLarge) {
		t.Fatalf("Run() error = %v, want ErrMessageTooLarge", err)
	}
	if got, max := source.read.Load(), int64(1025); got > max {
		t.Fatalf("source consumed %d bytes, want at most %d", got, max)
	}
}

func TestForwardLinesPreservesReadAheadAndDeferredErrors(t *testing.T) {
	deferredErr := errors.New("deferred source failure")
	tests := []struct {
		name  string
		data  string
		err   error
		max   int
		lines []string
	}{
		{name: "delimiter and final tail with EOF", data: "first\nsecond", err: io.EOF, max: 64,
			lines: []string{"first\n", "second"}},
		{name: "newline at maximum with next line", data: "abcd\nnext\n", err: io.EOF, max: 5,
			lines: []string{"abcd\n", "next\n"}},
		{name: "several lines and final unterminated", data: "one\ntwo\nthree", err: io.EOF, max: 64,
			lines: []string{"one\n", "two\n", "three"}},
		{name: "tail before non EOF error", data: "first\nsecond", err: deferredErr, max: 64,
			lines: []string{"first\n", "second"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			err := forwardLines(context.Background(), "client", &singleReadReader{data: []byte(tt.data), err: tt.err},
				tt.max, func(line []byte) error {
					got = append(got, string(line))
					return nil
				})
			if strings.Join(got, "") != tt.data {
				t.Fatalf("forwarded = %q, want exact %q; lines=%q", strings.Join(got, ""), tt.data, got)
			}
			if len(got) != len(tt.lines) {
				t.Fatalf("lines = %q, want %q", got, tt.lines)
			}
			for i := range got {
				if got[i] != tt.lines[i] {
					t.Fatalf("line %d = %q, want %q", i, got[i], tt.lines[i])
				}
			}
			if tt.err == deferredErr && !errors.Is(err, deferredErr) {
				t.Fatalf("forwardLines() error = %v, want deferred error", err)
			}
			if tt.err == io.EOF && err != nil {
				t.Fatalf("forwardLines() error = %v, want nil", err)
			}
		})
	}
}

func TestForwardLinesRetainsTerminalErrorWhenDelimiterIsFinalByte(t *testing.T) {
	customErr := errors.New("terminal read failure")
	tests := []struct {
		name    string
		readErr error
		wantErr error
	}{
		{name: "EOF", readErr: io.EOF},
		{name: "non EOF error", readErr: customErr, wantErr: customErr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := &oneShotTerminalReader{data: []byte("line\n"), err: tt.readErr}
			var forwarded strings.Builder
			err := forwardLines(context.Background(), "client", source, 64, func(line []byte) error {
				_, writeErr := forwarded.Write(line)
				return writeErr
			})
			if forwarded.String() != "line\n" {
				t.Fatalf("forwarded = %q, want exact line", forwarded.String())
			}
			if source.reads.Load() != 1 {
				t.Fatalf("underlying reads = %d, want 1", source.reads.Load())
			}
			if tt.wantErr == nil && err != nil {
				t.Fatalf("forwardLines() error = %v, want nil", err)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("forwardLines() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
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
	ctx, cancel := context.WithCancel(context.Background())
	request := `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"sleep","arguments":{"secret":"hidden"}}}` + "\n"
	stdin := newRequestThenBlockingReader(request)
	events := &eventCollector{}
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			Command: server, Args: []string{"sleep"}, Stdin: stdin, InterruptStdin: stdin.Close,
			Stdout: io.Discard, Stderr: io.Discard, Tracker: newTracker(t), Events: events,
		})
	}()
	<-stdin.blocked
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run() error = %v, want context canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not return after cancellation")
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
	var stdout bytes.Buffer
	err := Run(context.Background(), Options{
		Command: server, Args: []string{"echo"}, Stdin: strings.NewReader(request),
		Stdout: &stdout, Stderr: io.Discard, Tracker: newTracker(t), Events: failingEvents{err: want},
	})
	if !errors.Is(err, want) {
		t.Fatalf("Run() error = %v, want event writer error", err)
	}
	if !strings.Contains(stdout.String(), `"id":1`) {
		t.Fatalf("stdout = %q, want forwarded response despite event writer error", stdout.String())
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

func TestRunRejectsGenericReadCloserThatDoesNotInterruptRead(t *testing.T) {
	server := buildFakeServer(t)
	reader := &nonInterruptingReadCloser{}
	err := Run(context.Background(), Options{
		Command: server, Args: []string{"sleep"}, Stdin: reader,
		Stdout: io.Discard, Stderr: io.Discard,
	})
	if !errors.Is(err, ErrUninterruptibleStdin) {
		t.Fatalf("Run() error = %v, want ErrUninterruptibleStdin", err)
	}
	if reader.reads.Load() != 0 || reader.closes.Load() != 0 {
		t.Fatalf("reader calls: reads=%d closes=%d, want zero", reader.reads.Load(), reader.closes.Load())
	}
}

func TestRunDetectsShortWritesBeforeObservation(t *testing.T) {
	server := buildFakeServer(t)
	t.Run("client to child", func(t *testing.T) {
		events := &eventCollector{}
		err := Run(context.Background(), Options{
			Command: server, Args: []string{"echo"},
			Stdin:  strings.NewReader(`{"id":1,"method":"tools/call","params":{"name":"echo"}}` + "\n"),
			Stdout: io.Discard, Stderr: io.Discard, Tracker: newTracker(t), Events: events,
			wrapChildStdin: func(closer io.WriteCloser) io.WriteCloser {
				return &shortWriteCloser{WriteCloser: closer}
			},
		})
		if !errors.Is(err, io.ErrShortWrite) {
			t.Fatalf("Run() error = %v, want io.ErrShortWrite", err)
		}
		if got := events.Events(); len(got) != 0 {
			t.Fatalf("events = %+v, want none for unforwarded request", got)
		}
	})

	t.Run("child to client", func(t *testing.T) {
		events := &eventCollector{}
		err := Run(context.Background(), Options{
			Command: server, Args: []string{"rpc"},
			Stdin:  strings.NewReader(`{"id":1,"method":"tools/call","params":{"name":"echo"}}` + "\n"),
			Stdout: shortWriter{}, Stderr: io.Discard, Tracker: newTracker(t), Events: events,
		})
		if !errors.Is(err, io.ErrShortWrite) {
			t.Fatalf("Run() error = %v, want io.ErrShortWrite", err)
		}
		got := events.Events()
		if len(got) != 1 || got[0].Outcome != observe.OutcomeDisconnected {
			t.Fatalf("events = %+v, want only pending-call disconnect", got)
		}
	})
}

func TestRunForwardsBothDirectionsWithoutWriteSerialization(t *testing.T) {
	server := buildFakeServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	barrier := newWriteBarrier(ctx, 2)
	request := `{"id":1,"method":"tools/call","params":{"name":"echo"}}` + "\n"
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			Command: server, Args: []string{"duplex"}, Stdin: strings.NewReader(request),
			Stdout: barrier.Writer(io.Discard), Stderr: io.Discard,
			wrapChildStdin: func(closer io.WriteCloser) io.WriteCloser {
				return &barrierWriteCloser{WriteCloser: closer, writer: barrier.Writer(closer)}
			},
		})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(2 * time.Second):
		cancel()
		<-done
		t.Fatal("opposite-direction writes were serialized")
	}
}

func TestRunJoinsPrimaryAndCleanupErrors(t *testing.T) {
	server := buildFakeServer(t)
	stdinCloseErr := errors.New("close child stdin")
	stdoutCloseErr := errors.New("close child stdout")
	waitErr := errors.New("wait child cleanup")
	clientCloseErr := errors.New("close client stdin")
	clientStdin := &errorReadCloser{ReadCloser: io.NopCloser(strings.NewReader("123456789")), err: clientCloseErr}
	err := Run(context.Background(), Options{
		Command: server, Args: []string{"sleep"},
		Stdin:  clientStdin,
		Stdout: io.Discard, Stderr: io.Discard, maxMessageBytes: 8,
		InterruptStdin: clientStdin.Close,
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

func TestRunPreservesSpontaneousChildExitWhenChildEOFWins(t *testing.T) {
	server := buildFakeServer(t)
	reader := newClosableBlockingReader()
	err := Run(context.Background(), Options{
		Command: server, Args: []string{"error"}, Stdin: reader,
		Stdout: io.Discard, Stderr: io.Discard,
		InterruptStdin: reader.Close,
	})
	if err == nil || !strings.Contains(err.Error(), "exit status 7") {
		t.Fatalf("Run() error = %v, want spontaneous exit status 7", err)
	}
	if !reader.Closed() || !reader.Unblocked() {
		t.Fatalf("reader closed=%v unblocked=%v", reader.Closed(), reader.Unblocked())
	}
}

func TestRunCancellationClosesAndUnblocksAcceptedStdin(t *testing.T) {
	server := buildFakeServer(t)
	reader := newClosableBlockingReader()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			Command: server, Args: []string{"sleep"}, Stdin: reader,
			Stdout: io.Discard, Stderr: io.Discard,
			InterruptStdin: reader.Close,
		})
	}()
	<-reader.started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run() error = %v, want context canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run() did not return promptly after cancellation")
	}
	if !reader.Closed() || !reader.Unblocked() {
		t.Fatalf("reader closed=%v unblocked=%v", reader.Closed(), reader.Unblocked())
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

func TestObservationSinkCloseIsIdempotentAndConcurrentEnqueueSafe(t *testing.T) {
	sink := newObservationSink(&eventCollector{})
	event := observe.Event{Version: observe.EventVersion, Kind: observe.KindSessionReady,
		At: time.Now(), Client: "codex", Service: "svc", SessionHash: "session"}
	started := make(chan struct{})
	go func() {
		close(started)
		for i := 0; i < observationQueueSize*4; i++ {
			sink.enqueue([]observe.Event{event})
		}
	}()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := sink.close(ctx); err != nil && !strings.Contains(err.Error(), "dropped") {
		t.Fatalf("first close error = %v", err)
	}
	if err := sink.close(ctx); err != nil && !strings.Contains(err.Error(), "dropped") {
		t.Fatalf("second close error = %v", err)
	}
}

type blockingEvents struct {
	started chan struct{}
	release chan struct{}
}

func (b *blockingEvents) Write(observe.Event) error {
	select {
	case <-b.started:
	default:
		close(b.started)
	}
	<-b.release
	return nil
}

func TestObservationSinkSlowWriterDoesNotBlockProtocolForwarding(t *testing.T) {
	server := buildFakeServer(t)
	writer := &blockingEvents{started: make(chan struct{}), release: make(chan struct{})}
	request := `{"id":1,"method":"tools/call","params":{"name":"echo"}}` + "\n"
	var stdout bytes.Buffer
	started := time.Now()
	err := Run(context.Background(), Options{
		Command: server, Args: []string{"rpc"}, Stdin: strings.NewReader(request),
		Stdout: &stdout, Stderr: io.Discard, Tracker: newTracker(t), Events: writer,
	})
	close(writer.release)
	if !strings.Contains(stdout.String(), `"id":1`) {
		t.Fatalf("stdout = %q, want forwarded response", stdout.String())
	}
	if err == nil || !strings.Contains(err.Error(), "observation sink drain") {
		t.Fatalf("Run() error = %v, want bounded observation drain error", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("Run() took %s with blocked observation writer", elapsed)
	}
}

func TestObservationSinkQueueFullIsObservable(t *testing.T) {
	writer := &blockingEvents{started: make(chan struct{}), release: make(chan struct{})}
	sink := newObservationSink(writer)
	event := observe.Event{Version: observe.EventVersion, Kind: observe.KindSessionReady,
		At: time.Now(), Client: "codex", Service: "svc", SessionHash: "session"}
	events := make([]observe.Event, observationQueueSize*2)
	for i := range events {
		events[i] = event
	}
	sink.enqueue(events)
	select {
	case <-writer.started:
	case <-time.After(time.Second):
		t.Fatal("observation writer did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	err := sink.close(ctx)
	cancel()
	if err == nil || !strings.Contains(err.Error(), "observation sink drain") {
		t.Fatalf("close() error = %v, want bounded drain error", err)
	}
	close(writer.release)
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := sink.close(ctx); err == nil || !strings.Contains(err.Error(), "dropped") {
		t.Fatalf("second close error = %v, want dropped-event visibility", err)
	}
}

type blockingReader struct{ reads atomic.Int32 }

func (r *blockingReader) Read([]byte) (int, error) {
	r.reads.Add(1)
	select {}
}

type nonInterruptingReadCloser struct {
	reads  atomic.Int32
	closes atomic.Int32
}

func (r *nonInterruptingReadCloser) Read([]byte) (int, error) {
	r.reads.Add(1)
	select {}
}

func (r *nonInterruptingReadCloser) Close() error {
	r.closes.Add(1)
	return nil
}

type countingByteReader struct {
	remaining int64
	value     byte
	read      atomic.Int64
}

type singleReadReader struct {
	data   []byte
	err    error
	offset int
}

type oneShotTerminalReader struct {
	data  []byte
	err   error
	reads atomic.Int32
}

func (r *oneShotTerminalReader) Read(p []byte) (int, error) {
	if r.reads.Add(1) != 1 {
		return 0, errors.New("unexpected second underlying read")
	}
	return copy(p, r.data), r.err
}

func (r *singleReadReader) Read(p []byte) (int, error) {
	if r.offset == len(r.data) {
		return 0, r.err
	}
	n := copy(p, r.data[r.offset:])
	r.offset += n
	if r.offset == len(r.data) {
		return n, r.err
	}
	return n, nil
}

type writeBarrier struct {
	ctx     context.Context
	mu      sync.Mutex
	needed  int
	entered int
	release chan struct{}
}

func newWriteBarrier(ctx context.Context, needed int) *writeBarrier {
	return &writeBarrier{ctx: ctx, needed: needed, release: make(chan struct{})}
}

func (b *writeBarrier) Writer(dst io.Writer) io.Writer {
	return barrierWriter{barrier: b, dst: dst}
}

type barrierWriter struct {
	barrier *writeBarrier
	dst     io.Writer
}

func (w barrierWriter) Write(p []byte) (int, error) {
	w.barrier.mu.Lock()
	w.barrier.entered++
	if w.barrier.entered == w.barrier.needed {
		close(w.barrier.release)
	}
	w.barrier.mu.Unlock()
	select {
	case <-w.barrier.release:
		return w.dst.Write(p)
	case <-w.barrier.ctx.Done():
		return 0, w.barrier.ctx.Err()
	}
}

type barrierWriteCloser struct {
	io.WriteCloser
	writer io.Writer
}

func (w *barrierWriteCloser) Write(p []byte) (int, error) { return w.writer.Write(p) }

func (r *countingByteReader) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	n := int64(len(p))
	if n > r.remaining {
		n = r.remaining
	}
	for i := range p[:n] {
		p[i] = r.value
	}
	r.remaining -= n
	r.read.Add(n)
	return int(n), nil
}

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }

type shortWriteCloser struct{ io.WriteCloser }

func (w *shortWriteCloser) Write(p []byte) (int, error) { return len(p) - 1, nil }

type closableBlockingReader struct {
	closed    chan struct{}
	started   chan struct{}
	unblocked chan struct{}
	startOnce sync.Once
	doneOnce  sync.Once
}

func newClosableBlockingReader() *closableBlockingReader {
	return &closableBlockingReader{
		closed: make(chan struct{}), started: make(chan struct{}), unblocked: make(chan struct{}),
	}
}

func (r *closableBlockingReader) Read([]byte) (int, error) {
	r.startOnce.Do(func() { close(r.started) })
	<-r.closed
	r.doneOnce.Do(func() { close(r.unblocked) })
	return 0, io.EOF
}

type requestThenBlockingReader struct {
	reader  *strings.Reader
	blocked chan struct{}
	closed  chan struct{}
	once    sync.Once
}

func newRequestThenBlockingReader(request string) *requestThenBlockingReader {
	return &requestThenBlockingReader{
		reader: strings.NewReader(request), blocked: make(chan struct{}), closed: make(chan struct{}),
	}
}

func (r *requestThenBlockingReader) Read(p []byte) (int, error) {
	if r.reader.Len() > 0 {
		return r.reader.Read(p)
	}
	r.once.Do(func() { close(r.blocked) })
	<-r.closed
	return 0, io.EOF
}

func (r *requestThenBlockingReader) Close() error {
	select {
	case <-r.closed:
	default:
		close(r.closed)
	}
	return nil
}

func (r *closableBlockingReader) Close() error {
	select {
	case <-r.closed:
	default:
		close(r.closed)
	}
	return nil
}

func (r *closableBlockingReader) Closed() bool {
	select {
	case <-r.closed:
		return true
	default:
		return false
	}
}

func (r *closableBlockingReader) Unblocked() bool {
	select {
	case <-r.unblocked:
		return true
	default:
		return false
	}
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
	return fakeServerPath
}
