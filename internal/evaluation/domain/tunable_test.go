package domain

import (
	"reflect"
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

func TestCompactionRecentGroupsTunable_RoundTrip(t *testing.T) {
	tun := compactionRecentGroupsTunable{}
	if tun.Key() != "compaction_recent_groups" || tun.Category() != CatCompaction {
		t.Fatalf("unexpected key/category: %q / %q", tun.Key(), tun.Category())
	}
	resource := map[string]any{}
	if err := tun.Write(resource, 5.0); err != nil {
		t.Fatalf("write: %v", err)
	}
	if v, _ := tun.Read(resource); v != 5.0 {
		t.Fatalf("round-trip = %v, want 5", v)
	}
	for _, ok := range []float64{0, 2, 3, 5} {
		if err := tun.Validate(ok); err != nil {
			t.Fatalf("Validate(%v) must accept: %v", ok, err)
		}
	}
	for _, bad := range []any{4.0, 1.0, 6.0, "3"} {
		if err := tun.Validate(bad); err == nil {
			t.Fatalf("Validate(%v) must reject outside {0,2,3,5}", bad)
		}
	}
	space := tun.SearchSpace()
	if !reflect.DeepEqual(space.Discrete, []any{0, 2, 3, 5}) {
		t.Fatalf("search space = %v, want {0,2,3,5}", space.Discrete)
	}
}

func TestCompactionSafetyRatioTunable_RoundTrip(t *testing.T) {
	tun := compactionSafetyRatioTunable{}
	if tun.Key() != "compaction_safety_ratio" || tun.Category() != CatCompaction {
		t.Fatalf("unexpected key/category: %q / %q", tun.Key(), tun.Category())
	}
	resource := map[string]any{}
	if err := tun.Write(resource, 0.9); err != nil {
		t.Fatalf("write: %v", err)
	}
	if v, _ := tun.Read(resource); v != 0.9 {
		t.Fatalf("round-trip = %v, want 0.9", v)
	}
	if err := tun.Validate(0.0); err != nil {
		t.Fatalf("Validate(0) must mean default: %v", err)
	}
	if err := tun.Validate(constants.TunableSafetyRatioMax); err != nil {
		t.Fatalf("Validate(max) must accept: %v", err)
	}
	for _, bad := range []any{0.4, 1.0, -0.1, "0.8"} {
		if err := tun.Validate(bad); err == nil {
			t.Fatalf("Validate(%v) must reject out-of-range", bad)
		}
	}
	space := tun.SearchSpace()
	if space.Min != constants.TunableSafetyRatioMin || space.Max != constants.TunableSafetyRatioMax || space.Step != 0.05 {
		t.Fatalf("search space = %+v", space)
	}
}

// TestRegistryContextAndCompactionTunables 验证三个新 tunable 经注册表完整
// 参与 ReadSnapshot / ApplyPatches 往返。
func TestRegistryContextAndCompactionTunables(t *testing.T) {
	reg := NewTunableRegistry()
	for _, key := range []string{"max_context_tokens", "compaction_recent_groups", "compaction_safety_ratio"} {
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
		"max_context_tokens":       24576.0,
		"compaction_recent_groups": 5.0,
		"compaction_safety_ratio":  0.85,
	})
	if err != nil {
		t.Fatalf("ApplyPatches: %v", err)
	}
	if len(changes) != 3 {
		t.Fatalf("changes = %d, want 3", len(changes))
	}
	params, _ := resource["model_parameters"].(map[string]any)
	if params["max_context_tokens"] != 24576.0 ||
		params["compaction_recent_groups"] != 5.0 ||
		params["compaction_safety_ratio"] != 0.85 {
		t.Fatalf("patches not persisted: %v", params)
	}

	// 非法 patch 必须整体失败且不污染快照。
	if _, err := reg.ApplyPatches(resource, map[string]any{
		"compaction_recent_groups": 4.0,
	}); err == nil {
		t.Fatal("invalid compaction_recent_groups patch must be rejected")
	}
	if params["compaction_recent_groups"] != 5.0 {
		t.Fatalf("failed patch must not mutate resource: %v", params)
	}
}
