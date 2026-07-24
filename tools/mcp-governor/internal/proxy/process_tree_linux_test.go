package proxy

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	testShutdownGrace  = 100 * time.Millisecond
	testShutdownBudget = 2 * time.Second
)

func TestRunCancellationTerminatesOwnedProcessGroup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stdin := newClosableBlockingReader()
	done, pids := startStubbornTree(t, ctx, stdin, stdin.Close, io.Discard)
	unrelated := startUnrelatedProcess(t)

	waitTreeReady(t, pids)
	cancel()
	err := waitRun(t, done)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v, want context canceled", err)
	}
	assertTreeGone(t, pids)
	assertProcessAlive(t, unrelated.Process.Pid)
}

func TestRunClientEOFTerminatesStubbornOwnedProcessGroup(t *testing.T) {
	done, pids := startStubbornTree(t, context.Background(), strings.NewReader(""), nil, io.Discard)
	waitTreeReady(t, pids)
	if err := waitRun(t, done); err != nil {
		t.Fatalf("Run() error = %v, want clean client EOF shutdown", err)
	}
	assertTreeGone(t, pids)
}

func TestRunForwardingErrorTerminatesStubbornOwnedProcessGroup(t *testing.T) {
	stdin := newClosableBlockingReader()
	want := errors.New("client output unavailable")
	done, pids := startStubbornTree(t, context.Background(), stdin, stdin.Close, errorWriter{err: want})
	waitTreeReady(t, pids)
	err := waitRun(t, done)
	if !errors.Is(err, want) {
		t.Fatalf("Run() error = %v, want forwarding error", err)
	}
	assertTreeGone(t, pids)
}

func startStubbornTree(
	t *testing.T,
	ctx context.Context,
	stdin io.Reader,
	interrupt func() error,
	stdout io.Writer,
) (<-chan error, string) {
	t.Helper()
	pidDir := t.TempDir()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			Command: fakeServerPath, Args: []string{"stubborn-tree"},
			Env:   append(os.Environ(), "MCP_GOVERNOR_TREE_PID_DIR="+pidDir),
			Stdin: stdin, InterruptStdin: interrupt, Stdout: stdout, Stderr: io.Discard,
			shutdownGrace: testShutdownGrace, shutdownBudget: testShutdownBudget,
		})
	}()
	return done, pidDir
}

func waitTreeReady(t *testing.T, dir string) {
	t.Helper()
	deadline := time.Now().Add(testShutdownBudget)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(dir, "child")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "grandchild")); err == nil {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("stubborn process tree did not publish exact PIDs")
}

func waitRun(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(testShutdownBudget + time.Second):
		t.Fatal("Run() exceeded deterministic shutdown budget")
		return nil
	}
}

func assertTreeGone(t *testing.T, dir string) {
	t.Helper()
	for _, name := range []string{"child", "grandchild"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil {
			t.Fatal(err)
		}
		assertProcessGone(t, pid)
	}
}

func assertProcessGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(testShutdownBudget)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process %d still exists after shutdown budget", pid)
}

func startUnrelatedProcess(t *testing.T) *exec.Cmd {
	t.Helper()
	command := exec.Command("sleep", "30")
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_ = command.Wait()
	})
	return command
}

func assertProcessAlive(t *testing.T, pid int) {
	t.Helper()
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("unrelated process %d was affected: %v", pid, err)
	}
}

type errorWriter struct{ err error }

func (w errorWriter) Write([]byte) (int, error) { return 0, w.err }
