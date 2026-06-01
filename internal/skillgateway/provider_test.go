package skillgateway

import (
	"errors"
	"testing"

	"go.uber.org/zap"
)

func TestProviderRegistry_Register(t *testing.T) {
	reg := newProviderRegistry()
	p := newMockProvider("skill:a", "llm", nil, nil)
	if err := reg.Register(p); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestProviderRegistry_DuplicateRegister(t *testing.T) {
	reg := newProviderRegistry()
	p := newMockProvider("skill:a", "llm", nil, nil)
	_ = reg.Register(p)
	err := reg.Register(newMockProvider("skill:a", "llm", nil, nil))
	if err == nil {
		t.Fatal("expected error for duplicate skill_id")
	}
	var skillErr *SkillError
	if !errors.As(err, &skillErr) {
		t.Fatalf("expected *SkillError, got %T", err)
	}
	if skillErr.Code != ErrSkillAlreadyExists {
		t.Errorf("expected ErrSkillAlreadyExists, got %d", skillErr.Code)
	}
}

func TestProviderRegistry_Resolve(t *testing.T) {
	reg := newProviderRegistry()
	p := newMockProvider("skill:a", "llm", nil, nil)
	_ = reg.Register(p)

	resolved, ok := reg.Resolve("skill:a")
	if !ok {
		t.Fatal("expected to resolve skill:a")
	}
	if resolved != p {
		t.Error("resolved provider does not match registered provider")
	}
}

func TestProviderRegistry_ResolveNotFound(t *testing.T) {
	reg := newProviderRegistry()
	_, ok := reg.Resolve("skill:nonexistent")
	if ok {
		t.Error("expected not found")
	}
}

func TestProviderRegistry_TypeOf(t *testing.T) {
	reg := newProviderRegistry()
	_ = reg.Register(newMockProvider("skill:a", "mcp", nil, nil))
	if typ := reg.TypeOf("skill:a"); typ != "mcp" {
		t.Errorf("expected type 'mcp', got %s", typ)
	}
	if typ := reg.TypeOf("skill:unknown"); typ != "unknown" {
		t.Errorf("expected 'unknown', got %s", typ)
	}
}

func TestGateway_RegisterProvider(t *testing.T) {
	gw := NewDefaultGateway(nil, zap.NewNop(), nil)
	p := newMockProvider("skill:x", "llm", "output", nil)
	if err := gw.RegisterProvider(p); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGateway_Execute_Integration(t *testing.T) {
	gw := NewDefaultGateway(nil, zap.NewNop(), nil)
	p := newMockProvider("skill:x", "llm", "result", nil)
	_ = gw.RegisterProvider(p)

	resp, err := gw.Execute(testCtx(), SkillRequest{SkillID: "skill:x", Input: "hi"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Output != "result" {
		t.Errorf("expected 'result', got %v", resp.Output)
	}
}
