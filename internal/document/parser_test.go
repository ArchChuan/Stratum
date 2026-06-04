package document

import (
	"testing"

	"go.uber.org/zap"
)

func TestNewParser(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	if parser == nil { //nolint:staticcheck
		t.Error("expected parser to be non-nil")
	}

	if parser.logger == nil { //nolint:staticcheck
		t.Error("expected logger to be non-nil")
	}
}

func TestParseBytes(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	tests := []struct {
		name      string
		data      []byte
		hint      string
		expectErr bool
	}{
		// MIME type inputs
		{
			name:      "plain text via MIME type",
			data:      []byte("hello world"),
			hint:      "text/plain",
			expectErr: false,
		},
		{
			name:      "markdown via MIME type",
			data:      []byte("# Title\nContent"),
			hint:      "text/markdown",
			expectErr: false,
		},
		{
			name:      "unsupported MIME type",
			data:      []byte("data"),
			hint:      "application/unknown",
			expectErr: true,
		},
		// File name extension inputs
		{
			name:      "plain text via .txt extension",
			data:      []byte("hello world"),
			hint:      "document.txt",
			expectErr: false,
		},
		{
			name:      "markdown via .md extension",
			data:      []byte("# Title\nContent"),
			hint:      "README.md",
			expectErr: false,
		},
		{
			name:      "uppercase extension",
			data:      []byte("hello"),
			hint:      "NOTE.TXT",
			expectErr: false,
		},
		{
			name:      "unsupported extension",
			data:      []byte("data"),
			hint:      "file.xyz",
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parser.ParseBytes(tt.data, tt.hint)
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
