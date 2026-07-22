package domain

import "strings"

// IsSensitiveConfigKey reports whether a header or environment key commonly carries a credential.
func IsSensitiveConfigKey(key string) bool {
	normalized := strings.NewReplacer("_", "", "-", "", ".", "", " ", "").Replace(strings.ToLower(key))
	for _, marker := range []string{"authorization", "apikey", "token", "secret", "password", "credential"} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}
