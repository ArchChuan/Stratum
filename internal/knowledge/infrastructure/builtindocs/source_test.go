package builtindocs

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestSplitFrontmatter_noFrontmatter(t *testing.T) {
	body := "# Heading\n\nplain text"
	meta, rest := splitFrontmatter(body)
	require.Nil(t, meta)
	require.Equal(t, body, rest)
}

func TestSplitFrontmatter_stripsBlockAndLeadingNewline(t *testing.T) {
	raw := "---\ntitle: Agent 使用指南\ncategory: guides\n---\n\n# Heading\nbody"
	meta, rest := splitFrontmatter(raw)
	require.Equal(t, map[string]string{"title": "Agent 使用指南", "category": "guides"}, meta)
	// 前导换行被剔除，正文从标题开始，避免产生独立的 preamble section。
	require.Equal(t, "# Heading\nbody", rest)
}

func TestSplitFrontmatter_unterminatedFenceLeavesContentUntouched(t *testing.T) {
	raw := "---\ntitle: broken\nno closing fence"
	meta, rest := splitFrontmatter(raw)
	require.Nil(t, meta)
	require.Equal(t, raw, rest)
}

func TestSplitFrontmatter_quotesAndUnknownKeys(t *testing.T) {
	raw := "---\ntitle: \"Quoted\"\norder: 3\nunused: x\n---\nbody"
	meta, rest := splitFrontmatter(raw)
	// 引号去引；数值与未知 key 也照常保留，调用方只取已知字段。
	require.Equal(t, "Quoted", meta["title"])
	require.Equal(t, "body", rest)
}

func TestNormalizeContent_canonicalizesCRLFAndWhitespace(t *testing.T) {
	require.Equal(t, "a\nb", normalizeContent("a\r\nb\r\n  \r\n"))
	require.Equal(t, "a\nb", normalizeContent("a\nb"))
}

func TestLoadDoc_usesFrontmatterTitleAndCategory(t *testing.T) {
	doc, err := loadDoc("guides/agent.md")
	require.NoError(t, err)
	require.Equal(t, "Agent 使用指南", doc.Title)
	require.Equal(t, "guides", doc.Category)
	require.True(t, strings.HasPrefix(doc.Content, "# Agent Development Rules"), "content starts after frontmatter")
	require.NotEmpty(t, doc.Hash)
	require.NotEmpty(t, doc.DocID)
}

func TestLoadDoc_titleFallsBackToFirstHeading(t *testing.T) {
	doc, err := loadDoc("reference/mcp-integration.md")
	require.NoError(t, err)
	require.Equal(t, "MCP 使用指南", doc.Title)
}

func TestDocID_deterministicAndStable(t *testing.T) {
	first := DocID("guides/agent.md")
	second := DocID("guides/agent.md")
	require.Equal(t, first, second, "same path must yield the same docID")
	require.NotEqual(t, first, DocID("reference/mcp-integration.md"), "different paths must differ")
	require.NoError(t, uuid.Validate(first))
}

func TestContentHash_deterministicAndSensitive(t *testing.T) {
	require.Equal(t, ContentHash("hello"), ContentHash("hello"))
	require.NotEqual(t, ContentHash("hello"), ContentHash("hello "), "content change must change the hash")
	require.Len(t, ContentHash("x"), 64)
}

func TestAllDocs_sortedUniqueAndComplete(t *testing.T) {
	docs, err := New().AllDocs(context.Background())
	require.NoError(t, err)
	require.NotEmpty(t, docs)
	seen := map[string]bool{}
	for i, d := range docs {
		require.True(t, strings.HasSuffix(d.Path, ".md"), "%s must be markdown", d.Path)
		require.NotEmpty(t, d.Content)
		require.NotEmpty(t, d.Title)
		require.NotEmpty(t, d.Category)
		require.NotEmpty(t, d.Hash)
		require.False(t, seen[d.DocID], "docID %s must be unique", d.DocID)
		seen[d.DocID] = true
		if i > 0 {
			require.True(t, docs[i-1].Path < d.Path, "docs must be sorted by path")
		}
	}
}

func TestLegacyDocIDs_uniqueUUIDs(t *testing.T) {
	ids, err := LegacyDocIDs()
	require.NoError(t, err)
	require.NotEmpty(t, ids)
	seen := map[string]bool{}
	for _, id := range ids {
		require.NoError(t, uuid.Validate(id), "%s must be a valid UUID", id)
		require.False(t, seen[id], "legacy docIDs must not repeat")
		seen[id] = true
	}
}
