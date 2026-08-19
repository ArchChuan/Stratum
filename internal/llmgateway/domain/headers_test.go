package domain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCanonicalizeHeaderKey(t *testing.T) {
	cases := []struct{ in, want string }{
		{"x-api-key", "X-Api-Key"},
		{"  Authorization  ", "Authorization"},
		{"content-type", "Content-Type"},
		{"x-forwarded-for", "X-Forwarded-For"},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, CanonicalizeHeaderKey(tc.in), "input %q", tc.in)
	}
}

func TestValidateExtraHeaders(t *testing.T) {
	cases := []struct {
		name    string
		headers map[string]string
		want    string
	}{
		{name: "nil ok", headers: nil, want: ""},
		{name: "empty ok", headers: map[string]string{}, want: ""},
		{name: "custom header ok", headers: map[string]string{"X-Tenant": "a"}, want: ""},
		{name: "authorization blocked", headers: map[string]string{"Authorization": "Bearer x"}, want: "Authorization"},
		{name: "authorization case variant blocked", headers: map[string]string{"authorization": "x"}, want: "Authorization"},
		{name: "content-type blocked", headers: map[string]string{"Content-Type": "x"}, want: "Content-Type"},
		{name: "x-api-key blocked", headers: map[string]string{"x-api-key": "x"}, want: "X-Api-Key"},
		{name: "host blocked", headers: map[string]string{"Host": "x"}, want: "Host"},
		{name: "cookie blocked", headers: map[string]string{"Cookie": "x"}, want: "Cookie"},
		{name: "x-forwarded-for blocked", headers: map[string]string{"x-forwarded-for": "1.2.3.4"}, want: "X-Forwarded-For"},
		{name: "trailing space variant blocked", headers: map[string]string{"Content-Type ": "x"}, want: "Content-Type"},
		{name: "crlf in value rejected", headers: map[string]string{"X-Custom": "a\r\nX-Evil: b"}, want: "control"},
		{name: "empty key rejected", headers: map[string]string{"": "x"}, want: "empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateExtraHeaders(tc.headers)
			if tc.want == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tc.want)
		})
	}
}
