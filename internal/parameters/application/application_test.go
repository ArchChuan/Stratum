package application

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/byteBuilderX/stratum/internal/parameters/domain"
	"github.com/byteBuilderX/stratum/internal/parameters/domain/port"
)

// memStore is an in-memory PlatformStore for resolver/service tests.
type memStore struct {
	values map[string]json.RawMessage
}

func (m *memStore) GetValue(_ context.Context, key string) (json.RawMessage, bool, error) {
	raw, ok := m.values[key]
	return raw, ok, nil
}

func (m *memStore) SetValue(_ context.Context, key string, value json.RawMessage, _ string) error {
	m.values[key] = value
	return nil
}

func (m *memStore) GetAll(_ context.Context) ([]port.PlatformValue, error) {
	var out []port.PlatformValue
	for k, v := range m.values {
		out = append(out, port.PlatformValue{Key: k, Value: v})
	}
	return out, nil
}

func newTestStore() *memStore {
	return &memStore{values: make(map[string]json.RawMessage)}
}

func TestResolverTwoLevelFallback(t *testing.T) {
	registry := domain.NewParametersRegistry()
	store := newTestStore()
	// 平台默认:temperature=0.9(定义默认 0.7)
	store.values["agent.temperature"] = json.RawMessage(`0.9`)
	resolver := NewResolver(registry, store)

	t.Run("declared wins over platform default", func(t *testing.T) {
		value, present, err := resolver.Resolve(context.Background(), "agent.temperature", map[string]any{"agent.temperature": 0.3})
		if err != nil {
			t.Fatal(err)
		}
		if !present || value != 0.3 {
			t.Fatalf("got (%v, %v), want (0.3, true)", value, present)
		}
	})

	t.Run("platform default applies when declared absent", func(t *testing.T) {
		value, present, err := resolver.Resolve(context.Background(), "agent.temperature", nil)
		if err != nil {
			t.Fatal(err)
		}
		if !present || value != 0.9 {
			t.Fatalf("got (%v, %v), want (0.9, true)", value, present)
		}
	})

	t.Run("definition default applies when both tiers unset", func(t *testing.T) {
		value, present, err := resolver.Resolve(context.Background(), "agent.max_iterations", nil)
		if err != nil {
			t.Fatal(err)
		}
		if !present || value != int64(10) {
			t.Fatalf("got (%v, %v), want (10, true)", value, present)
		}
	})

	t.Run("declared zero is unset, falls to platform default", func(t *testing.T) {
		value, present, err := resolver.Resolve(context.Background(), "agent.temperature", map[string]any{"agent.temperature": 0})
		if err != nil {
			t.Fatal(err)
		}
		if !present || value != 0.9 {
			t.Fatalf("got (%v, %v), want (0.9, true): explicit 0 == unset", value, present)
		}
	})

	t.Run("zero default resolves to unset", func(t *testing.T) {
		value, present, err := resolver.Resolve(context.Background(), "agent.max_tokens", nil)
		if err != nil {
			t.Fatal(err)
		}
		if present || value != nil {
			t.Fatalf("got (%v, %v), want (nil, false): 0 default == unset", value, present)
		}
	})

	t.Run("unknown key errors", func(t *testing.T) {
		if _, _, err := resolver.Resolve(context.Background(), "nope.nope", nil); err == nil {
			t.Fatal("unknown key must error")
		}
	})
}

func TestResolverResolveForResource(t *testing.T) {
	registry := domain.NewParametersRegistry()
	store := newTestStore()
	store.values["agent.temperature"] = json.RawMessage(`0.9`)
	resolver := NewResolver(registry, store)

	effective, err := resolver.ResolveForResource(context.Background(), map[string]any{
		"agent.max_tokens": 2048,
	})
	if err != nil {
		t.Fatal(err)
	}
	// temperature 从平台默认 0.9;max_tokens 声明 2048;其余 0 默认的不出现。
	if got := effective["agent.temperature"]; got != 0.9 {
		t.Fatalf("temperature = %v, want 0.9", got)
	}
	if got := effective["agent.max_tokens"]; got != int64(2048) {
		t.Fatalf("max_tokens = %v, want 2048", got)
	}
	if _, ok := effective["agent.max_context_tokens"]; ok {
		t.Fatal("0-default key must not appear in effective map")
	}
	if _, ok := effective["agent.max_iterations"]; !ok {
		t.Fatal("non-zero default key must appear in effective map")
	}
	if _, ok := effective["agent.bindings"]; ok {
		t.Fatal("bindings has no default and must stay absent")
	}
}

func TestServiceSetPlatformValues(t *testing.T) {
	registry := domain.NewParametersRegistry()
	store := newTestStore()
	svc := NewService(registry, store)

	t.Run("sets valid platform values", func(t *testing.T) {
		err := svc.SetPlatformValues(context.Background(), map[string]any{
			"evaluation.optimizer.temperature": 0.5,
			"trace.capture_parameters":         true,
		}, "admin-1")
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := store.values["evaluation.optimizer.temperature"]; !ok {
			t.Fatal("value not stored")
		}
	})

	t.Run("rejects resource-scope key (single attribution)", func(t *testing.T) {
		err := svc.SetPlatformValues(context.Background(), map[string]any{"agent.temperature": 0.3}, "admin-1")
		var invalid *domain.ErrInvalidParameter
		if !domain.AsInvalidParameter(err, &invalid) {
			t.Fatalf("want ErrInvalidParameter, got %v", err)
		}
	})

	t.Run("rejects unknown key", func(t *testing.T) {
		err := svc.SetPlatformValues(context.Background(), map[string]any{"bogus.key": 1}, "admin-1")
		var invalid *domain.ErrInvalidParameter
		if !domain.AsInvalidParameter(err, &invalid) {
			t.Fatalf("want ErrInvalidParameter, got %v", err)
		}
	})

	t.Run("rejects out-of-bounds value", func(t *testing.T) {
		err := svc.SetPlatformValues(context.Background(), map[string]any{"memory.recall_top_k": 999}, "admin-1")
		var invalid *domain.ErrInvalidParameter
		if !domain.AsInvalidParameter(err, &invalid) {
			t.Fatalf("want ErrInvalidParameter, got %v", err)
		}
	})

	t.Run("merge semantics: partial write keeps existing", func(t *testing.T) {
		_ = svc.SetPlatformValues(context.Background(), map[string]any{"memory.recall_top_k": 10}, "admin-1")
		_ = svc.SetPlatformValues(context.Background(), map[string]any{"memory.fact_injection_top_n": 8}, "admin-1")
		// 第二次只写一个 key,不能清掉第一个。
		if _, ok := store.values["memory.recall_top_k"]; !ok {
			t.Fatal("merge write must not wipe previously stored keys")
		}
	})

	t.Run("empty input is a no-op", func(t *testing.T) {
		if err := svc.SetPlatformValues(context.Background(), map[string]any{}, "admin-1"); err != nil {
			t.Fatal(err)
		}
	})
}

func TestServicePlatformValuesMergesDefaults(t *testing.T) {
	registry := domain.NewParametersRegistry()
	store := newTestStore()
	store.values["evaluation.optimizer.temperature"] = json.RawMessage(`0.5`)
	svc := NewService(registry, store)

	values, err := svc.PlatformValues(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := values["evaluation.optimizer.temperature"]; got != 0.5 {
		t.Fatalf("stored value = %v, want 0.5", got)
	}
	if got := values["evaluation.optimizer.model"]; got != "qwen-plus" {
		t.Fatalf("default value = %v, want qwen-plus", got)
	}
	if _, ok := values["agent.temperature"]; ok {
		t.Fatal("resource-scope key must not appear in platform values")
	}
}
