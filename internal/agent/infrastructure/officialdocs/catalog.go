// Package officialdocs searches a generated, immutable catalog of platform
// documentation. Runtime search never reads repository or tenant files.
package officialdocs

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
)

const (
	MaxResults      = 5
	MaxExcerptRunes = 240

	titleWeight   = 8
	sectionWeight = 5
	bodyWeight    = 1
)

//go:generate go run ./generate -manifest ../../../../docs/assistant/catalog.yaml -out catalog.json

//go:embed catalog.json
var catalogJSON []byte

type catalogEntry struct {
	DocumentID     string `json:"documentId"`
	Title          string `json:"title"`
	ProductVersion string `json:"productVersion"`
	Section        string `json:"section"`
	URL            string `json:"url"`
	Ordinal        int    `json:"ordinal"`
	Body           string `json:"body"`
}

type scoredEntry struct {
	entry catalogEntry
	score int
}

// Search returns only citations backed by the embedded official catalog.
func Search(ctx context.Context, query string) ([]domain.Citation, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("search official docs: %w", err)
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, domain.ErrInvalidOfficialEvidenceQuery
	}

	var entries []catalogEntry
	if err := json.Unmarshal(catalogJSON, &entries); err != nil {
		return nil, fmt.Errorf("decode embedded official docs catalog: %w", err)
	}
	tokens := tokenize(query)
	if len(tokens) == 0 {
		return nil, domain.ErrInvalidOfficialEvidenceQuery
	}

	matches := make([]scoredEntry, 0, len(entries))
	for _, entry := range entries {
		score, coverage := score(entry, tokens)
		if score > 0 && coverage >= minimumCoverage(len(tokens)) {
			matches = append(matches, scoredEntry{entry: entry, score: score})
		}
	}
	if len(matches) == 0 {
		return nil, domain.ErrOfficialEvidenceNotFound
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].score != matches[j].score {
			return matches[i].score > matches[j].score
		}
		left, right := matches[i].entry, matches[j].entry
		if left.DocumentID != right.DocumentID {
			return left.DocumentID < right.DocumentID
		}
		if left.Section != right.Section {
			return left.Section < right.Section
		}
		return left.Ordinal < right.Ordinal
	})
	if len(matches) > MaxResults {
		matches = matches[:MaxResults]
	}

	citations := make([]domain.Citation, 0, len(matches))
	for _, match := range matches {
		entry := match.entry
		citations = append(citations, domain.Citation{
			DocumentID:     entry.DocumentID,
			Title:          entry.Title,
			ProductVersion: entry.ProductVersion,
			Section:        entry.Section,
			URL:            entry.URL,
			Excerpt:        excerpt(entry.Body, tokens),
		})
	}
	return citations, nil
}

func score(entry catalogEntry, tokens []string) (int, int) {
	title := tokenSet(entry.Title)
	section := tokenSet(entry.Section)
	body := tokenSet(entry.Body)
	total := 0
	coverage := 0
	for _, token := range tokens {
		weight := 0
		if _, ok := body[token]; ok {
			weight = bodyWeight
		}
		if _, ok := section[token]; ok {
			weight = sectionWeight
		}
		if _, ok := title[token]; ok {
			weight = titleWeight
		}
		if weight > 0 {
			coverage++
		}
		total += weight
	}
	return total, coverage
}

func minimumCoverage(tokenCount int) int {
	if tokenCount <= 1 {
		return tokenCount
	}
	return (tokenCount*2 + 2) / 3
}

func tokenSet(input string) map[string]struct{} {
	tokens := tokenize(input)
	set := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		set[token] = struct{}{}
	}
	return set
}

func tokenize(input string) []string {
	input = strings.ToLower(input)
	seen := make(map[string]struct{})
	var tokens []string
	var ascii strings.Builder
	var chinese []rune
	flushASCII := func() {
		if ascii.Len() == 0 {
			return
		}
		if ascii.Len() > 1 {
			appendToken(&tokens, seen, ascii.String())
		}
		ascii.Reset()
	}
	flushChinese := func() {
		for i := 0; i+1 < len(chinese); i++ {
			appendToken(&tokens, seen, string(chinese[i:i+2]))
		}
		chinese = chinese[:0]
	}

	for _, r := range input {
		switch {
		case isASCIILetterOrDigit(r):
			flushChinese()
			ascii.WriteRune(r)
		case unicode.Is(unicode.Han, r):
			flushASCII()
			chinese = append(chinese, r)
		default:
			flushASCII()
			flushChinese()
		}
	}
	flushASCII()
	flushChinese()
	return tokens
}

func appendToken(tokens *[]string, seen map[string]struct{}, token string) {
	if token == "" {
		return
	}
	if _, ok := seen[token]; ok {
		return
	}
	seen[token] = struct{}{}
	*tokens = append(*tokens, token)
}

func isASCIILetterOrDigit(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
}

func excerpt(body string, tokens []string) string {
	normalized := strings.Join(strings.Fields(body), " ")
	runes := []rune(normalized)
	if len(runes) <= MaxExcerptRunes {
		return normalized
	}

	lower := strings.ToLower(normalized)
	matchByte := -1
	for _, token := range tokens {
		if idx := exactTokenIndex(lower, token); idx >= 0 && (matchByte < 0 || idx < matchByte) {
			matchByte = idx
		}
	}
	matchRune := 0
	if matchByte > 0 {
		matchRune = utf8.RuneCountInString(normalized[:matchByte])
	}
	start := matchRune - MaxExcerptRunes/3
	if start < 0 {
		start = 0
	}
	end := start + MaxExcerptRunes
	if end > len(runes) {
		end = len(runes)
		start = end - MaxExcerptRunes
	}
	return strings.TrimSpace(string(runes[start:end]))
}

func exactTokenIndex(input, token string) int {
	if !isASCIIString(token) {
		return strings.Index(input, token)
	}
	searchFrom := 0
	for searchFrom < len(input) {
		relative := strings.Index(input[searchFrom:], token)
		if relative < 0 {
			return -1
		}
		index := searchFrom + relative
		end := index + len(token)
		leftBoundary := index == 0 || !isASCIIWordByte(input[index-1])
		rightBoundary := end == len(input) || !isASCIIWordByte(input[end])
		if leftBoundary && rightBoundary {
			return index
		}
		searchFrom = index + 1
	}
	return -1
}

func isASCIIString(input string) bool {
	for _, r := range input {
		if r > unicode.MaxASCII {
			return false
		}
	}
	return true
}

func isASCIIWordByte(value byte) bool {
	return value >= 'a' && value <= 'z' || value >= '0' && value <= '9'
}
