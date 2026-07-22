package persistence

import (
	"reflect"
	"testing"
)

func TestBuildCandidateSafeDiffChangedFieldsOnly(t *testing.T) {
	diff := buildCandidateSafeDiff(
		map[string]any{"label": "old", "unchanged": true, "nested": map[string]any{"count": float64(1)}},
		map[string]any{"label": "new", "unchanged": true, "nested": map[string]any{"count": float64(2)}},
		true,
	)
	if !reflect.DeepEqual(diff.ChangedFields, []string{"label", "nested"}) {
		t.Fatalf("changed fields = %#v", diff.ChangedFields)
	}
	if diff.Changes["label"].Before != "old" || diff.Changes["label"].After != "new" {
		t.Fatalf("label change = %#v", diff.Changes["label"])
	}
	if _, ok := diff.Changes["unchanged"]; ok {
		t.Fatal("unchanged field returned")
	}
}

func TestBuildCandidateSafeDiffMissingParentAndSensitiveKeys(t *testing.T) {
	diff := buildCandidateSafeDiff(nil, map[string]any{
		"label": "new", "payload": "raw", "nested": map[string]any{"token": "secret"},
	}, false)
	if !diff.ParentMissing || !reflect.DeepEqual(diff.ChangedFields, []string{"label"}) {
		t.Fatalf("diff = %#v", diff)
	}
	if _, ok := diff.Changes["payload"]; ok {
		t.Fatal("sensitive payload returned")
	}
}

func TestBuildCandidateSafeDiffDropsCanonicalSensitiveKeysRecursively(t *testing.T) {
	diff := buildCandidateSafeDiff(nil, map[string]any{
		"label":    "safe",
		"auth":     map[string]any{"cookie": "secret"},
		"Session":  "secret",
		"database": map[string]any{"connectionString": "secret"},
		"tls":      map[string]any{"CERT": "secret", "private-key": "secret"},
	}, false)
	if !reflect.DeepEqual(diff.ChangedFields, []string{"label"}) {
		t.Fatalf("sensitive fields leaked into diff: %#v", diff)
	}
}
