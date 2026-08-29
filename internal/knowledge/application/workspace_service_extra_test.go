package application

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"mime/multipart"
	"testing"
	"time"

	"go.uber.org/zap"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
	"github.com/byteBuilderX/stratum/internal/knowledge/domain/port"
	"github.com/byteBuilderX/stratum/internal/knowledge/infrastructure/document"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// fakeWorkspaceRepo 实现 port.WorkspaceRepo；按 name 保存，脚本化错误。
type fakeWorkspaceRepo struct {
	workspaces map[string]*domain.Workspace

	createErr error
	getErr    error
	listErr   error
	deleteErr error
	updateErr error
	configErr error

	deleted []string
	renames []struct{ oldName, newName string }

	// audits 捕获每次写方法收到的审计事件（nil 参数不记录）。
	audits []*auditdomain.ResourceChangeAuditEvent
}

var _ port.WorkspaceRepo = (*fakeWorkspaceRepo)(nil)

func newFakeWorkspaceRepo() *fakeWorkspaceRepo {
	return &fakeWorkspaceRepo{workspaces: map[string]*domain.Workspace{}}
}

func (f *fakeWorkspaceRepo) Create(_ context.Context, _ string, ws *domain.Workspace, _ []string, audit *auditdomain.ResourceChangeAuditEvent) error {
	if audit != nil {
		f.audits = append(f.audits, audit)
	}
	if f.createErr != nil {
		return f.createErr
	}
	if ws.ID == "" {
		ws.ID = "wsid-" + ws.Name
	}
	f.workspaces[ws.Name] = ws
	return nil
}

func (f *fakeWorkspaceRepo) GetByName(_ context.Context, _, name string) (*domain.Workspace, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	ws, ok := f.workspaces[name]
	if !ok {
		return nil, domain.ErrWorkspaceNotFound
	}
	return ws, nil
}

func (f *fakeWorkspaceRepo) GetByID(_ context.Context, _, id string) (*domain.Workspace, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	for _, ws := range f.workspaces {
		if ws.ID == id {
			return ws, nil
		}
	}
	return nil, domain.ErrWorkspaceNotFound
}

func (f *fakeWorkspaceRepo) List(_ context.Context, _ string) ([]*domain.Workspace, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]*domain.Workspace, 0, len(f.workspaces))
	for _, ws := range f.workspaces {
		out = append(out, ws)
	}
	return out, nil
}

func (f *fakeWorkspaceRepo) UpdateWorkspaceAll(_ context.Context, _, name string, renameTo, description *string, snap domain.KnowledgeWorkspaceSnapshot, _ string, _ string, audit *auditdomain.ResourceChangeAuditEvent) error {
	if audit != nil {
		f.audits = append(f.audits, audit)
	}
	if f.updateErr != nil {
		return f.updateErr
	}
	ws, ok := f.workspaces[name]
	if !ok {
		return domain.ErrWorkspaceNotFound
	}
	if renameTo != nil {
		delete(f.workspaces, name)
		ws.Name = *renameTo
		f.workspaces[*renameTo] = ws
		f.renames = append(f.renames, struct{ oldName, newName string }{name, *renameTo})
	}
	if description != nil {
		ws.Description = *description
	}
	ws.Config = snap.Config
	return nil
}

func (f *fakeWorkspaceRepo) Delete(_ context.Context, _, name string, audit *auditdomain.ResourceChangeAuditEvent) error {
	if audit != nil {
		f.audits = append(f.audits, audit)
	}
	if f.deleteErr != nil {
		return f.deleteErr
	}
	delete(f.workspaces, name)
	f.deleted = append(f.deleted, name)
	return nil
}

func (f *fakeWorkspaceRepo) GetConfigForUpload(_ context.Context, _, name string) (domain.WorkspaceConfig, error) {
	if f.configErr != nil {
		return domain.WorkspaceConfig{}, f.configErr
	}
	ws, ok := f.workspaces[name]
	if !ok {
		return domain.WorkspaceConfig{}, domain.ErrWorkspaceNotFound
	}
	return ws.Config, nil
}

func (f *fakeWorkspaceRepo) GetConfigByID(_ context.Context, _, id string) (domain.WorkspaceConfig, error) {
	ws, err := f.GetByID(context.Background(), "t1", id)
	if err != nil {
		return domain.WorkspaceConfig{}, err
	}
	return ws.Config, nil
}

// collectionStub 记录 collectionProvisioner 调用。
type collectionStub struct {
	created []struct {
		name string
		dim  int
	}
	createErr    error
	deletedByDoc map[string][]string
	deleteErr    error
}

func newCollectionStub() *collectionStub {
	return &collectionStub{deletedByDoc: map[string][]string{}}
}

func (c *collectionStub) CreateCollectionWithDim(_ context.Context, name string, dim int) error {
	if c.createErr != nil {
		return c.createErr
	}
	c.created = append(c.created, struct {
		name string
		dim  int
	}{name, dim})
	return nil
}

func (c *collectionStub) DeleteByDocumentIDs(_ context.Context, collectionName string, docIDs []string) error {
	if c.deleteErr != nil {
		return c.deleteErr
	}
	c.deletedByDoc[collectionName] = append(c.deletedByDoc[collectionName], docIDs...)
	return nil
}

// fakeModelExists 实现 port.ModelExists；目录为空时能力内模型不存在，
// err 可脚本化目录查询失败。
type fakeModelExists struct {
	embedding map[string]bool
	rerank    map[string]bool
	chat      map[string]bool
	err       error
}

func (f *fakeModelExists) Exists(_ context.Context, model string, capability port.ModelCapability) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	switch capability {
	case port.CapRerank:
		return f.rerank[model], nil
	case port.CapChat:
		return f.chat[model], nil
	default:
		return f.embedding[model], nil
	}
}

// buildWorkspaceService 组装 WorkspaceService + 可选的 ingest 依赖。
func buildWorkspaceService(repo *fakeWorkspaceRepo) (*WorkspaceService, *KnowledgeIngest) {
	// parser 输出段落文本，确保 chunking 产生非空 chunks。
	ki := NewKnowledgeIngest(&mockParser{out: paragraphInput(3)}, document.NewChunkingService(), &mockEmbedder{dim: 1024}, NewMockVectorStore(), zap.NewNop())
	svc := NewWorkspaceService(repo, ki, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})
	return svc, ki
}

func seedWorkspace(repo *fakeWorkspaceRepo, name string) *domain.Workspace {
	// 嵌入模型必须显式配置（无静态兜底），seed 构造补显式模型。
	ws, err := domain.NewWorkspace(name, "desc", domain.WorkspaceConfig{EmbeddingModel: "text-embedding-v3"}, domain.DefaultChunkSize, domain.DefaultTopK)
	if err != nil {
		panic(err)
	}
	ws.ID = "wsid-" + name
	repo.workspaces[name] = ws
	return ws
}

func TestDimensionForModelPin(t *testing.T) {
	cases := []struct {
		model string
		want  int
	}{
		{"text-embedding-v1", 1536},
		{"text-embedding-v2", 1024},
		{"text-embedding-v3", 1024},
		{"text-embedding-v4", 1024},
		{"embedding-3", 2048},
		{"text-embedding-3-small", 1536}, // default
		{"", 1536},                       // default
		{"unknown-model", 1536},          // default
	}
	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			if got := constants.DimensionForModel(tc.model); got != tc.want {
				t.Fatalf("DimensionForModel(%q) = %d, want %d", tc.model, got, tc.want)
			}
		})
	}
}

func TestWorkspaceCreateSuccessAndCollection(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	store := newCollectionStub()
	svc, _ := buildWorkspaceService(repo)
	svc.SetVectorStore(store)

	ws, err := svc.CreateWorkspace(context.Background(), "t1", CreateWorkspaceInput{
		Name: "ws1", Description: "d", Config: domain.WorkspaceConfig{EmbeddingModel: "text-embedding-v3"},
	}, "user-1")
	if err != nil {
		t.Fatalf("create = %v", err)
	}
	if ws.ID == "" {
		t.Fatal("id must be set by repo")
	}
	if len(store.created) != 1 {
		t.Fatalf("collections = %+v", store.created)
	}
	if store.created[0].name != constants.CollectionName("t1", ws.ID, ws.Config.EmbeddingModel) || store.created[0].dim != 1024 {
		t.Fatalf("collection = %+v", store.created[0])
	}
	// embedding-3 → 2048 dim。
	store2 := newCollectionStub()
	svc2, _ := buildWorkspaceService(repo)
	svc2.SetVectorStore(store2)
	_, _ = svc2.CreateWorkspace(context.Background(), "t1", CreateWorkspaceInput{
		Name: "ws2", Config: domain.WorkspaceConfig{EmbeddingModel: "embedding-3"},
	}, "user-1")
	if store2.created[0].dim != 2048 {
		t.Fatalf("dim = %d", store2.created[0].dim)
	}
}

func TestWorkspaceCreateWithoutVectorStore(t *testing.T) {
	// 极端情况：vectorStore 未注入时跳过 collection 创建。
	repo := newFakeWorkspaceRepo()
	svc, _ := buildWorkspaceService(repo)
	if _, err := svc.CreateWorkspace(context.Background(), "t1", CreateWorkspaceInput{Name: "ws1", Config: domain.WorkspaceConfig{EmbeddingModel: "text-embedding-v3"}}, "user-1"); err != nil {
		t.Fatalf("create = %v", err)
	}
}

func TestWorkspaceCreateValidationAndErrors(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	svc, _ := buildWorkspaceService(repo)
	svc.SetModelExists(&fakeModelExists{embedding: map[string]bool{"text-embedding-v3": true}})

	// 目录校验：非法 embedding model 不在全局目录。
	if _, err := svc.CreateWorkspace(context.Background(), "t1", CreateWorkspaceInput{
		Name: "ws", Config: domain.WorkspaceConfig{EmbeddingModel: "nope"},
	}, "user-1"); !errors.Is(err, domain.ErrInvalidEmbeddingModel) {
		t.Fatalf("invalid model err = %v", err)
	}
	// 必填校验：缺 embedding model 且其余字段合法 → 400 语义错误（无静态兜底）。
	if _, err := svc.CreateWorkspace(context.Background(), "t1", CreateWorkspaceInput{
		Name: "ws-missing-model", Config: domain.WorkspaceConfig{QueryMode: "hybrid"},
	}, "user-1"); !errors.Is(err, domain.ErrEmbeddingModelRequired) {
		t.Fatalf("missing model err = %v", err)
	}
	// repo 错误传播。
	repo.createErr = errors.New("db down")
	if _, err := svc.CreateWorkspace(context.Background(), "t1", CreateWorkspaceInput{Name: "ws", Config: domain.WorkspaceConfig{EmbeddingModel: "text-embedding-v3"}}, "user-1"); err == nil {
		t.Fatal("repo error must propagate")
	}
}

func TestWorkspaceCreateValidatesRerankCatalogue(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	svc, _ := buildWorkspaceService(repo)
	catalogue := &fakeModelExists{
		embedding: map[string]bool{"text-embedding-v3": true},
		rerank:    map[string]bool{"rerank-v3": true},
	}
	svc.SetModelExists(catalogue)

	// 外部 rerank provider 的模型不在目录 → 拒绝。
	if _, err := svc.CreateWorkspace(context.Background(), "t1", CreateWorkspaceInput{
		Name: "ws", Config: domain.WorkspaceConfig{EmbeddingModel: "text-embedding-v3", Reranking: "cohere:unknown"},
	}, "user-1"); !errors.Is(err, domain.ErrInvalidRerankIdentity) {
		t.Fatalf("rerank not in catalogue err = %v", err)
	}
	// 目录查询失败传播（fail-closed，不默认放行）。
	catalogue.err = errors.New("catalogue down")
	if _, err := svc.CreateWorkspace(context.Background(), "t1", CreateWorkspaceInput{Name: "ws", Config: domain.WorkspaceConfig{EmbeddingModel: "text-embedding-v3"}}, "user-1"); err == nil {
		t.Fatal("catalogue error must propagate")
	}
	// 目录含该 rerank 模型 → 创建成功。
	catalogue.err = nil
	if _, err := svc.CreateWorkspace(context.Background(), "t1", CreateWorkspaceInput{
		Name: "ws2", Config: domain.WorkspaceConfig{EmbeddingModel: "text-embedding-v3", Reranking: "cohere:rerank-v3"},
	}, "user-1"); err != nil {
		t.Fatalf("rerank in catalogue create = %v", err)
	}
}

func TestWorkspaceCreateRollsBackOnCollectionFailure(t *testing.T) {
	// 极端情况：collection 创建失败 → DB 记录回滚删除 + 错误。
	repo := newFakeWorkspaceRepo()
	store := newCollectionStub()
	store.createErr = errors.New("milvus down")
	svc, _ := buildWorkspaceService(repo)
	svc.SetVectorStore(store)

	if _, err := svc.CreateWorkspace(context.Background(), "t1", CreateWorkspaceInput{Name: "ws1", Config: domain.WorkspaceConfig{EmbeddingModel: "text-embedding-v3"}}, "user-1"); err == nil {
		t.Fatal("collection failure must error")
	}
	if len(repo.deleted) != 1 || repo.deleted[0] != "ws1" {
		t.Fatalf("rollback deletes = %+v", repo.deleted)
	}
}

func TestWorkspaceList(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	svc, _ := buildWorkspaceService(repo)

	// 极端情况：空列表 → 空 slice 非 nil。
	list, err := svc.ListWorkspaces(context.Background(), "t1")
	if err != nil || len(list) != 0 || list == nil {
		t.Fatalf("empty list = %+v, %v", list, err)
	}

	seedWorkspace(repo, "a")
	seedWorkspace(repo, "b")
	list, err = svc.ListWorkspaces(context.Background(), "t1")
	if err != nil || len(list) != 2 {
		t.Fatalf("list = %d, %v", len(list), err)
	}
	repo.listErr = errors.New("db down")
	if _, err := svc.ListWorkspaces(context.Background(), "t1"); err == nil {
		t.Fatal("repo error must propagate")
	}
}

func TestWorkspaceUpdate(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	svc, _ := buildWorkspaceService(repo)
	seedWorkspace(repo, "ws1")

	newName := "ws-renamed"
	desc := "new desc"
	topK := 7
	ws, err := svc.UpdateWorkspace(context.Background(), "t1", "ws1", UpdateWorkspaceInput{
		Name:        &newName,
		Description: &desc,
		Config:      &domain.WorkspaceConfig{TopK: topK, QueryMode: "vector"},
	}, "user-1")
	if err != nil {
		t.Fatalf("update = %v", err)
	}
	if ws.Name != "ws-renamed" || ws.Description != "new desc" || ws.Config.TopK != 7 || ws.Config.QueryMode != "vector" {
		t.Fatalf("updated = %+v", ws)
	}
	if len(repo.renames) != 1 || repo.renames[0].newName != "ws-renamed" {
		t.Fatalf("renames = %+v", repo.renames)
	}
	// 仓库同步更新。
	if repo.workspaces["ws-renamed"] == nil || repo.workspaces["ws1"] != nil {
		t.Fatal("repo must track rename")
	}
}

func TestWorkspaceUpdateRejections(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	svc, _ := buildWorkspaceService(repo)
	seedWorkspace(repo, "ws1")

	// 极端情况：不存在。
	if _, err := svc.UpdateWorkspace(context.Background(), "t1", "ghost", UpdateWorkspaceInput{}, "user-1"); !errors.Is(err, domain.ErrWorkspaceNotFound) {
		t.Fatalf("ghost err = %v", err)
	}
	// 极端情况：embedding model 不可变。
	if _, err := svc.UpdateWorkspace(context.Background(), "t1", "ws1", UpdateWorkspaceInput{
		Config: &domain.WorkspaceConfig{EmbeddingModel: "embedding-3"},
	}, "user-1"); !errors.Is(err, domain.ErrEmbeddingModelImmutable) {
		t.Fatalf("immutable err = %v", err)
	}
	// 极端情况：非法 query mode。
	if _, err := svc.UpdateWorkspace(context.Background(), "t1", "ws1", UpdateWorkspaceInput{
		Config: &domain.WorkspaceConfig{QueryMode: "bogus"},
	}, "user-1"); !errors.Is(err, domain.ErrInvalidQueryMode) {
		t.Fatalf("invalid mode err = %v", err)
	}
}

func TestWorkspaceGetStats(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	svc, ki := buildWorkspaceService(repo)
	seedWorkspace(repo, "ws1")
	docRepo := newMockDocRepo()
	ki.SetDocRepo(docRepo)
	svc.SetDocRepo(docRepo)

	res, err := svc.GetWorkspaceStats(context.Background(), "t1", "ws1", "user-1")
	if err != nil {
		t.Fatalf("stats = %v", err)
	}
	if res.Name != "ws1" || res.Stats["vector_count"] != int64(0) || res.Stats["doc_count"] != 0 {
		t.Fatalf("stats = %+v", res)
	}

	// 极端情况：vector 统计失败降级为 {error: ...}，不阻断。
	repo.workspaces["ws1"].ID = "wsid-1"
	failing := &vectorStoreFailing{}
	ki.vectorStore = failing
	res, err = svc.GetWorkspaceStats(context.Background(), "t1", "ws1", "user-1")
	if err != nil {
		t.Fatalf("degraded stats = %v", err)
	}
	if _, ok := res.Stats["error"]; !ok {
		t.Fatalf("degraded stats = %+v", res.Stats)
	}
	// 极端情况：doc 统计失败 → 不写 doc_count 键，仍成功。
	svc.SetDocRepo(&docCountErrRepo{mockDocRepo: newMockDocRepo()})
	res, err = svc.GetWorkspaceStats(context.Background(), "t1", "ws1", "user-1")
	if err != nil {
		t.Fatalf("doc-err stats = %v", err)
	}
	if _, ok := res.Stats["doc_count"]; ok {
		t.Fatalf("doc_count must be absent on error, got %+v", res.Stats)
	}
}

// vectorStoreFailing 的 CountVectors/DeleteCollection 失败，其余方法 no-op。
type vectorStoreFailing struct{}

func (v *vectorStoreFailing) CreateCollectionWithDim(context.Context, string, int) error { return nil }

func (v *vectorStoreFailing) Insert(context.Context, string, []port.VectorDocument) error { return nil }

func (v *vectorStoreFailing) Search(context.Context, string, []float32, int) ([]port.VectorSearchResult, error) {
	return nil, nil
}

func (v *vectorStoreFailing) SearchWithFilter(context.Context, string, []float32, int, string) ([]port.VectorSearchResult, error) {
	return nil, nil
}

func (v *vectorStoreFailing) DescribeCollection(context.Context, string) (port.CollectionInfo, error) {
	return port.CollectionInfo{}, nil
}

func (v *vectorStoreFailing) Flush(context.Context, string) error { return nil }

func (v *vectorStoreFailing) DeleteCollection(context.Context, string) error {
	return errors.New("milvus down")
}

func (v *vectorStoreFailing) CountVectors(context.Context, string) (int64, error) {
	return 0, errors.New("milvus down")
}

func (v *vectorStoreFailing) DeleteByDocumentIDs(context.Context, string, []string) error { return nil }

// docCountErrRepo 覆盖 CountByWorkspace 使统计失败。
type docCountErrRepo struct{ *mockDocRepo }

func (d *docCountErrRepo) CountByWorkspace(context.Context, string, string) (int, error) {
	return 0, errors.New("db down")
}

func TestWorkspaceDelete(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	svc, _ := buildWorkspaceService(repo)
	seedWorkspace(repo, "ws1")

	if err := svc.DeleteWorkspace(context.Background(), "t1", "ws1", "user-1"); err != nil {
		t.Fatalf("delete = %v", err)
	}
	if len(repo.deleted) != 1 || repo.deleted[0] != "ws1" {
		t.Fatalf("deleted = %+v", repo.deleted)
	}
	// 极端情况：不存在。
	if err := svc.DeleteWorkspace(context.Background(), "t1", "ghost", "user-1"); !errors.Is(err, domain.ErrWorkspaceNotFound) {
		t.Fatalf("ghost err = %v", err)
	}
}

func TestWorkspaceDeleteStorageFailureBlocksDB(t *testing.T) {
	// 极端情况：清理 storage 失败 → 不删除 DB 行。
	repo := newFakeWorkspaceRepo()
	svc, ki := buildWorkspaceService(repo)
	seedWorkspace(repo, "ws1")
	failing := &vectorStoreFailing{}
	ki.vectorStore = failing
	svc.SetVectorStore(failing)

	if err := svc.DeleteWorkspace(context.Background(), "t1", "ws1", "user-1"); err == nil {
		t.Fatal("storage failure must error")
	}
	if len(repo.deleted) != 0 {
		t.Fatalf("db must not be deleted, got %+v", repo.deleted)
	}
}

func TestWorkspaceGetConfigAndWorkspaceQueries(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	svc, _ := buildWorkspaceService(repo)
	seedWorkspace(repo, "ws1")

	cfg, err := svc.GetConfig(context.Background(), "t1", "ws1")
	if err != nil || cfg.EmbeddingModel != "text-embedding-v3" {
		t.Fatalf("config = %+v, %v", cfg, err)
	}
	if _, err := svc.GetConfig(context.Background(), "t1", "ghost"); !errors.Is(err, domain.ErrWorkspaceNotFound) {
		t.Fatalf("ghost config err = %v", err)
	}
	ws, err := svc.GetWorkspace(context.Background(), "t1", "ws1")
	if err != nil || ws.Name != "ws1" {
		t.Fatalf("workspace = %+v, %v", ws, err)
	}
	if _, err := svc.GetWorkspaceByID(context.Background(), "t1", "wsid-ws1"); err != nil {
		t.Fatalf("by id = %v", err)
	}
	if _, err := svc.GetWorkspaceByID(context.Background(), "t1", "ghost"); !errors.Is(err, domain.ErrWorkspaceNotFound) {
		t.Fatalf("ghost by id = %v", err)
	}
}

func TestWorkspaceListSnapshotDocuments(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	svc, ki := buildWorkspaceService(repo)
	seedWorkspace(repo, "ws1")

	// 极端情况：无 docRepo → 明确错误。
	if _, err := svc.ListSnapshotDocuments(context.Background(), "t1", "wsid-1"); err == nil {
		t.Fatal("missing doc repo must error")
	}

	docRepo := newMockDocRepo()
	docRepo.saved = append(docRepo.saved, &domain.Document{ID: "d1", Source: "s"})
	ki.SetDocRepo(docRepo)
	svc.SetDocRepo(docRepo)
	docs, err := svc.ListSnapshotDocuments(context.Background(), "t1", "wsid-1")
	if err != nil || len(docs) != 1 || docs[0].ID != "d1" {
		t.Fatalf("snapshot docs = %+v, %v", docs, err)
	}
}

func TestWorkspaceListDocuments(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	svc, ki := buildWorkspaceService(repo)
	seedWorkspace(repo, "ws1")

	// 极端情况：无 docRepo → 空 slice 非 nil。
	views, err := svc.ListDocuments(context.Background(), "t1", "ws1", "user-1")
	if err != nil || len(views) != 0 || views == nil {
		t.Fatalf("no repo views = %+v, %v", views, err)
	}
	// 极端情况：workspace 不存在。
	docRepo := newMockDocRepo()
	ki.SetDocRepo(docRepo)
	svc.SetDocRepo(docRepo)
	if _, err := svc.ListDocuments(context.Background(), "t1", "ghost", "user-1"); !errors.Is(err, domain.ErrWorkspaceNotFound) {
		t.Fatalf("ghost err = %v", err)
	}

	// 投影正确。
	started := &domain.Document{
		ID: "d1", Source: "src", ContentHash: "h", IngestStatus: constants.IngestStatusProcessing,
		ProcessedChunks: 1, TotalChunks: 3,
		CreatedAt: timeNow(), IngestStartedAt: timePtr(),
	}
	docRepo.saved = append(docRepo.saved, started)
	views, err = svc.ListDocuments(context.Background(), "t1", "ws1", "user-1")
	if err != nil || len(views) != 1 {
		t.Fatalf("views = %+v, %v", views, err)
	}
	v := views[0]
	if v.ID != "d1" || v.IngestStatus != constants.IngestStatusProcessing || v.TotalChunks != 3 || v.IngestStartedAt == nil {
		t.Fatalf("view = %+v", v)
	}
}

func TestWorkspaceIngestUpload(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	svc, ki := buildWorkspaceService(repo)
	seedWorkspace(repo, "ws1")
	docRepo := newMockDocRepo()
	ki.SetDocRepo(docRepo)
	svc.SetDocRepo(docRepo)

	fh := newUploadFileHeader(t, "test.txt", paragraphInput(3))
	result, err := svc.IngestUpload(context.Background(), "t1", "ws1", fh, "user-1", nil, nil)
	if err != nil {
		t.Fatalf("ingest = %v", err)
	}
	if result.DocumentID == "" || result.Status != constants.IngestStatusProcessing || result.TotalChunks == 0 {
		t.Fatalf("result = %+v", result)
	}
	// 等待 background embed+persist goroutine 完成。
	if err := ki.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown = %v", err)
	}
	if docRepo.savedCount() != 1 {
		t.Fatalf("docs saved = %d", docRepo.savedCount())
	}
}

func TestWorkspaceIngestUploadRejections(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	svc, _ := buildWorkspaceService(repo)
	seedWorkspace(repo, "ws1")

	// 极端情况：workspace 不存在。
	fh := newUploadFileHeader(t, "x.txt", "hello")
	if _, err := svc.IngestUpload(context.Background(), "t1", "ghost", fh, "user-1", nil, nil); !errors.Is(err, domain.ErrWorkspaceNotFound) {
		t.Fatalf("ghost err = %v", err)
	}
	// 极端情况：重复内容 hash → ErrDuplicateDocument。
	docRepo := newMockDocRepo()
	docRepo.existsHash[fmt.Sprintf("%x", sha256.Sum256([]byte("hello")))] = true
	svc.SetDocRepo(docRepo)
	if _, err := svc.IngestUpload(context.Background(), "t1", "ws1", fh, "user-1", nil, nil); !errors.Is(err, domain.ErrDuplicateDocument) {
		t.Fatalf("duplicate err = %v", err)
	}
}

func TestWorkspaceDeleteDocument(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	svc, ki := buildWorkspaceService(repo)
	seedWorkspace(repo, "ws1")
	docRepo := newMockDocRepo()
	ki.SetDocRepo(docRepo)
	svc.SetDocRepo(docRepo)
	store := newCollectionStub()
	svc.SetVectorStore(store)

	// 极端情况：storage 未配置。
	svc2, _ := buildWorkspaceService(repo)
	if err := svc2.DeleteDocument(context.Background(), "t1", "ws1", "d1", "user-1"); err == nil {
		t.Fatal("unconfigured storage must error")
	}

	// 极端情况：processing 文档拒绝删除。
	docRepo.saved = append(docRepo.saved, &domain.Document{
		ID: "d-processing", IngestStatus: constants.IngestStatusProcessing,
	})
	if err := svc.DeleteDocument(context.Background(), "t1", "ws1", "d-processing", "user-1"); !errors.Is(err, domain.ErrDocumentProcessing) {
		t.Fatalf("processing err = %v", err)
	}

	// 成功路径。
	docRepo.saved = append(docRepo.saved, &domain.Document{
		ID: "d1", IngestStatus: constants.IngestStatusCompleted,
	})
	if err := svc.DeleteDocument(context.Background(), "t1", "ws1", "d1", "user-1"); err != nil {
		t.Fatalf("delete = %v", err)
	}
	if len(store.deletedByDoc[constants.CollectionName("t1", "wsid-ws1", "text-embedding-v3")]) != 1 {
		t.Fatalf("vector deletes = %+v", store.deletedByDoc)
	}
}

func TestWorkspaceDeleteDocumentNotFound(t *testing.T) {
	repo := newFakeWorkspaceRepo()
	svc, ki := buildWorkspaceService(repo)
	seedWorkspace(repo, "ws1")
	docRepo := newMockDocRepo()
	ki.SetDocRepo(docRepo)
	svc.SetDocRepo(docRepo)
	svc.SetVectorStore(newCollectionStub())

	// 极端情况：文档不存在。
	if err := svc.DeleteDocument(context.Background(), "t1", "ws1", "ghost", "user-1"); !errors.Is(err, domain.ErrDocumentNotFound) {
		t.Fatalf("not found err = %v", err)
	}
	// 极端情况：workspace 不存在。
	if err := svc.DeleteDocument(context.Background(), "t1", "ghost", "d1", "user-1"); !errors.Is(err, domain.ErrWorkspaceNotFound) {
		t.Fatalf("ghost ws err = %v", err)
	}
}

// newUploadFileHeader 用 multipart 协议构造 *multipart.FileHeader。
func newUploadFileHeader(t *testing.T, filename, content string) *multipart.FileHeader {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("create form file = %v", err)
	}
	if _, err := fw.Write([]byte(content)); err != nil {
		t.Fatalf("write = %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close = %v", err)
	}
	r := multipart.NewReader(&buf, w.Boundary())
	form, err := r.ReadForm(1 << 20)
	if err != nil {
		t.Fatalf("read form = %v", err)
	}
	files, ok := form.File["file"]
	if !ok || len(files) == 0 {
		t.Fatal("no file part")
	}
	return files[0]
}

func timeNow() (t time.Time) { return time.Now() }

func timePtr() *time.Time {
	t := time.Now()
	return &t
}

// stubTenantRole resolves every actor as a fixed role so ownership tests
// control authorization via the fake, not tenant membership.
type stubTenantRole struct{ role string }

func (s stubTenantRole) ResolveTenantRole(_ context.Context, _, _ string) (string, error) {
	return s.role, nil
}

func TestValidateModelsInCatalogueChatModels(t *testing.T) {
	base := domain.WorkspaceConfig{EmbeddingModel: "text-embedding-v3"}
	catalogue := &fakeModelExists{
		embedding: map[string]bool{"text-embedding-v3": true},
		chat:      map[string]bool{"qwen-turbo": true},
	}
	svc := &WorkspaceService{modelExists: catalogue, logger: zap.NewNop()}

	t.Run("rerank_model 不在 chat 目录拒绝", func(t *testing.T) {
		cfg := base
		cfg.Reranking = RerankIdentityBuiltin
		cfg.RerankModel = "qwen-max"
		if err := svc.validateModelsInCatalogue(context.Background(), cfg); !errors.Is(err, domain.ErrInvalidRerankModel) {
			t.Fatalf("err = %v, want ErrInvalidRerankModel", err)
		}
	})
	t.Run("judge_model 不在 chat 目录拒绝", func(t *testing.T) {
		cfg := base
		cfg.JudgeModel = "qwen-max"
		if err := svc.validateModelsInCatalogue(context.Background(), cfg); !errors.Is(err, domain.ErrInvalidJudgeModel) {
			t.Fatalf("err = %v, want ErrInvalidJudgeModel", err)
		}
	})
	t.Run("chat 目录模型通过", func(t *testing.T) {
		cfg := base
		cfg.Reranking = RerankIdentityBuiltin
		cfg.RerankModel = "qwen-turbo"
		cfg.JudgeModel = "qwen-turbo"
		if err := svc.validateModelsInCatalogue(context.Background(), cfg); err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
	})
	t.Run("休眠 rerank_model 不校验（reranking 非 builtin）", func(t *testing.T) {
		cfg := base
		cfg.Reranking = ""
		cfg.RerankModel = "qwen-max"
		if err := svc.validateModelsInCatalogue(context.Background(), cfg); err != nil {
			t.Fatalf("err = %v, want nil（休眠 rerank_model 不参与目录校验）", err)
		}
	})
	t.Run("目录查询失败传播（5xx 而非 400）", func(t *testing.T) {
		svc.modelExists = &fakeModelExists{embedding: map[string]bool{"text-embedding-v3": true}, err: errors.New("db down")}
		cfg := base
		cfg.JudgeModel = "qwen-turbo"
		err := svc.validateModelsInCatalogue(context.Background(), cfg)
		if err == nil || errors.Is(err, domain.ErrInvalidJudgeModel) {
			t.Fatalf("err = %v, want wrapped db error, not ErrInvalidJudgeModel", err)
		}
	})
	t.Run("builtin 空 rerank_model 在 modelExists 为 nil 时也拒绝", func(t *testing.T) {
		svc.modelExists = nil
		cfg := base
		cfg.Reranking = "builtin-score-v1"
		if err := svc.validateModelsInCatalogue(context.Background(), cfg); !errors.Is(err, domain.ErrRerankModelRequired) {
			t.Fatalf("err = %v, want ErrRerankModelRequired", err)
		}
	})
}
