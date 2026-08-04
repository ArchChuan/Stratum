package domain

import (
	"testing"
	"time"
)

func TestCenterCursorRoundTrip(t *testing.T) {
	// UTC 化往返必须保持一致。
	createdAt := time.Date(2026, 7, 1, 12, 30, 0, 0, time.FixedZone("CST", 8*3600))
	encoded := EncodeCenterCursor(createdAt, "res-42")
	decoded, err := DecodeCenterCursor(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !decoded.CreatedAt.Equal(createdAt.UTC()) {
		t.Errorf("createdAt mismatch: %v vs %v", decoded.CreatedAt, createdAt.UTC())
	}
	if decoded.ID != "res-42" {
		t.Errorf("id mismatch: %q", decoded.ID)
	}
}

func TestEncodeCenterCursorDeterministic(t *testing.T) {
	at := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	if first := EncodeCenterCursor(at, "x"); first != EncodeCenterCursor(at, "x") {
		t.Error("encoding must be deterministic")
	}
}

func TestDecodeCenterCursorRejectsInvalid(t *testing.T) {
	cases := []struct {
		name  string
		value string
	}{
		{"empty string", ""},
		{"not base64", "!!not-base64!!"},
		{"valid base64 invalid json", func() string {
			// "not json" 经 RawURLEncoding 编码
			const s = "bm90IGpzb24"
			return s
		}()},
		{"zero createdAt", func() string {
			return EncodeCenterCursor(time.Time{}, "res-1")
		}()},
		{"empty id", func() string {
			return EncodeCenterCursor(time.Now(), "   ")
		}()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := DecodeCenterCursor(tc.value); err != ErrInvalidCenterQuery {
				t.Errorf("expected ErrInvalidCenterQuery, got %v", err)
			}
		})
	}
}
