package testserver

import (
	"encoding/json"
	"testing"
)

// TestAnnotationsParsesFixtureHints 固定 annotations() 的解析行为：fixture DSL
// 的 hint map 必须落到 SDK typed 字段上。
func TestAnnotationsParsesFixtureHints(t *testing.T) {
	a := annotations(map[string]any{
		"readOnlyHint":    false,
		"destructiveHint": true,
		"idempotentHint":  true,
		"openWorldHint":   false,
		"title":           "x",
	})
	if a == nil {
		t.Fatal("expected non-nil annotations")
	}
	if a.ReadOnlyHint {
		t.Error("ReadOnlyHint = true, want false")
	}
	if a.DestructiveHint == nil || !*a.DestructiveHint {
		t.Error("DestructiveHint not true")
	}
	if !a.IdempotentHint {
		t.Error("IdempotentHint = false, want true")
	}
	if a.OpenWorldHint != nil && *a.OpenWorldHint {
		t.Error("OpenWorldHint = true, want false")
	}
	if a.Title != "x" {
		t.Errorf("Title = %q, want %q", a.Title, "x")
	}
}

func TestAnnotationsEmptyReturnsNil(t *testing.T) {
	if annotations(nil) != nil {
		t.Fatal("expected nil for empty map")
	}
	if annotations(map[string]any{}) != nil {
		t.Fatal("expected nil for empty map")
	}
}

// TestAnnotationsMarshalIntoSDKToolJSON 是旧缺陷的回归：annotations() 曾整体
// 丢弃 map，空 struct 经 SDK 序列化为 {"idempotentHint":false,"readOnlyHint":false}，
// 与 fixture 请求 destructiveHint:true 的意图相反。此测试覆盖「解析 → SDK
// MarshalJSON」完整链路。
func TestAnnotationsMarshalIntoSDKToolJSON(t *testing.T) {
	a := annotations(map[string]any{"destructiveHint": true})
	raw, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if v, ok := got["destructiveHint"].(bool); !ok || !v {
		t.Fatalf("destructiveHint = %v (ok=%v), want true; raw=%s", got["destructiveHint"], ok, raw)
	}
}
