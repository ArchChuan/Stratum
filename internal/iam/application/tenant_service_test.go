package application

import "testing"

func TestMaskAPIKey(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"", ""},
		{"abc", "a••••••••"}, // len<=6: show half (1 char) + 8 bullets
		{"sk-abc1234567", "sk-abc••••••••"},                           // len>6: show first 6 + 8 bullets
		{"sk-" + string(make([]byte, 30)), "sk-\x00\x00\x00••••••••"}, // long key
	}
	for _, tc := range cases {
		got := maskAPIKey(tc.input)
		if got != tc.want {
			t.Errorf("maskAPIKey(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
