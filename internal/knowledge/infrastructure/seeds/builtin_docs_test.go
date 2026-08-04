package seeds

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/agent/infrastructure/officialdocs"
	knowledge "github.com/byteBuilderX/stratum/internal/knowledge/application"
	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
	"github.com/stretchr/testify/require"
)

// --- pure helpers ---

func TestSectionSlug(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "ascii kept", in: "Getting-Started_2", want: "Getting-Started_2"},
		{name: "spaces become underscore", in: "getting started", want: "getting_started"},
		{name: "unicode becomes underscore", in: "中文 section", want: "___section"}, // per rune, not byte
		{name: "punctuation becomes underscore", in: "a.b/c:d", want: "a_b_c_d"},
		{name: "empty stays empty", in: "", want: ""},
		{name: "mixed runes", in: "A-1_中文!", want: "A-1____"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, sectionSlug(tc.in))
		})
	}
}

func TestFormatDocContent(t *testing.T) {
	entry := officialdocs.CatalogEntry{Title: "T", Section: "S", Body: "B"}
	require.Equal(t, "# T\n\n## S\n\nB", formatDocContent(entry))
}

func TestContentHash_deterministicAndDistinct(t *testing.T) {
	h1 := contentHash("same content")
	h2 := contentHash("same content")
	require.Equal(t, h1, h2)
	require.Len(t, h1, 64)
	require.NotEqual(t, h1, contentHash("different content"))
	// Empty content still hashes deterministically.
	require.Equal(t, contentHash(""), contentHash(""))
}

// --- SeedBuiltinDocs branch coverage ---

func TestSeedBuiltinDocs_nilIngest(t *testing.T) {
	n := SeedBuiltinDocs(context.Background(), "t1", "", nil, nil, zap.NewNop())
	require.Zero(t, n)
}

func TestSeedBuiltinDocs_nilDocRepo(t *testing.T) {
	ingest := knowledge.NewKnowledgeIngest(nil, nil, nil, nil, zap.NewNop())
	n := SeedBuiltinDocs(context.Background(), "t1", "", ingest, nil, zap.NewNop())
	require.Zero(t, n)
}

// stubDocRepo implements knowledgeport.DocRepo; only ExistsByHash is exercised.
type stubDocRepo struct {
	exists    bool
	existsErr error
}

func (s *stubDocRepo) ExistsByHash(context.Context, string, string, string) (bool, error) {
	return s.exists, s.existsErr
}
func (s *stubDocRepo) Save(context.Context, string, string, *domain.Document) error { return nil }
func (s *stubDocRepo) List(context.Context, string, string) ([]*domain.Document, error) {
	return nil, nil
}
func (s *stubDocRepo) Delete(context.Context, string, string, string) error           { return nil }
func (s *stubDocRepo) CountByWorkspace(context.Context, string, string) (int, error)  { return 0, nil }
func (s *stubDocRepo) MarkIngestStarted(context.Context, string, string, int) error   { return nil }
func (s *stubDocRepo) MarkIngestCompleted(context.Context, string, string, int) error { return nil }
func (s *stubDocRepo) MarkIngestFailed(context.Context, string, string, string) error { return nil }
func (s *stubDocRepo) RecoverStuckIngests(context.Context, string, time.Duration) (int, error) {
	return 0, nil
}

// errParser fails on every document so IngestDocument returns before spawning
// the background job; seeds must log-and-continue, never block startup.
type errParser struct{}

func (errParser) ParseBytes([]byte, string) (string, error) { return "", errors.New("parse boom") }

func TestSeedBuiltinDocs_existsCheckFails(t *testing.T) {
	ingest := knowledge.NewKnowledgeIngest(errParser{}, nil, nil, nil, zap.NewNop())
	docRepo := &stubDocRepo{existsErr: errors.New("db down")}
	n := SeedBuiltinDocs(context.Background(), "t1", "", ingest, docRepo, zap.NewNop())
	require.Zero(t, n)
}

func TestSeedBuiltinDocs_allSkipped(t *testing.T) {
	ingest := knowledge.NewKnowledgeIngest(errParser{}, nil, nil, nil, zap.NewNop())
	docRepo := &stubDocRepo{exists: true}
	n := SeedBuiltinDocs(context.Background(), "t1", "", ingest, docRepo, zap.NewNop())
	require.Zero(t, n)
}

func TestSeedBuiltinDocs_ingestFails(t *testing.T) {
	ingest := knowledge.NewKnowledgeIngest(errParser{}, nil, nil, nil, zap.NewNop())
	docRepo := &stubDocRepo{exists: false}
	n := SeedBuiltinDocs(context.Background(), "t1", "", ingest, docRepo, zap.NewNop())
	require.Zero(t, n)
}
