package officialdocs

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
)

func TestSearchReturnsConfiguredCitationShapeForChineseAndASCIIQueries(t *testing.T) {
	tests := []struct {
		name        string
		query       string
		documentID  string
		title       string
		url         string
		excerptText string
	}{
		{
			name:        "Chinese knowledge query",
			query:       "知识库摄取",
			documentID:  "knowledge-guide",
			title:       "Knowledge 使用指南",
			url:         "/docs/agent/knowledge-workspace",
			excerptText: "摄取",
		},
		{
			name:        "ASCII MCP query",
			query:       "MCP transport",
			documentID:  "mcp-guide",
			title:       "MCP 使用指南",
			url:         "/docs/mcp-integration",
			excerptText: "transport",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			citations, err := Search(context.Background(), tt.query)
			if err != nil {
				t.Fatalf("Search() error = %v", err)
			}
			if len(citations) == 0 {
				t.Fatal("Search() returned no citations")
			}

			got := citations[0]
			if got.DocumentID != tt.documentID || got.Title != tt.title {
				t.Fatalf("Search() first citation = %#v", got)
			}
			if got.ProductVersion != "2026.07" || got.URL != tt.url {
				t.Fatalf("Search() version/link = %q/%q", got.ProductVersion, got.URL)
			}
			if strings.TrimSpace(got.Section) == "" || strings.TrimSpace(got.Excerpt) == "" {
				t.Fatalf("Search() returned incomplete citation = %#v", got)
			}
			if !strings.Contains(strings.ToLower(got.Excerpt), strings.ToLower(tt.excerptText)) {
				t.Fatalf("Search() excerpt %q does not contain %q", got.Excerpt, tt.excerptText)
			}
		})
	}
}

func TestSearchOrderingIsStableAndExcerptIsBounded(t *testing.T) {
	first, err := Search(context.Background(), "Agent tool")
	if err != nil {
		t.Fatalf("first Search() error = %v", err)
	}
	second, err := Search(context.Background(), "Agent tool")
	if err != nil {
		t.Fatalf("second Search() error = %v", err)
	}
	if len(first) == 0 || len(first) != len(second) {
		t.Fatalf("Search() result lengths = %d and %d", len(first), len(second))
	}

	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("Search() ordering differs at %d: %#v != %#v", i, first[i], second[i])
		}
		if utf8.RuneCountInString(first[i].Excerpt) > MaxExcerptRunes {
			t.Fatalf("excerpt rune count = %d, want <= %d", utf8.RuneCountInString(first[i].Excerpt), MaxExcerptRunes)
		}
	}
}

func TestSearchRejectsEmptyQuery(t *testing.T) {
	for _, query := range []string{"", " \n\t "} {
		citations, err := Search(context.Background(), query)
		if !errors.Is(err, domain.ErrInvalidOfficialEvidenceQuery) {
			t.Fatalf("Search(%q) error = %v", query, err)
		}
		if len(citations) != 0 {
			t.Fatalf("Search(%q) citations = %#v, want none", query, citations)
		}
	}
}

func TestSearchReturnsNotFoundWithoutFabricatedCitation(t *testing.T) {
	citations, err := Search(context.Background(), "zzzxxyyqqq nonexistent-official-topic")
	if !errors.Is(err, domain.ErrOfficialEvidenceNotFound) {
		t.Fatalf("Search() error = %v", err)
	}
	if len(citations) != 0 {
		t.Fatalf("Search() citations = %#v, want none", citations)
	}
}
