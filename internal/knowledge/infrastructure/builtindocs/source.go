// Package builtindocs reads the built-in platform knowledge documents from the
// embedded docs/knowledge tree into domain-port types. It is pure
// infrastructure: it depends only on stdlib, the docs/knowledge embed package
// and uuid, never on application code — so the DDD layering holds while the
// sync engine (application) consumes it through knowledgeport.BuiltinDocSource.
package builtindocs

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/google/uuid"

	knowledgedocs "github.com/byteBuilderX/stratum/docs/knowledge"
	knowledgeport "github.com/byteBuilderX/stratum/internal/knowledge/domain/port"
)

// builtinNamespace is a fixed UUID namespace reserved for built-in platform
// knowledge documents. It deliberately differs from uuid.NameSpaceOID that the
// legacy catalog seed used to derive its docIDs (those live in
// legacy_docs.json), so a docID can never collide across the two generations.
var builtinNamespace = uuid.MustParse("5a0c0000-0000-4000-8000-00000000000b")

//go:embed legacy_docs.json
var legacyDocIDsJSON []byte

// Source implements knowledgeport.BuiltinDocSource over the embedded
// docs/knowledge tree.
type Source struct{}

func New() *Source { return &Source{} }

// AllDocs returns every embedded knowledge document, sorted by Path so the
// sync order is deterministic across pods and restarts.
func (s *Source) AllDocs(_ context.Context) ([]knowledgeport.BuiltinDoc, error) {
	var docs []knowledgeport.BuiltinDoc
	err := fs.WalkDir(knowledgedocs.FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(p, ".md") {
			return nil
		}
		doc, err := loadDoc(p)
		if err != nil {
			return fmt.Errorf("builtindocs: load %s: %w", p, err)
		}
		docs = append(docs, doc)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(docs, func(i, j int) bool { return docs[i].Path < docs[j].Path })
	return docs, nil
}

func loadDoc(p string) (knowledgeport.BuiltinDoc, error) {
	raw, err := knowledgedocs.FS.ReadFile(p)
	if err != nil {
		return knowledgeport.BuiltinDoc{}, err
	}
	content := normalizeContent(string(raw))
	meta, content := splitFrontmatter(content)
	title := meta["title"]
	if title == "" {
		title = firstHeading(content)
	}
	return knowledgeport.BuiltinDoc{
		DocID:    DocID(p),
		Path:     p,
		Title:    title,
		Category: path.Dir(p),
		Content:  content,
		Hash:     ContentHash(content),
	}, nil
}

// DocID derives the deterministic UUIDv5 for a document path. Stable across
// processes and restarts; used as the knowledge_docs primary key so a doc is
// identified by its source path no matter how many times it is synced.
func DocID(p string) string {
	return uuid.NewSHA1(builtinNamespace, []byte("builtin:"+p)).String()
}

// ContentHash returns the sha256 hex of the normalized document content. The
// sync engine compares this against the stored hash to detect changes.
func ContentHash(content string) string {
	h := sha256.Sum256([]byte(content))
	return fmt.Sprintf("%x", h)
}

// LegacyDocIDs returns the docIDs produced by the legacy catalog seed (UUIDv5
// of "documentId:sectionSlug" under uuid.NameSpaceOID). They are marked as
// built-in legacy on first sync so the old section-level seed documents get
// cleaned up once the new per-file documents take over.
func LegacyDocIDs() ([]string, error) {
	var data struct {
		DocumentIDs []string `json:"documentIds"`
	}
	if err := json.Unmarshal(legacyDocIDsJSON, &data); err != nil {
		return nil, fmt.Errorf("decode legacy_docs.json: %w", err)
	}
	return data.DocumentIDs, nil
}

// normalizeContent canonicalizes a document body so a change to line endings
// or trailing whitespace alone does not count as a content change.
func normalizeContent(s string) string {
	return strings.TrimSpace(strings.ReplaceAll(s, "\r\n", "\n"))
}

// splitFrontmatter strips a minimal `---` fenced metadata block (key: value
// lines, parsed with stdlib only — no YAML dependency). Unknown keys are
// ignored; a malformed fence leaves the content untouched.
func splitFrontmatter(s string) (map[string]string, string) {
	if !strings.HasPrefix(s, "---\n") {
		return nil, s
	}
	rest := s[len("---\n"):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return nil, s
	}
	meta := map[string]string{}
	for _, line := range strings.Split(rest[:end], "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		meta[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	// 剔除 frontmatter 结束符后的前导换行：正文直接从标题开始，避免被
	// textchunk 当成独立的 preamble section（与 generate 的 stripFrontmatter 对齐）。
	return meta, strings.TrimLeft(rest[end+len("\n---"):], "\r\n")
}

// firstHeading returns the first `# ` heading text, or "" when absent.
func firstHeading(content string) string {
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return ""
}
