package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	knowledgeapp "github.com/byteBuilderX/stratum/internal/knowledge/application"
	knowledgedomain "github.com/byteBuilderX/stratum/internal/knowledge/domain"
	"gopkg.in/yaml.v3"
)

// goldenSnapshot mirrors the HTTP-serving subset of a workspace config. The
// tool only exercises the HTTP 保真子集: reranking "" and query_rewrite "none"
// (the /knowledge/query path never applies either), so both fields are pinned
// and validated at load time instead of silently ignored.
type goldenSnapshot struct {
	EmbeddingModel string  `yaml:"embedding_model" json:"embedding_model"`
	QueryMode      string  `yaml:"query_mode" json:"query_mode"`
	TopK           int     `yaml:"top_k" json:"top_k"`
	ChunkSize      int     `yaml:"chunk_size" json:"chunk_size"`
	ChunkOverlap   int     `yaml:"chunk_overlap" json:"chunk_overlap"`
	ScoreThreshold float64 `yaml:"score_threshold" json:"score_threshold"`
	Reranking      string  `yaml:"reranking" json:"reranking"`
	QueryRewrite   string  `yaml:"query_rewrite" json:"query_rewrite"`
}

// goldenCase is one retrieval query with its expected documents. Mode is a
// per-case query mode (vector/keyword/hybrid — all within the HTTP binding);
// keyword is never a workspace config value.
type goldenCase struct {
	ID                string   `yaml:"id" json:"id"`
	Query             string   `yaml:"query" json:"query"`
	Mode              string   `yaml:"mode" json:"mode"`
	RelevantDocuments []string `yaml:"relevant_documents" json:"relevant_documents"`
	// CitationDocuments is the expected complete context for the answer: all of
	// them must be retrieved (citation_correct). Empty (phase-1 default) means
	// the case does not contribute to the citation pass rate.
	CitationDocuments []string `yaml:"citation_documents,omitempty" json:"citation_documents,omitempty"`
	ExpectNoAnswer    bool     `yaml:"expect_no_answer" json:"expect_no_answer"`
	Note              string   `yaml:"note,omitempty" json:"note,omitempty"`
}

type goldenSet struct {
	Version  int            `yaml:"version" json:"version"`
	Snapshot goldenSnapshot `yaml:"snapshot" json:"snapshot"`
	Cases    []goldenCase   `yaml:"cases" json:"cases"`
}

// goldenDocument is one source markdown file to ingest. Its file name is the
// relevant_documents reference used by cases.
type goldenDocument struct {
	Source string
	Path   string
}

// loadGolden reads cases.yaml plus documents/*.md and validates the dataset as
// a whole. Failures here are dataset defects (exit 1), not infra failures.
func loadGolden(dir string) (goldenSet, []goldenDocument, error) {
	var set goldenSet
	if err := readYAML(filepath.Join(dir, "cases.yaml"), &set); err != nil {
		return set, nil, err
	}
	documents, err := loadDocuments(filepath.Join(dir, "documents"))
	if err != nil {
		return set, nil, err
	}
	if err := validateGolden(set, documents); err != nil {
		return set, nil, err
	}
	return set, documents, nil
}

func readYAML(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read golden dataset: %w", err)
	}
	if err := yaml.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode golden dataset %s: %w", path, err)
	}
	return nil
}

func loadDocuments(dir string) ([]goldenDocument, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read golden documents: %w", err)
	}
	documents := make([]goldenDocument, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		documents = append(documents, goldenDocument{Source: entry.Name(), Path: filepath.Join(dir, entry.Name())})
	}
	return documents, nil
}

func validateGolden(set goldenSet, documents []goldenDocument) error {
	if set.Version != 1 {
		return fmt.Errorf("golden dataset: unsupported version %d", set.Version)
	}
	if len(set.Cases) == 0 {
		return errors.New("golden dataset: at least one case required")
	}
	if err := validateSnapshot(set.Snapshot); err != nil {
		return err
	}
	sources := make(map[string]bool, len(documents))
	for _, doc := range documents {
		if sources[doc.Source] {
			return fmt.Errorf("golden dataset: duplicate source %q", doc.Source)
		}
		sources[doc.Source] = true
	}
	seen := make(map[string]bool, len(set.Cases))
	for _, tc := range set.Cases {
		if err := validateCase(tc, sources); err != nil {
			return err
		}
		if seen[tc.ID] {
			return fmt.Errorf("golden dataset: duplicate case id %q", tc.ID)
		}
		seen[tc.ID] = true
	}
	return nil
}

func validateSnapshot(s goldenSnapshot) error {
	if strings.TrimSpace(s.EmbeddingModel) == "" {
		return errors.New("golden dataset: snapshot.embedding_model required")
	}
	if !knowledgedomain.AllowedQueryModes[s.QueryMode] {
		return fmt.Errorf("golden dataset: snapshot.query_mode %q not in {vector,graph,hybrid}", s.QueryMode)
	}
	if err := validateTopK(s.TopK); err != nil {
		return err
	}
	if err := validateChunkAndThreshold(s); err != nil {
		return err
	}
	if s.Reranking != knowledgeapp.RerankingNone {
		return fmt.Errorf("golden dataset: snapshot.reranking must be %q (HTTP path never applies rerank), got %q",
			knowledgeapp.RerankingNone, s.Reranking)
	}
	if s.QueryRewrite != knowledgeapp.QueryRewriteNone {
		return fmt.Errorf("golden dataset: snapshot.query_rewrite must be %q (no server-side rewrite over HTTP), got %q",
			knowledgeapp.QueryRewriteNone, s.QueryRewrite)
	}
	return nil
}

func validateTopK(topK int) error {
	if topK != metricTopK || topK > HTTPTopKMax {
		return fmt.Errorf("golden dataset: snapshot.top_k must equal RetrievalK (%d) and be <= HTTP max (%d), got %d",
			metricTopK, HTTPTopKMax, topK)
	}
	return nil
}

func validateChunkAndThreshold(s goldenSnapshot) error {
	if s.ChunkSize <= 0 || s.ChunkOverlap < 0 {
		return errors.New("golden dataset: snapshot.chunk_size must be positive and chunk_overlap non-negative")
	}
	if s.ScoreThreshold < 0 || s.ScoreThreshold > 1 {
		return errors.New("golden dataset: snapshot.score_threshold must be in [0,1]")
	}
	return nil
}

func validateCase(tc goldenCase, sources map[string]bool) error {
	if strings.TrimSpace(tc.ID) == "" || strings.TrimSpace(tc.Query) == "" {
		return errors.New("golden dataset: case id and query are required")
	}
	if len(tc.Query) > MaxCaseQueryChars {
		return fmt.Errorf("golden dataset: case %s query exceeds %d chars", tc.ID, MaxCaseQueryChars)
	}
	if !isAllowedCaseMode(tc.Mode) {
		return fmt.Errorf("golden dataset: case %s mode %q not in {vector,keyword,hybrid} (HTTP binding)", tc.ID, tc.Mode)
	}
	if tc.ExpectNoAnswer {
		return nil
	}
	if len(tc.RelevantDocuments) == 0 {
		return fmt.Errorf("golden dataset: non-no-answer case %s must list relevant_documents", tc.ID)
	}
	if err := validateReferences(tc.ID, "relevant_documents", tc.RelevantDocuments, sources); err != nil {
		return err
	}
	return validateReferences(tc.ID, "citation_documents", tc.CitationDocuments, sources)
}

// isAllowedCaseMode accepts the three per-case query modes the HTTP path
// serves. keyword is never a workspace config value but is valid per-case.
func isAllowedCaseMode(mode string) bool {
	switch mode {
	case "vector", "keyword", "hybrid":
		return true
	default:
		return false
	}
}

// validateReferences ensures every referenced source resolves to an ingested
// document, with a field-specific error message for the citation list.
func validateReferences(caseID, field string, references []string, sources map[string]bool) error {
	for _, source := range references {
		if !sources[source] {
			if field == "citation_documents" {
				return fmt.Errorf("golden dataset: case %s citation references unknown document %q", caseID, source)
			}
			return fmt.Errorf("golden dataset: case %s references unknown document %q", caseID, source)
		}
	}
	return nil
}

func documentSources(documents []goldenDocument) []string {
	sources := make([]string, 0, len(documents))
	for _, doc := range documents {
		sources = append(sources, doc.Source)
	}
	return sources
}
