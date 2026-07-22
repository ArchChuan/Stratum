package application

import (
	"strings"
	"testing"
)

func TestCanonicalToolArgumentsDigestIsStable(t *testing.T) {
	left := map[string]any{
		"order":  map[string]any{"items": []any{"a", "b"}, "quantity": float64(2)},
		"reason": "duplicate",
	}
	right := map[string]any{
		"reason": "duplicate",
		"order":  map[string]any{"quantity": float64(2), "items": []any{"a", "b"}},
	}

	leftDigest, err := CanonicalToolArgumentsDigest(left)
	if err != nil {
		t.Fatal(err)
	}
	rightDigest, err := CanonicalToolArgumentsDigest(right)
	if err != nil {
		t.Fatal(err)
	}

	if leftDigest != rightDigest {
		t.Fatalf("equivalent arguments produced different digests: %q != %q", leftDigest, rightDigest)
	}
	if !strings.HasPrefix(leftDigest, "tool-arguments:v1:sha256:") {
		t.Fatalf("digest is not versioned: %q", leftDigest)
	}
}

func TestCanonicalToolArgumentsDigestChangesWithPayload(t *testing.T) {
	one, err := CanonicalToolArgumentsDigest(map[string]any{"quantity": float64(1)})
	if err != nil {
		t.Fatal(err)
	}
	two, err := CanonicalToolArgumentsDigest(map[string]any{"quantity": float64(2)})
	if err != nil {
		t.Fatal(err)
	}
	if one == two {
		t.Fatal("different arguments produced the same digest")
	}
}

func TestCanonicalToolArgumentsDigestRejectsNonJSONValues(t *testing.T) {
	if _, err := CanonicalToolArgumentsDigest(map[string]any{"invalid": make(chan int)}); err == nil {
		t.Fatal("non-JSON tool arguments were accepted")
	}
}
