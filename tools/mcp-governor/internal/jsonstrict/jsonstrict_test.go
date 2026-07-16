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
		{"case-aliased root key", `{"items":[],"Items":[]}`, `duplicate key "Items" at $`},
		{"duplicate nested key in array", `{"items":[{"id":1,"id":2}]}`, `duplicate key "id" at $.items[0]`},
		{"case-aliased nested key", `{"items":[{"start_ticks":1,"START_TICKS":2}]}`, `duplicate key "START_TICKS" at $.items[0]`},
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
