package textutil

import "testing"

func TestTruncateRunes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{name: "empty", in: "", max: 10, want: ""},
		{name: "zero limit", in: "abc", max: 0, want: ""},
		{name: "negative limit no panic", in: "abc", max: -3, want: ""},
		{name: "shorter than limit", in: "abc", max: 10, want: "abc"},
		{name: "exact length", in: "abc", max: 3, want: "abc"},
		{name: "cut at rune boundary", in: "很长的内容", max: 3, want: "很长的"},
		{name: "ascii cut", in: "abcdefgh", max: 5, want: "abcde"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := TruncateRunes(tc.in, tc.max); got != tc.want {
				t.Fatalf("TruncateRunes(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
			}
		})
	}
}
