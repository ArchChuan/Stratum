package http_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bannedCredentialFieldNames are credential value field names that must
// never appear in a golden response body (§6 敏感字段策略). A masked value
// field is one typo away from leaking — ban the name, not the value.
var bannedCredentialFieldNames = map[string]bool{
	"token":         true,
	"secret":        true,
	"api_key_value": true,
	"client_secret": true,
	"access_key":    true,
	"password":      true,
	"access_token":  true,
	"refresh_token": true,
	"secret_key":    true,
}

// allowedCredentialFieldNames names a request header key (not a value).
var allowedCredentialFieldNames = map[string]bool{"api_key_header": true}

func TestGoldenNoCredentialValueFields(t *testing.T) {
	entries, err := os.ReadDir("testdata/contracts")
	if err != nil {
		t.Fatalf("read golden dir: %v", err)
	}
	found := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".golden.json") {
			continue
		}
		body, err := os.ReadFile(filepath.Join("testdata/contracts", e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		var v any
		if err := json.Unmarshal(body, &v); err != nil {
			t.Fatalf("unmarshal %s: %v", e.Name(), err)
		}
		var walk func(prefix string, v any)
		walk = func(prefix string, v any) {
			switch m := v.(type) {
			case map[string]any:
				for k, val := range m {
					if bannedCredentialFieldNames[k] && !allowedCredentialFieldNames[k] {
						t.Errorf("%s: banned credential field %s%s", e.Name(), prefix, k)
						found++
					}
					walk(prefix+k+".", val)
				}
			case []any:
				for i, val := range m {
					walk(fmt.Sprintf("%s[%d].", prefix, i), val)
				}
			}
		}
		walk("", v)
	}
	if found > 0 {
		t.Errorf("found %d banned credential fields; remove them from the response DTO/proto (fail-closed, no exemptions)", found)
	}
}
