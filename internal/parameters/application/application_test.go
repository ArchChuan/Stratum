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

func TestResolverPlatformTwoLevelFallback(t *testing.T) {
	registry := domain.NewParametersRegistry()
	store := newTestStore()
	// 平台级 key:存储值 → 定义默认两级兜底(全局参数契约)。
	store.values["memory.recall_top_k"] = json.RawMessage(`9`)
	store.values["agent.factcheck.top_k"] = json.RawMessage(`6`)
	resolver := NewResolver(registry, store)

	t.Run("declared wins over stored platform value", func(t *testing.T) {
		value, present, err := resolver.Resolve(context.Background(), "memory.recall_top_k", map[string]any{"memory.recall_top_k": 3})
		if err != nil {
			t.Fatal(err)
		}
		if !present || value != int64(3) {
			t.Fatalf("got (%v, %v), want (3, true)", value, present)
		}
	})

	t.Run("stored platform value applies when declared absent", func(t *testing.T) {
		value, present, err := resolver.Resolve(context.Background(), "memory.recall_top_k", nil)
		if err != nil {
			t.Fatal(err)
		}
		if !present || value != int64(9) {
			t.Fatalf("got (%v, %v), want (9, true)", value, present)
		}
	})

	t.Run("definition default applies when both tiers unset", func(t *testing.T) {
		value, present, err := resolver.Resolve(context.Background(), "memory.fact_injection_top_n", nil)
		if err != nil {
			t.Fatal(err)
		}
		if !present || value != int64(8) {
			t.Fatalf("got (%v, %v), want (8, true)", value, present)
		}
	})

	t.Run("declared zero is unset, falls to stored platform value", func(t *testing.T) {
		value, present, err := resolver.Resolve(context.Background(), "agent.factcheck.top_k", map[string]any{"agent.factcheck.top_k": 0})
		if err != nil {
			t.Fatal(err)
		}
		if !present || value != int64(6) {
			t.Fatalf("got (%v, %v), want (6, true): explicit 0 == unset", value, present)
		}
	})

	t.Run("zero default resolves to unset", func(t *testing.T) {
		value, present, err := resolver.Resolve(context.Background(), "evaluation.judge.temperature", nil)
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

func TestResolverResourceDeclaredOnly(t *testing.T) {
	registry := domain.NewParametersRegistry()
	store := newTestStore()
	// 平台存储里残留的资源级值必须被忽略:资源默认值已下线,资源配置只在资源层。
	store.values["agent.temperature"] = json.RawMessage(`0.9`)
	resolver := NewResolver(registry, store)

	t.Run("declared value resolves", func(t *testing.T) {
		value, present, err := resolver.Resolve(context.Background(), "agent.temperature", map[string]any{"agent.temperature": 0.3})
		if err != nil {
			t.Fatal(err)
		}
		if !present || value != 0.3 {
			t.Fatalf("got (%v, %v), want (0.3, true)", value, present)
		}
	})

	t.Run("absent declared resolves to unset despite stored platform value", func(t *testing.T) {
		value, present, err := resolver.Resolve(context.Background(), "agent.temperature", nil)
		if err != nil {
			t.Fatal(err)
		}
		if present || value != nil {
			t.Fatalf("got (%v, %v), want (nil, false): no platform default for resource keys", value, present)
		}
	})

	t.Run("definition default never applies", func(t *testing.T) {
		value, present, err := resolver.Resolve(context.Background(), "agent.max_iterations", nil)
		if err != nil {
			t.Fatal(err)
		}
		if present || value != nil {
			t.Fatalf("got (%v, %v), want (nil, false): definition default must not apply", value, present)
		}
	})

	t.Run("declared zero is unset", func(t *testing.T) {
		value, present, err := resolver.Resolve(context.Background(), "agent.temperature", map[string]any{"agent.temperature": 0})
		if err != nil {
			t.Fatal(err)
		}
		if present || value != nil {
			t.Fatalf("got (%v, %v), want (nil, false): explicit 0 == unset", value, present)
		}
	})
}

func TestResolverResolveForResourceDeclaredOnly(t *testing.T) {
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
	// 只返回声明值:平台存储的 temperature 0.9 不再进入资源生效值。
	if _, ok := effective["agent.temperature"]; ok {
		t.Fatal("platform-stored resource default must not be applied")
	}
	if got := effective["agent.max_tokens"]; got != int64(2048) {
		t.Fatalf("max_tokens = %v, want 2048", got)
	}
	if _, ok := effective["agent.max_context_tokens"]; ok {
		t.Fatal("0-default key must not appear in effective map")
	}
	if _, ok := effective["agent.max_iterations"]; ok {
		t.Fatal("definition default must not apply for resource keys")
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

	t.Run("rejects resource-scope value (no platform resource defaults)", func(t *testing.T) {
		err := svc.SetPlatformValues(
			context.Background(),
			map[string]any{"agent.temperature": 0.3},
			"admin-1",
		)
		var invalid *domain.ErrInvalidParameter
		if !domain.AsInvalidParameter(err, &invalid) {
			t.Fatalf("want ErrInvalidParameter, got %v", err)
		}
		if _, ok := store.values["agent.temperature"]; ok {
			t.Fatal("resource-scope key must not be stored as a platform default")
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
		err := svc.SetPlatformValues(context.Background(), map[string]any{"evaluation.optimizer.temperature": 5}, "admin-1")
		var invalid *domain.ErrInvalidParameter
		if !domain.AsInvalidParameter(err, &invalid) {
			t.Fatalf("want ErrInvalidParameter, got %v", err)
		}
	})

	t.Run("merge semantics: partial write keeps existing", func(t *testing.T) {
		_ = svc.SetPlatformValues(context.Background(), map[string]any{"evaluation.optimizer.temperature": 0.5}, "admin-1")
		_ = svc.SetPlatformValues(context.Background(), map[string]any{"evaluation.optimizer.max_tokens": 2048}, "admin-1")
		// 第二次只写一个 key,不能清掉第一个。
		if _, ok := store.values["evaluation.optimizer.temperature"]; !ok {
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
	if got := values["evaluation.optimizer.model"]; got != "" {
		t.Fatalf("default value = %v, want empty (model must resolve from catalog, no hardcoded fallback)", got)
	}
	if _, ok := values["agent.temperature"]; ok {
		t.Fatal("resource-scope key must not be returned by PlatformValues")
	}
}
