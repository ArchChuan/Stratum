package document

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	knowledgeport "github.com/byteBuilderX/stratum/internal/knowledge/domain/port"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestParseFile_Errors(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)
	dir := t.TempDir()

	cases := []struct {
		name     string
		fileName string
	}{
		{"missing txt", filepath.Join(dir, "missing.txt")},
		{"missing pdf", filepath.Join(dir, "missing.pdf")},
		{"missing docx", filepath.Join(dir, "missing.docx")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parser.ParseFile(tc.fileName)
			require.Error(t, err)
		})
	}
}

func TestParseFile_TXT(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)
	dir := t.TempDir()
	path := filepath.Join(dir, "note.txt")
	require.NoError(t, os.WriteFile(path, []byte("hello from txt"), 0o600))

	text, err := parser.ParseFile(path)
	require.NoError(t, err)
	require.Equal(t, "hello from txt", text)
}

func TestParseFile_ReadFails(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	// 目录当文件读，触发 os.ReadFile 错误分支
	_, err := parser.ParseFile(t.TempDir())
	require.Error(t, err)
}

func TestParseBytes_PDFCorrupt(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	_, err := parser.ParseBytes([]byte("not a pdf"), "report.pdf")
	require.Error(t, err)

	_, err = parser.ParseBytes([]byte("not a pdf"), "application/pdf")
	require.Error(t, err)
}

func TestParseBytes_DOCXCorrupt(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	_, err := parser.ParseBytes([]byte("not a docx"), "doc.docx")
	require.Error(t, err)

	_, err = parser.ParseBytes([]byte("not a docx"), "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
	require.Error(t, err)
}

// TestParseBytes_PathHintDetectsExtension guards against the built-in docs
// sync regression where FileName carries a path (e.g. "guides/agent.md"). The
// old MIME detection treated any "/"-containing hint as a MIME type and failed
// with "unsupported content type" before extension detection could run.
func TestParseBytes_PathHintDetectsExtension(t *testing.T) {
	logger := zap.NewNop()
	parser := NewParser(logger)

	for _, hint := range []string{"guides/agent.md", "reference/mcp-integration.md", "docs/guides/knowledge-workspace.md"} {
		text, err := parser.ParseBytes([]byte("# Heading\nbody"), hint)
		require.NoError(t, err, "path hint %q must parse as markdown", hint)
		require.Equal(t, "# Heading\nbody", text)
	}

	// MIME hints still work after the reorder.
	text, err := parser.ParseBytes([]byte("plain"), "text/markdown")
	require.NoError(t, err)
	require.Equal(t, "plain", text)

	_, err = parser.ParseBytes([]byte("x"), "application/pdf")
	require.Error(t, err, "a real MIME hint that is corrupt must still fail")
}

func TestChunkingService_Chunk(t *testing.T) {
	svc := NewChunkingService()

	require.Equal(t, "", svc.Clean("   "))

	chunks, err := svc.Chunk(context.Background(), "a b c", "recursive", 100, 0, nil)
	require.NoError(t, err)
	require.NotEmpty(t, chunks.Leaves)

	_, err = svc.Chunk(context.Background(), "a", "no-such-strategy", 100, 0, nil)
	require.ErrorContains(t, err, "unknown chunking strategy")
}

func TestChunkingService_Filter(t *testing.T) {
	svc := NewChunkingService()

	filtered := svc.Filter([]knowledgeport.TextChunk{
		{Content: "  meaningful content here  ", ParentID: "p1"},
		{Content: "   ", ParentID: ""}, // 纯空白被过滤
	})
	require.Len(t, filtered, 1)
	require.Equal(t, "  meaningful content here  ", filtered[0].Content) // FilterChunks 不 trim
	require.Equal(t, "p1", filtered[0].ParentID)

	require.Empty(t, svc.Filter(nil))
}
