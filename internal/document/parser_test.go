package document

import (
	"testing"

	"go.uber.org/zap"
)

func TestNewParser(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	if parser == nil {
		t.Error("expected parser to be non-nil")
	}

	if parser.logger == nil {
		t.Error("expected logger to be non-nil")
	}
}

func TestParseBytes(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	tests := []struct {
		name        string
		data        []byte
		contentType string
		expectErr   bool
	}{
		{
			name:        "plain text",
			data:        []byte("hello world"),
			contentType: "text/plain",
			expectErr:   false,
		},
		{
			name:        "markdown",
			data:        []byte("# Title\nContent"),
			contentType: "text/markdown",
			expectErr:   false,
		},
		{
			name:        "unsupported type",
			data:        []byte("data"),
			contentType: "application/unknown",
			expectErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parser.ParseBytes(tt.data, tt.contentType)
			if (err != nil) != tt.expectErr {
				t.Errorf("ParseBytes() error = %v, expectErr %v", err, tt.expectErr)
			}
			if !tt.expectErr && result == "" {
				t.Error("expected non-empty result for valid input")
			}
		})
	}
}

func TestParseFileUnsupported(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	_, err := parser.ParseFile("test.xyz")
	if err == nil {
		t.Error("expected error for unsupported file type")
	}
}
