package jsonstrict

import (
	"strings"
	"testing"
)

func TestValidateNoDuplicateKeys(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr string
	}{
		{"unique objects", `{"items":[{"id":1},{"id":2}]}`, ""},
		{"duplicate root key", `{"items":[],"items":[]}`, `duplicate key "items" at $`},
		{"duplicate nested key in array", `{"items":[{"id":1,"id":2}]}`, `duplicate key "id" at $.items[0]`},
		{"malformed JSON", `{"items":`, "EOF"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateNoDuplicateKeys([]byte(tt.input))
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateNoDuplicateKeys: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %v; want containing %q", err, tt.wantErr)
			}
		})
	}
}
