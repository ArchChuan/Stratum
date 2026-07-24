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
	"sync/atomic"
	"syscall"
	"time"

	"github.com/byteBuilderX/stratum/tools/mcp-governor/internal/observe"
)

const defaultMaxMessageBytes = 8 << 20

const (
	defaultClientEOFDrain = 250 * time.Millisecond
	defaultShutdownGrace  = 750 * time.Millisecond
	defaultShutdownBudget = 2 * time.Second
)

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
	shutdownGrace   time.Duration
	shutdownBudget  time.Duration
}

const observationQueueSize = 128

// observationSink decouples protocol forwarding from best-effort observation I/O.
// A full queue drops observations and exposes the count at shutdown; it never
// causes the MCP byte stream to fail.
type observationSink struct {
	writer      observeWriter
	queue       chan observe.Event
	closed      chan struct{}
	dropped     atomic.Uint64
	writeErrors atomic.Uint64
	mu          sync.Mutex
	stateMu     sync.Mutex
	err         error
	closing     bool
	marked      bool
	cancel      context.CancelFunc
	cancelOnce  sync.Once
}

type observeWriter interface {
	Write(observe.Event) error
}

func newObservationSink(writer observeWriter) *observationSink {
	ctx, cancel := context.WithCancel(context.Background())
	s := &observationSink{writer: writer, queue: make(chan observe.Event, observationQueueSize), closed: make(chan struct{}), cancel: cancel}
	go s.run(ctx)
	return s
}

func (s *observationSink) run(ctx context.Context) {
	defer close(s.closed)
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-s.queue:
			if !ok {
				return
			}
			if err := writeObservation(ctx, s.writer, event); err != nil {
				s.writeErrors.Add(1)
				s.mu.Lock()
				if s.err == nil {
					s.err = err
				}
				s.mu.Unlock()
			}
		}
	}
}

type observationDegradedMarker interface {
	MarkDegraded(reason string, count uint64) error
}

func (s *observationSink) persistDegraded() error {
	marker, ok := s.writer.(observationDegradedMarker)
	if !ok {
		return nil
	}
	s.stateMu.Lock()
	if s.marked {
		s.stateMu.Unlock()
		return nil
	}
	s.marked = true
	s.stateMu.Unlock()
	var err error
	if dropped := s.dropped.Load(); dropped > 0 {
		err = errors.Join(err, marker.MarkDegraded("observation_sink_dropped", dropped))
	}
	if writeErrors := s.writeErrors.Load(); writeErrors > 0 {
		err = errors.Join(err, marker.MarkDegraded("observation_sink_write_error", writeErrors))
	}
	return err
}

type contextObserveWriter interface {
	WriteContext(context.Context, observe.Event) error
}

func writeObservation(ctx context.Context, writer observeWriter, event observe.Event) error {
	if contextual, ok := writer.(contextObserveWriter); ok {
		return contextual.WriteContext(ctx, event)
	}
	return writer.Write(event)
}

func (s *observationSink) enqueue(events []observe.Event) {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if s.closing {
		s.dropped.Add(uint64(len(events)))
		return
	}
	for _, event := range events {
		select {
		case s.queue <- event:
		default:
			s.dropped.Add(1)
		}
	}
}

func (s *observationSink) close(ctx context.Context) error {
	s.stateMu.Lock()
	if !s.closing {
		s.closing = true
		close(s.queue)
	}
	s.stateMu.Unlock()
	var drainErr error
	select {
	case <-s.closed:
	case <-ctx.Done():
		s.cancelOnce.Do(s.cancel)
		drainErr = fmt.Errorf("observation sink drain: %w", ctx.Err())
	}
	s.mu.Lock()
	err := s.err
	s.mu.Unlock()
	if dropped := s.dropped.Load(); dropped > 0 {
		err = errors.Join(err, fmt.Errorf("observation sink dropped %d events", dropped))
	}
	return errors.Join(drainErr, err, s.persistDegraded())
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
	childCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.Command(options.Command, options.Args...)
	if err := configureOwnedProcess(cmd); err != nil {
		return fmt.Errorf("stdio proxy: configure owned process: %w", err)
	}
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

	gate := newObservationGate()
	var sink *observationSink
	if options.Events != nil {
		sink = newObservationSink(options.Events)
	}
	writeEvents := func(events []observe.Event) error {
		if sink == nil {
			return nil
		}
		sink.enqueue(events)
		return nil
	}
	defer func() {
		if sink != nil {
			ctx, cancel := context.WithTimeout(context.Background(), defaultShutdownGrace)
			_ = sink.close(ctx)
			cancel()
		}
	}()
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

	waitResult := make(chan error, 1)
	go func() { waitResult <- waitChild(options, cmd) }()

	forwarded := make([]result, 0, 2)
	var waitErr error
	waitComplete := false
	shutdownInitiated := false
	exitCancellationExpected := false
	escalationComplete := false
	var clientStdinCloseErr, shutdownErr error
	var drainTimer, graceTimer, budgetTimer *time.Timer
	var drainC, graceC, budgetC <-chan time.Time
	grace := options.shutdownGrace
	if grace <= 0 {
		grace = defaultShutdownGrace
	}
	budget := options.shutdownBudget
	if budget <= 0 {
		budget = defaultShutdownBudget
	}
	stopTimer := func(timer *time.Timer) {
		if timer != nil {
			timer.Stop()
		}
	}
	defer func() {
		stopTimer(drainTimer)
		stopTimer(graceTimer)
		stopTimer(budgetTimer)
	}()
	initiateShutdown := func(expectedCancellation bool) {
		exitCancellationExpected = exitCancellationExpected || expectedCancellation
		if shutdownInitiated {
			return
		}
		shutdownInitiated = true
		cancel()
		_ = stdinCleanup.Close()
		_ = stdoutCleanup.Close()
		clientStdinCloseErr = interruptStdin(options)
		if err := signalOwnedProcessGroup(cmd, syscall.SIGTERM); err != nil {
			shutdownErr = errors.Join(shutdownErr, fmt.Errorf("stdio proxy: terminate process group: %w", err))
		}
		graceTimer = time.NewTimer(grace)
		graceC = graceTimer.C
		budgetTimer = time.NewTimer(budget)
		budgetC = budgetTimer.C
	}

	for len(forwarded) < 2 || !waitComplete || !escalationComplete {
		select {
		case item := <-results:
			forwarded = append(forwarded, item)
			if item.side == "client" && item.err == nil && !shutdownInitiated {
				_ = stdinCleanup.Close()
				if drainTimer == nil {
					drainTimer = time.NewTimer(defaultClientEOFDrain)
					drainC = drainTimer.C
				}
			} else {
				initiateShutdown(item.err != nil)
			}
		case waitErr = <-waitResult:
			waitComplete = true
			initiateShutdown(false)
			if !ownedProcessGroupExists(cmd) {
				escalationComplete = true
			}
		case <-ctx.Done():
			initiateShutdown(true)
		case <-drainC:
			drainC = nil
			initiateShutdown(true)
		case <-graceC:
			graceC = nil
			if err := signalOwnedProcessGroup(cmd, syscall.SIGKILL); err != nil {
				shutdownErr = errors.Join(shutdownErr, fmt.Errorf("stdio proxy: kill process group: %w", err))
			}
			escalationComplete = true
		case <-budgetC:
			budgetC = nil
			_ = signalOwnedProcessGroup(cmd, syscall.SIGKILL)
			budgetErr := errors.New("stdio proxy: shutdown budget exceeded")
			if sink != nil {
				drainCtx, cancelDrain := context.WithTimeout(context.Background(), defaultShutdownGrace)
				sinkErr := sink.close(drainCtx)
				cancelDrain()
				if sinkErr != nil {
					budgetErr = errors.Join(budgetErr, sinkErr)
				}
				sink = nil
			}
			return errors.Join(budgetErr, shutdownErr, clientStdinCloseErr, stdinCleanup.Err(), stdoutCleanup.Err())
		}
		if shutdownInitiated && waitComplete && len(forwarded) == 2 && !ownedProcessGroupExists(cmd) {
			escalationComplete = true
		}
	}
	cancel()

	var runErr error
	for _, item := range forwarded {
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
		_ = writeEvents(options.Tracker.Flush(outcome))
	}
	if sink != nil {
		drainCtx, cancelDrain := context.WithTimeout(context.Background(), defaultShutdownGrace)
		sinkErr := sink.close(drainCtx)
		cancelDrain()
		if sinkErr != nil {
			runErr = errors.Join(runErr, sinkErr)
		}
		sink = nil
	}
	return errors.Join(runErr, shutdownErr, clientStdinCloseErr, stdinCleanup.Err(), stdoutCleanup.Err())
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
		if errors.Is(err, os.ErrClosed) {
			return nil
		}
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
	if len(r.pending) == 0 && r.deferred != nil {
		err := r.deferred
		r.deferred = nil
		return 0, err
	}
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
