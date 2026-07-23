package proxy

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"

	"github.com/byteBuilderX/stratum/tools/mcp-governor/internal/observe"
)

const defaultMaxMessageBytes = 8 << 20

var ErrMessageTooLarge = errors.New("message too large")
var ErrUninterruptibleStdin = errors.New("stdin cannot be interrupted")

type Options struct {
	Command string
	Args    []string
	Env     []string
	Dir     string
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
	Tracker *observe.Tracker
	Events  interface{ Write(observe.Event) error }
	// InterruptStdin must unblock any active Stdin.Read call before returning.
	InterruptStdin func() error

	maxMessageBytes int
	wrapChildStdin  func(io.WriteCloser) io.WriteCloser
	wrapChildStdout func(io.ReadCloser) io.ReadCloser
	waitChild       func(*exec.Cmd) error
}

func Run(ctx context.Context, options Options) error {
	if strings.TrimSpace(options.Command) == "" {
		return errors.New("stdio proxy: command is required")
	}
	if options.Stdin == nil {
		options.Stdin = strings.NewReader("")
	}
	if !interruptibleStdin(options) {
		return fmt.Errorf("stdio proxy: %w", ErrUninterruptibleStdin)
	}
	if options.Stdout == nil {
		options.Stdout = io.Discard
	}
	if options.Stderr == nil {
		options.Stderr = io.Discard
	}
	maxBytes := options.maxMessageBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxMessageBytes
	}
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(childCtx, options.Command, options.Args...)
	cmd.Env, cmd.Dir = options.Env, options.Dir
	childIn, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdio proxy: create stdin: %w", err)
	}
	childOut, err := cmd.StdoutPipe()
	if err != nil {
		return errors.Join(fmt.Errorf("stdio proxy: create stdout: %w", err), closeError("child stdin", childIn))
	}
	if options.wrapChildStdin != nil {
		childIn = options.wrapChildStdin(childIn)
	}
	if options.wrapChildStdout != nil {
		childOut = options.wrapChildStdout(childOut)
	}
	stdinCleanup := newCleanupCloser("child stdin", childIn)
	stdoutCleanup := newCleanupCloser("child stdout", childOut)
	cmd.Stderr = options.Stderr
	if err := cmd.Start(); err != nil {
		return errors.Join(fmt.Errorf("stdio proxy: start child: %w", err), stdinCleanup.Close(), stdoutCleanup.Close())
	}

	var eventMu sync.Mutex
	gate := newObservationGate()
	writeEvents := func(events []observe.Event) error {
		if options.Events == nil {
			return nil
		}
		eventMu.Lock()
		defer eventMu.Unlock()
		for _, event := range events {
			if err := options.Events.Write(event); err != nil {
				return fmt.Errorf("stdio proxy: write event: %w", err)
			}
		}
		return nil
	}
	type result struct {
		side string
		err  error
	}
	results := make(chan result, 2)
	go func() {
		results <- result{"client", forwardLines(childCtx, "client", options.Stdin, maxBytes, func(line []byte) error {
			if options.Tracker != nil {
				gate.beginClient()
				defer gate.finishClient()
			}
			if err := writeFull(childIn, line); err != nil {
				return err
			}
			if options.Tracker == nil {
				return nil
			}
			return writeEvents(options.Tracker.ClientMessage(line))
		})}
	}()
	go func() {
		results <- result{"child", forwardLines(childCtx, "server", childOut, maxBytes, func(line []byte) error {
			if err := writeFull(options.Stdout, line); err != nil {
				return err
			}
			if options.Tracker == nil {
				return nil
			}
			if err := gate.waitClient(childCtx); err != nil {
				return err
			}
			return writeEvents(options.Tracker.ServerMessage(line))
		})}
	}()

	first := <-results
	if first.side == "client" && first.err == nil {
		_ = stdinCleanup.Close()
	}
	shutdownInitiated := false
	exitCancellationExpected := false
	var clientStdinCloseErr error
	if first.err != nil || first.side == "child" {
		shutdownInitiated = true
		exitCancellationExpected = first.err != nil || ctx.Err() != nil
		cancel()
		_ = stdinCleanup.Close()
		_ = stdoutCleanup.Close()
		clientStdinCloseErr = interruptStdin(options)
	}
	second := <-results
	_ = stdinCleanup.Close()
	_ = stdoutCleanup.Close()
	waitErr := waitChild(options, cmd)
	cancel()

	var runErr error
	for _, item := range []result{first, second} {
		if err := forwardingError(item.err, shutdownInitiated || ctx.Err() != nil); err != nil {
			runErr = errors.Join(runErr, err)
		}
	}
	if ctx.Err() != nil {
		runErr = errors.Join(runErr, ctx.Err())
	}
	if err := childWaitError(waitErr, exitCancellationExpected || ctx.Err() != nil); err != nil {
		runErr = errors.Join(runErr, err)
	}
	if options.Tracker != nil {
		outcome := observe.OutcomeDisconnected
		if ctx.Err() != nil {
			outcome = observe.OutcomeCancelled
		}
		if err := writeEvents(options.Tracker.Flush(outcome)); err != nil {
			runErr = errors.Join(runErr, err)
		}
	}
	return errors.Join(runErr, clientStdinCloseErr, stdinCleanup.Err(), stdoutCleanup.Err())
}

type cleanupCloser struct {
	name string
	once sync.Once
	err  error
	io.Closer
}

func newCleanupCloser(name string, closer io.Closer) *cleanupCloser {
	return &cleanupCloser{name: name, Closer: closer}
}

func (c *cleanupCloser) Close() error {
	c.once.Do(func() { c.err = closeError(c.name, c.Closer) })
	return c.err
}

func (c *cleanupCloser) Err() error { return c.err }

func closeError(name string, closer io.Closer) error {
	if err := closer.Close(); err != nil {
		return fmt.Errorf("stdio proxy: close %s: %w", name, err)
	}
	return nil
}

func waitChild(options Options, cmd *exec.Cmd) error {
	if options.waitChild != nil {
		return options.waitChild(cmd)
	}
	return cmd.Wait()
}

func childWaitError(err error, cancelled bool) error {
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if cancelled && errors.As(err, &exitErr) && processWasSignalled(exitErr) {
		return nil
	}
	return fmt.Errorf("stdio proxy: child process: %w", err)
}

func processWasSignalled(err *exec.ExitError) bool {
	status, ok := err.ProcessState.Sys().(syscall.WaitStatus)
	return ok && status.Signaled()
}

func forwardingError(err error, cancelled bool) error {
	if err == nil || errors.Is(err, io.EOF) {
		return nil
	}
	if cancelled && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, io.ErrClosedPipe) || errors.Is(err, os.ErrClosed)) {
		return nil
	}
	return err
}

func interruptibleStdin(options Options) bool {
	if options.InterruptStdin != nil {
		return true
	}
	reader := options.Stdin
	if _, ok := reader.(*os.File); ok {
		return true
	}
	if _, ok := reader.(*io.PipeReader); ok {
		return true
	}
	switch reader.(type) {
	case *bytes.Buffer, *bytes.Reader, *strings.Reader:
		return true
	default:
		return false
	}
}

func interruptStdin(options Options) error {
	if options.InterruptStdin != nil {
		if err := options.InterruptStdin(); err != nil {
			return fmt.Errorf("stdio proxy: interrupt client stdin: %w", err)
		}
		return nil
	}
	closer, ok := options.Stdin.(io.Closer)
	if !ok {
		return nil
	}
	return closeError("client stdin", closer)
}

func forwardLines(
	ctx context.Context,
	side string,
	src io.Reader,
	maxBytes int,
	handleLine func([]byte) error,
) error {
	reader := bufio.NewReader(&lineLimitedReader{reader: src, remaining: maxBytes + 1, maximum: maxBytes + 1})
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, err := reader.ReadString('\n')
		if len(line) > maxBytes {
			return fmt.Errorf("%s message exceeds %d byte limit: %w", side, maxBytes, ErrMessageTooLarge)
		}
		if len(line) > 0 {
			if handleErr := handleLine([]byte(line)); handleErr != nil {
				return handleErr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

type lineLimitedReader struct {
	reader    io.Reader
	remaining int
	maximum   int
	pending   []byte
	deferred  error
}

func (r *lineLimitedReader) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, ErrMessageTooLarge
	}
	if len(p) > r.remaining {
		p = p[:r.remaining]
	}
	var n int
	var readErr error
	if len(r.pending) > 0 {
		n = copy(p, r.pending)
		r.pending = r.pending[n:]
		if len(r.pending) == 0 {
			readErr = r.deferred
		}
	} else {
		n, readErr = r.reader.Read(p)
	}
	if index := bytes.IndexByte(p[:n], '\n'); index >= 0 {
		lineBytes := index + 1
		if lineBytes < n {
			tail := append([]byte(nil), p[lineBytes:n]...)
			r.pending = append(tail, r.pending...)
		}
		if readErr != nil {
			r.deferred = readErr
		}
		n = lineBytes
		r.remaining = r.maximum
		return n, nil
	} else {
		r.remaining -= n
	}
	if len(r.pending) > 0 {
		return n, nil
	}
	if readErr != nil {
		r.deferred = nil
	}
	return n, readErr
}

type observationGate struct {
	mu       sync.Mutex
	inFlight bool
	done     chan struct{}
}

func newObservationGate() *observationGate { return &observationGate{} }

func (g *observationGate) beginClient() {
	g.mu.Lock()
	g.inFlight = true
	g.done = make(chan struct{})
	g.mu.Unlock()
}

func (g *observationGate) finishClient() {
	g.mu.Lock()
	if g.inFlight {
		g.inFlight = false
		close(g.done)
	}
	g.mu.Unlock()
}

func (g *observationGate) waitClient(ctx context.Context) error {
	g.mu.Lock()
	if !g.inFlight {
		g.mu.Unlock()
		return nil
	}
	done := g.done
	g.mu.Unlock()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func writeFull(dst io.Writer, data []byte) error {
	written, err := dst.Write(data)
	if err != nil {
		return err
	}
	if written != len(data) {
		return io.ErrShortWrite
	}
	return nil
}
