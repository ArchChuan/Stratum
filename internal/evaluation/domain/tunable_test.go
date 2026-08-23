package domain

import (
	"testing"

	"github.com/byteBuilderX/stratum/pkg/constants"
)

func TestMaxContextTokensTunable_RoundTrip(t *testing.T) {
	tun := maxContextTokensTunable{}
	if tun.Key() != "max_context_tokens" || tun.Category() != CatContextMemory {
		t.Fatalf("unexpected key/category: %q / %q", tun.Key(), tun.Category())
	}
	if tun.DefaultValue() != 0 {
		t.Fatalf("0 must mean auto: default = %v", tun.DefaultValue())
	}
	resource := map[string]any{}
	if v, err := tun.Read(resource); err != nil || v != 0 {
		t.Fatalf("missing key must read as 0: v=%v err=%v", v, err)
	}
	if err := tun.Write(resource, 16384.0); err != nil {
		t.Fatalf("write: %v", err)
	}
	if v, _ := tun.Read(resource); v != 16384.0 {
		t.Fatalf("round-trip = %v, want 16384", v)
	}
	for _, bad := range []any{-1.0, 40000.0, "big"} {
		if err := tun.Validate(bad); err == nil {
			t.Fatalf("Validate(%v) must reject out-of-range", bad)
		}
	}
	if err := tun.Validate(0.0); err != nil {
		t.Fatalf("Validate(0) must accept auto: %v", err)
	}
	if err := tun.Validate(float64(constants.TunableMaxContextTokensMax)); err != nil {
		t.Fatalf("Validate(max) must accept: %v", err)
	}
}

// TestRegistryContextTunables 验证新 tunable 经注册表完整
// 参与 ReadSnapshot / ApplyPatches 往返。
func TestRegistryContextTunables(t *testing.T) {
	reg := NewTunableRegistry()
	for _, key := range []string{"max_context_tokens"} {
		if reg.Get(key) == nil {
			t.Fatalf("registry missing %s", key)
		}
	}

	resource := map[string]any{
		"model_parameters": map[string]any{
			"temperature": 0.5,
		},
	}
	snapshot, err := reg.ReadSnapshot(resource)
	if err != nil {
		t.Fatalf("ReadSnapshot: %v", err)
	}
	if v, _ := snapshot["max_context_tokens"].(float64); v != 0 {
		t.Fatalf("unset max_context_tokens = %v, want 0", v)
	}

	changes, err := reg.ApplyPatches(resource, map[string]any{
		"max_context_tokens": 24576.0,
	})
	if err != nil {
		t.Fatalf("ApplyPatches: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("changes = %d, want 1", len(changes))
	}
	params, _ := resource["model_parameters"].(map[string]any)
	if params["max_context_tokens"] != 24576.0 {
		t.Fatalf("patches not persisted: %v", params)
	}
}
