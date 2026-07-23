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
	if !interruptibleStdin(options.Stdin) {
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
		results <- result{"client", forwardLines(childCtx, "client", options.Stdin, childIn, maxBytes, func(line []byte) error {
			if options.Tracker == nil {
				return nil
			}
			return writeEvents(options.Tracker.ClientMessage(line))
		})}
	}()
	go func() {
		results <- result{"child", forwardLines(childCtx, "server", childOut, options.Stdout, maxBytes, func(line []byte) error {
			if options.Tracker == nil {
				return nil
			}
			return writeEvents(options.Tracker.ServerMessage(line))
		})}
	}()

	first := <-results
	if first.side == "client" && first.err == nil {
		_ = stdinCleanup.Close()
	}
	cancelledByProxy := false
	var clientStdinCloseErr error
	if first.err != nil || first.side == "child" {
		cancelledByProxy = true
		cancel()
		_ = stdinCleanup.Close()
		_ = stdoutCleanup.Close()
		if closer, ok := options.Stdin.(io.Closer); ok {
			clientStdinCloseErr = closeError("client stdin", closer)
		}
	}
	second := <-results
	_ = stdinCleanup.Close()
	_ = stdoutCleanup.Close()
	waitErr := waitChild(options, cmd)
	cancel()

	var runErr error
	for _, item := range []result{first, second} {
		if err := forwardingError(item.err, cancelledByProxy || ctx.Err() != nil); err != nil {
			runErr = errors.Join(runErr, err)
		}
	}
	if ctx.Err() != nil {
		runErr = errors.Join(runErr, ctx.Err())
	}
	if err := childWaitError(waitErr, cancelledByProxy || ctx.Err() != nil); err != nil {
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
	if cancelled && errors.As(err, &exitErr) {
		return nil
	}
	return fmt.Errorf("stdio proxy: child process: %w", err)
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

func interruptibleStdin(reader io.Reader) bool {
	if _, ok := reader.(io.Closer); ok {
		return true
	}
	switch reader.(type) {
	case *bytes.Buffer, *bytes.Reader, *strings.Reader:
		return true
	default:
		return false
	}
}

func forwardLines(
	ctx context.Context,
	side string,
	src io.Reader,
	dst io.Writer,
	maxBytes int,
	observeLine func([]byte) error,
) error {
	reader := bufio.NewReader(src)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		line, err := reader.ReadString('\n')
		if len(line) > maxBytes {
			return fmt.Errorf("%s message exceeds %d byte limit: %w", side, maxBytes, ErrMessageTooLarge)
		}
		if len(line) > 0 {
			message := strings.TrimSuffix(line, "\n")
			if observeErr := observeLine([]byte(message)); observeErr != nil {
				return observeErr
			}
			if _, writeErr := io.WriteString(dst, line); writeErr != nil {
				return writeErr
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
