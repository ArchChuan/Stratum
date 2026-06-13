package skill

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestCodeExecutor_JS_Basic(t *testing.T) {
	exec := NewCodeExecutor(DefaultCodeExecutorConfig())
	code := `function process(input) { return { result: input.x + input.y }; }`
	input := map[string]any{"x": 3, "y": 4}

	out, err := exec.Execute(context.Background(), "javascript", code, "t1", input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	raw, ok := out.(json.RawMessage)
	if !ok {
		t.Fatalf("want json.RawMessage, got %T: %v", out, out)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	result, _ := m["result"].(float64)
	if result != 7 {
		t.Errorf("want 7, got %v", m["result"])
	}
}

func TestCodeExecutor_JS_Timeout(t *testing.T) {
	cfg := DefaultCodeExecutorConfig()
	cfg.DefaultTimeoutSec = 1
	exec := NewCodeExecutor(cfg)
	code := `function process(input) { while(true){} }`

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := exec.Execute(ctx, "javascript", code, "t1", nil)
	if !errors.Is(err, ErrExecutionTimeout) {
		t.Fatalf("want ErrExecutionTimeout, got %v", err)
	}
}

func TestCodeExecutor_JS_UnsafeGlobals(t *testing.T) {
	exec := NewCodeExecutor(DefaultCodeExecutorConfig())
	// fetch should be undefined, process still runs
	code := `function process(input) { return typeof fetch === 'undefined' ? {ok:true} : {ok:false}; }`

	out, err := exec.Execute(context.Background(), "javascript", code, "t1", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	raw, ok := out.(json.RawMessage)
	if !ok {
		t.Fatalf("want json.RawMessage, got %T", out)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if m["ok"] != true {
		t.Errorf("fetch should be undefined, got ok=%v", m["ok"])
	}
}

func TestCodeExecutor_UnsupportedLang(t *testing.T) {
	exec := NewCodeExecutor(DefaultCodeExecutorConfig())
	_, err := exec.Execute(context.Background(), "ruby", "", "t1", nil)
	if err == nil {
		t.Fatal("want error for unsupported lang")
	}
}

func TestCodeExecutor_ConcurrencyLimit(t *testing.T) {
	cfg := DefaultCodeExecutorConfig()
	cfg.MaxConcurrent = 1
	cfg.PerTenantMax = 0
	ex := NewCodeExecutor(cfg)

	// Manually pre-fill the global semaphore slot.
	_ = ex.sem.Acquire(context.Background(), "")

	_, err := ex.Execute(context.Background(), "javascript", `function process(i){return i}`, "t1", nil)
	if !errors.Is(err, ErrConcurrencyLimit) {
		t.Fatalf("want ErrConcurrencyLimit, got %v", err)
	}
	ex.sem.Release("")
}

func TestCodeExecutor_Python_Basic(t *testing.T) {
	if _, err := lookupPython(); err != nil {
		t.Skip("python3 not available")
	}
	if _, err := lookupPrlimit(); err != nil {
		t.Skip("prlimit not available")
	}
	exec := NewCodeExecutor(DefaultCodeExecutorConfig())
	code := `def process(input): return {"sum": input["a"] + input["b"]}`
	input := map[string]any{"a": 10, "b": 5}

	out, err := exec.Execute(context.Background(), "python", code, "t1", input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	raw, ok := out.(json.RawMessage)
	if !ok {
		t.Fatalf("want json.RawMessage, got %T: %v", out, out)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	sum, _ := m["sum"].(float64)
	if sum != 15 {
		t.Errorf("want 15, got %v", m["sum"])
	}
}
