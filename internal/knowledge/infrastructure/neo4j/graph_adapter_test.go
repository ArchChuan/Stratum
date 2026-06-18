package neo4j

import (
	"testing"
)

func TestValidateCypherIdentifier(t *testing.T) {
	tests := []struct {
		input   string
		wantErr bool
	}{
		{"Document", false},
		{"HAS_CHUNK", false},
		{"_private", false},
		{"Type123", false},
		{"a", false},
		{"", true},
		{"has chunk", true},
		{"has-chunk", true},
		{"123start", true},
		{"type;DROP", true},
		{"label`injection", true},
		{"中文", true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			err := validateCypherIdentifier(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateCypherIdentifier(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
		})
	}
}

func TestEscapeLucene(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"plain", "hello", "hello"},
		{"plus", "+", `\+`},
		{"backslash", `\`, `\\`},
		{"backslash_plus", `\+`, `\\\+`},
		{"inline_plus", "a+b", `a\+b`},
		{"colon", "test:value", `test\:value`},
		{"slash", "path/to/file", `path\/to\/file`},
		{"double_plus", "C++ error", `C\+\+ error`},
		{"brackets", "[ERROR]", `\[ERROR\]`},
		{"quotes", `query"with"quotes`, `query\"with\"quotes`},
		{"tilde", "range~0.5", `range\~0.5`},
		{"question", "field?wildcard", `field\?wildcard`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := escapeLucene(tt.input)
			if got != tt.expected {
				t.Errorf("escapeLucene(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}
