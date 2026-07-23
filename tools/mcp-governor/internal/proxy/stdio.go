package proxy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"

	"github.com/byteBuilderX/stratum/tools/mcp-governor/internal/observe"
)

const defaultMaxMessageBytes = 8 << 20

var ErrMessageTooLarge = errors.New("message too large")

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
}

func Run(ctx context.Context, options Options) error {
	if strings.TrimSpace(options.Command) == "" {
		return errors.New("stdio proxy: command is required")
	}
	if options.Stdin == nil {
		options.Stdin = strings.NewReader("")
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
		_ = childIn.Close()
		return fmt.Errorf("stdio proxy: create stdout: %w", err)
	}
	cmd.Stderr = options.Stderr
	if err := cmd.Start(); err != nil {
		_ = childIn.Close()
		return fmt.Errorf("stdio proxy: start child: %w", err)
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
		_ = childIn.Close()
	}
	if first.err != nil || first.side == "child" {
		cancel()
		_ = childIn.Close()
		_ = childOut.Close()
		if closer, ok := options.Stdin.(io.Closer); ok {
			_ = closer.Close()
		}
	}
	second := <-results
	_ = childIn.Close()
	_ = childOut.Close()
	waitErr := cmd.Wait()
	cancel()

	var primary error
	for _, item := range []result{first, second} {
		if item.err != nil && !errors.Is(item.err, io.EOF) && primary == nil {
			primary = item.err
		}
	}
	if primary == nil && waitErr != nil {
		if ctx.Err() != nil {
			primary = ctx.Err()
		} else {
			primary = fmt.Errorf("stdio proxy: child process: %w", waitErr)
		}
	}
	if options.Tracker != nil {
		outcome := observe.OutcomeDisconnected
		if ctx.Err() != nil {
			outcome = observe.OutcomeCancelled
		}
		if err := writeEvents(options.Tracker.Flush(outcome)); err != nil {
			primary = errors.Join(primary, err)
		}
	}
	return primary
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
