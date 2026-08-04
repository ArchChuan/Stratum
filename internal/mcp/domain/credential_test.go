package domain

import "testing"

func TestIsSensitiveConfigKey(t *testing.T) {
	cases := []struct {
		name string
		key  string
		want bool
	}{
		{"exact marker lower", "token", true},
		{"mixed case", "Authorization", true},
		{"underscore separators", "API_KEY", true},
		{"dash separators", "api-key", true},
		{"dot separators", "db.secret", true},
		{"space separators", "access token", true},
		{"marker embedded", "x_token_y", true},
		{"password", "db_password", true},
		{"credential", "user_credential", true},
		{"apikey no separator", "apikey", true},
		{"empty key", "", false},
		{"innocent key", "host", false},
		{"metadata does not match", "metadata", false},
		{"public_key not a credential marker", "public_key", false},
		{"secretary false positive check", "secretary", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsSensitiveConfigKey(tc.key); got != tc.want {
				t.Errorf("IsSensitiveConfigKey(%q) = %v, want %v", tc.key, got, tc.want)
			}
		})
	}
}

func TestIsSensitiveConfigKeyMarkerPrefix(t *testing.T) {
	// 极端情况：marker 作为前缀/后缀出现同样命中。
	for _, key := range []string{"tokenvalue", "valuetoken", "TokenValue", "X-Auth-Token"} {
		if !IsSensitiveConfigKey(key) {
			t.Errorf("expected %q sensitive", key)
		}
	}
}
