package skill

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/dop251/goja"
)

// ErrExecutionTimeout is returned when code runs past its deadline.
var ErrExecutionTimeout = errors.New("execution timeout")

// lookupPython checks whether python3 is available in PATH.
func lookupPython() (string, error) { return exec.LookPath("python3") }

// lookupPrlimit checks whether prlimit is available in PATH.
func lookupPrlimit() (string, error) { return exec.LookPath("prlimit") }

type CodeExecutorConfig struct {
	// MaxConcurrent is the global execution cap (semaphore channel size).
	MaxConcurrent int
	// PerTenantMax is the per-tenant sub-limit; 0 = no per-tenant limit.
	PerTenantMax int
	// DefaultTimeoutSec is the per-execution wall-clock timeout.
	DefaultTimeoutSec int
	// PythonMemoryMB is the virtual address space cap passed to prlimit --as.
	PythonMemoryMB int
}

// DefaultCodeExecutorConfig returns safe defaults.
func DefaultCodeExecutorConfig() CodeExecutorConfig {
	return CodeExecutorConfig{
		MaxConcurrent:     10,
		PerTenantMax:      3,
		DefaultTimeoutSec: 10,
		PythonMemoryMB:    128,
	}
}

// CodeExecutor runs Python/JS code in sandboxed subprocesses or a goja VM.
type CodeExecutor struct {
	cfg CodeExecutorConfig
	sem *Semaphore
}

// NewCodeExecutor creates a CodeExecutor with the given config.
func NewCodeExecutor(cfg CodeExecutorConfig) *CodeExecutor {
	return &CodeExecutor{
		cfg: cfg,
		sem: NewSemaphore(cfg.MaxConcurrent, cfg.PerTenantMax),
	}
}

// Execute runs code and returns the output value or an error.
// tenantID is used for per-tenant rate limiting.
func (e *CodeExecutor) Execute(ctx context.Context, lang, code, tenantID string, input map[string]interface{}) (interface{}, error) {
	if err := e.sem.Acquire(ctx, tenantID); err != nil {
		return nil, ErrConcurrencyLimit
	}
	defer e.sem.Release(tenantID)

	timeout := time.Duration(e.cfg.DefaultTimeoutSec) * time.Second
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	switch lang {
	case "python":
		return e.runPython(execCtx, code, input)
	case "javascript":
		return e.runJS(execCtx, code, input)
	default:
		return nil, fmt.Errorf("unsupported language: %s", lang)
	}
}

// runPython executes Python code via subprocess with prlimit resource caps.
// The user function must be named `process` and accept a dict argument.
// Wrapper script calls process(__input__) and prints the result as JSON.
func (e *CodeExecutor) runPython(ctx context.Context, code string, input map[string]interface{}) (interface{}, error) {
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("marshal input: %w", err)
	}

	wrapper := fmt.Sprintf(`
import json, sys
%s
__input__ = json.loads(%s)
try:
    result = process(__input__)
    print(json.dumps(result))
except Exception as e:
    print(json.dumps({"error": str(e)}))
`, code, strconv.Quote(string(inputJSON)))

	memBytes := strconv.Itoa(e.cfg.PythonMemoryMB * 1024 * 1024)

	// Write wrapper to a temp file so subprocess args contain no user code directly.
	f, err := os.CreateTemp("", "skill-*.py")
	if err != nil {
		return nil, fmt.Errorf("create temp script: %w", err)
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err := f.WriteString(wrapper); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("write temp script: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("close temp script: %w", err)
	}

	// prlimit wraps python3 with: virtual-address-space, CPU time (5s hard), open files (16)
	cmd := exec.CommandContext(ctx, "prlimit", //#nosec G204
		"--as="+memBytes,
		"--cpu=5",
		"--nofile=16",
		"python3", f.Name(),
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ErrExecutionTimeout
		}
		stderrStr := strings.TrimSpace(stderr.String())
		if stderrStr != "" {
			return nil, fmt.Errorf("python error: %s", stderrStr)
		}
		return nil, fmt.Errorf("python process failed: %w", err)
	}

	out := strings.TrimSpace(stdout.String())
	if out == "" {
		return nil, nil
	}

	var result json.RawMessage
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		return out, nil // return raw string if not JSON
	}
	return result, nil
}

// runJS executes JavaScript code in a goja VM with goroutine interrupt for timeout.
// The user function must be named `process` and accept an object argument.
func (e *CodeExecutor) runJS(ctx context.Context, code string, input map[string]interface{}) (interface{}, error) {
	vm := goja.New()

	// Disable potentially dangerous globals.
	for _, g := range []string{"require", "XMLHttpRequest", "fetch"} {
		if err := vm.Set(g, goja.Undefined()); err != nil {
			return nil, fmt.Errorf("disable global %s: %w", g, err)
		}
	}

	// Inject input as __input__ global.
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("marshal input: %w", err)
	}

	script := fmt.Sprintf(`
%s
var __input__ = %s;
var __result__ = process(__input__);
JSON.stringify(__result__);
`, code, string(inputJSON))

	type runResult struct {
		val interface{}
		err error
	}
	ch := make(chan runResult, 1)

	go func() {
		val, runErr := vm.RunString(script)
		if runErr != nil {
			ch <- runResult{err: runErr}
			return
		}
		// val is a JSON string from JSON.stringify
		raw, ok := val.Export().(string)
		if !ok {
			ch <- runResult{val: val.Export()}
			return
		}
		var result json.RawMessage
		if jsonErr := json.Unmarshal([]byte(raw), &result); jsonErr != nil {
			ch <- runResult{val: raw}
			return
		}
		ch <- runResult{val: result}
	}()

	select {
	case r := <-ch:
		return r.val, r.err
	case <-ctx.Done():
		vm.Interrupt(ErrExecutionTimeout)
		return nil, ErrExecutionTimeout
	}
}
