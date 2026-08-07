package infrastructure

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestParseRetryAfterSeconds locks the integer-seconds branch: positive
// values are returned as-is (capped at maxRetryDelay), while non-positive or
// non-integer input falls through to HTTP-date parsing and yields 0.
func TestParseRetryAfterSeconds(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   time.Duration
	}{
		{name: "empty header", header: "", want: 0},
		{name: "single second", header: "1", want: time.Second},
		{name: "plain seconds", header: "5", want: 5 * time.Second},
		{name: "signed positive", header: "+5", want: 5 * time.Second},
		{name: "exactly at cap", header: "10", want: maxRetryDelay},
		{name: "over cap capped to max", header: "30", want: maxRetryDelay},
		{name: "huge seconds capped to max", header: "86400", want: maxRetryDelay},
		{name: "zero seconds", header: "0", want: 0},
		{name: "negative seconds", header: "-5", want: 0},
		{name: "non-numeric", header: "abc", want: 0},
		{name: "fractional", header: "10.5", want: 0},
		{name: "surrounding whitespace", header: " 30 ", want: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, parseRetryAfter(tc.header))
		})
	}
}

// TestParseRetryAfterHTTPDate locks the HTTP-date branch: an RFC1123 date
// within (0, maxRetryDelay] of now is honoured; past or far-future dates
// yield 0 (never sleep on a bogus date).
func TestParseRetryAfterHTTPDate(t *testing.T) {
	// http.TimeFormat is a "GMT" layout; format in UTC so the wall-clock
	// reading matches the label even when the host runs in a non-UTC timezone.
	formatGMT := func(delta time.Duration) string {
		return time.Now().Add(delta).UTC().Format(http.TimeFormat)
	}
	t.Run("future within cap", func(t *testing.T) {
		got := parseRetryAfter(formatGMT(5 * time.Second))
		require.Greater(t, got, 4*time.Second)
		require.LessOrEqual(t, got, 6*time.Second)
	})
	t.Run("past date yields zero", func(t *testing.T) {
		require.Equal(t, time.Duration(0), parseRetryAfter(formatGMT(-1*time.Hour)))
	})
	t.Run("far future beyond cap yields zero", func(t *testing.T) {
		require.Equal(t, time.Duration(0), parseRetryAfter(formatGMT(1*time.Hour)))
	})
}
