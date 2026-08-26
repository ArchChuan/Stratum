package application

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	knowledgeport "github.com/byteBuilderX/stratum/internal/knowledge/domain/port"
	"github.com/byteBuilderX/stratum/internal/knowledge/infrastructure/document"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// replaceDocRepo embeds the ingest mock and makes CASReplace controllable, so
// the replace admission (win / lose / failure) is driven deterministically.
type replaceDocRepo struct {
	mockDocRepo
	casWins  bool
	casErr   error
	casCalls int
}

func (r *replaceDocRepo) CASReplace(context.Context, string, string, string, string, string, string, map[string]any, int) (bool, error) {
	r.casCalls++
	if r.casErr != nil {
		return false, r.casErr
	}
	return r.casWins, nil
}

// replaceVectorStore records the purge-vs-insert order so the replace contract
// (delete old vectors BEFORE embedding the new version) is provable.
type replaceVectorStore struct {
	MockVectorStore
	ops         []string
	insertCalls int
	purgeErr    error
}

func (v *replaceVectorStore) Insert(_ context.Context, collection string, chunks []knowledgeport.VectorDocument) error {
	v.insertCalls++
	v.ops = append(v.ops, "insert:"+collection)
	return nil
}

func (v *replaceVectorStore) DeleteByDocumentIDs(_ context.Context, _ string, docIDs []string) error {
	if v.purgeErr != nil {
		return v.purgeErr
	}
	v.ops = append(v.ops, "purge:"+docIDs[0])
	return nil
}

func replaceRequest(docID, expectedHash string) IngestDocumentRequest {
	return IngestDocumentRequest{
		TenantID:            "t1",
		Workspace:           "ws1",
		WorkspaceID:         "wsid-1",
		EmbeddingModel:      "text-embedding-v3",
		DocumentID:          docID,
		FileName:            "guides/a.md",
		ContentHash:         "newhash",
		Title:               "Agent Guide",
		Metadata:            map[string]any{"builtin_source": "guides/a.md"},
		ExpectedContentHash: expectedHash,
	}
}

func replaceLeaves() []knowledgeport.TextChunk {
	return []knowledgeport.TextChunk{{Content: "one"}, {Content: "two"}}
}

func TestPersistDocumentReplace_winPurgesOldVectorsBeforeSpawn(t *testing.T) {
	vs := &replaceVectorStore{}
	repo := &replaceDocRepo{casWins: true}
	ki := NewKnowledgeIngest(&mockParser{}, document.NewChunkingService(), &mockEmbedder{dim: 4}, vs, zap.NewNop())
	ki.SetDocRepo(repo)

	res, spawn, err := ki.persistDocumentGate(context.Background(), replaceRequest("docA", "oldhash"), replaceLeaves())

	require.NoError(t, err)
	require.True(t, spawn, "CAS winner owns the ingest pipeline")
	require.Nil(t, res)
	require.Equal(t, 1, repo.casCalls)
	require.Equal(t, []string{"purge:docA"}, vs.ops, "old vectors purged synchronously before any insert")
	require.Equal(t, 0, vs.insertCalls)
}

func TestPersistDocumentReplace_loseReturnsAlreadyExistsWithoutPurge(t *testing.T) {
	vs := &replaceVectorStore{}
	repo := &replaceDocRepo{casWins: false} // sibling pod owns the replace
	ki := NewKnowledgeIngest(&mockParser{}, document.NewChunkingService(), &mockEmbedder{dim: 4}, vs, zap.NewNop())
	ki.SetDocRepo(repo)

	res, spawn, err := ki.persistDocumentGate(context.Background(), replaceRequest("docA", "oldhash"), replaceLeaves())

	require.NoError(t, err)
	require.False(t, spawn, "CAS loser must never spawn the pipeline")
	require.True(t, res.AlreadyExists, "loser is reported as an existing-skip")
	require.Empty(t, vs.ops, "loser must not touch any vectors")
}

func TestPersistDocumentReplace_claimFailureFailsClosed(t *testing.T) {
	vs := &replaceVectorStore{}
	repo := &replaceDocRepo{casErr: errors.New("db down")}
	ki := NewKnowledgeIngest(&mockParser{}, document.NewChunkingService(), &mockEmbedder{dim: 4}, vs, zap.NewNop())
	ki.SetDocRepo(repo)

	_, spawn, err := ki.persistDocumentGate(context.Background(), replaceRequest("docA", "oldhash"), replaceLeaves())

	require.False(t, spawn)
	require.ErrorContains(t, err, "claim document replace")
	require.Empty(t, vs.ops, "claim failure must not purge vectors")
}

func TestPersistDocumentReplace_purgeFailureLeavesDocProcessing(t *testing.T) {
	vs := &replaceVectorStore{purgeErr: errors.New("milvus down")}
	repo := &replaceDocRepo{casWins: true}
	ki := NewKnowledgeIngest(&mockParser{}, document.NewChunkingService(), &mockEmbedder{dim: 4}, vs, zap.NewNop())
	ki.SetDocRepo(repo)

	_, spawn, err := ki.persistDocumentGate(context.Background(), replaceRequest("docA", "oldhash"), replaceLeaves())

	require.False(t, spawn, "a half-replaced document must not be embedded")
	require.ErrorContains(t, err, "purge replaced document vectors")
}

// TestIngestDocument_replaceWinnerRunsFullPipeline verifies the replace winner
// really spawns the background pipeline (embed → insert → completed) and that
// the vector purge happens before the new insert.
func TestIngestDocument_replaceWinnerRunsFullPipeline(t *testing.T) {
	vs := &replaceVectorStore{}
	repo := &replaceDocRepo{casWins: true}
	ki := NewKnowledgeIngest(&mockParser{out: "para one\npara two\npara three"}, document.NewChunkingService(), &mockEmbedder{dim: 4}, vs, zap.NewNop())
	ki.SetDocRepo(repo)

	req := replaceRequest("docA", "oldhash")
	_, err := ki.IngestDocument(context.Background(), req)
	require.NoError(t, err)
	require.NoError(t, ki.Shutdown(context.Background()), "wait for the background pipeline")

	require.Equal(t, 1, repo.casCalls)
	require.Equal(t, 1, vs.insertCalls, "winner embeds the new version")
	require.Len(t, vs.ops, 2)
	require.Equal(t, "purge:docA", vs.ops[0], "old vectors purged before the new insert")
	require.Equal(t, "insert:"+constants.CollectionName("t1", "wsid-1", "text-embedding-v3"), vs.ops[1])
	require.Len(t, repo.markCompleted, 1, "pipeline reached the terminal completed state")
	require.Equal(t, "docA", repo.markCompleted[0].ID)
}
