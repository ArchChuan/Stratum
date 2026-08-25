package application

// Document-level ACL tests (P0.5 读路径 + P0.6 access 管理):
//   - ListDocuments 可见集过滤 + ACL echo 门
//   - IngestUpload 白名单/创建者落库
//   - DeleteDocument 鉴权矩阵(fail closed)
//   - SetDocAccess 拒绝路径 + roleIDs 归一化
//   - GetWorkspaceStats doc_count 可见集统计 + vector_count member 隐藏

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/internal/knowledge/domain"
	"github.com/byteBuilderX/stratum/internal/knowledge/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// aclDocRepo 是 SetDocAccess 用例的 doc stub:GetByID 按 docs 查找,SetDocAccess
// 记录入参,其余方法继承 deleteDocRepo 的 no-op 语义。
type aclDocRepo struct {
	deleteDocRepo
	getErr   error
	gotUsers []string
	gotRoles []string
}

func (r *aclDocRepo) GetByID(_ context.Context, _, _, docID string) (*domain.Document, error) {
	if r.getErr != nil {
		return nil, r.getErr
	}
	for _, d := range r.docs {
		if d.ID == docID {
			return d, nil
		}
	}
	return nil, domain.ErrDocumentNotFound
}

func (r *aclDocRepo) SetDocAccess(_ context.Context, _, _ string, users, roles []string) error {
	r.gotUsers = append([]string(nil), users...)
	r.gotRoles = append([]string(nil), roles...)
	return nil
}

// fixedCountVectorStore 固定返回 CountVectors 计数,用于验证 vector_count 的
// member 隐藏门(默认 mock 恒为 0,无法证明门生效)。
type fixedCountVectorStore struct {
	count int64
}

func (v *fixedCountVectorStore) CreateCollectionWithDim(context.Context, string, int) error {
	return nil
}
func (v *fixedCountVectorStore) Insert(context.Context, string, []port.VectorDocument) error {
	return nil
}
func (v *fixedCountVectorStore) Search(context.Context, string, []float32, int) ([]port.VectorSearchResult, error) {
	return nil, nil
}
func (v *fixedCountVectorStore) SearchWithFilter(
	context.Context, string, []float32, int, string,
) ([]port.VectorSearchResult, error) {
	return nil, nil
}
func (v *fixedCountVectorStore) DescribeCollection(context.Context, string) (port.CollectionInfo, error) {
	return port.CollectionInfo{}, nil
}
func (v *fixedCountVectorStore) Flush(context.Context, string) error { return nil }
func (v *fixedCountVectorStore) DeleteCollection(context.Context, string) error {
	return nil
}
func (v *fixedCountVectorStore) CountVectors(context.Context, string) (int64, error) {
	return v.count, nil
}

func TestListDocuments_ACLMatrix(t *testing.T) {
	docs := []*domain.Document{
		{ID: "d1", Source: "a.txt", IngestStatus: constants.IngestStatusCompleted,
			AllowedUserIDs: []string{"u1"}},
		{ID: "d2", Source: "b.txt", IngestStatus: constants.IngestStatusCompleted,
			CreatedBy: "creator-1"},
	}
	cases := []struct {
		name           string
		ws             *domain.Workspace
		role           string
		useRoles       bool
		resolveFail    bool
		viewerID       string
		visible        []string
		wantErr        error
		wantCount      int
		wantEcho       bool
		wantQuery      bool
		wantRestricted []bool
	}{
		{
			name: "tenant admin sees all docs and ACL is echoed",
			ws:   &domain.Workspace{ID: "ws-1", Name: "docs"},
			role: "admin", useRoles: true, viewerID: "viewer-1",
			visible:   []string{"d1", "d2"},
			wantCount: 2, wantEcho: true,
		},
		{
			name: "workspace creator sees all docs and ACL is echoed",
			ws:   &domain.Workspace{ID: "ws-1", Name: "docs", CreatedBy: "viewer-1"},
			role: "member", useRoles: true, viewerID: "viewer-1",
			visible:   []string{"d1", "d2"},
			wantCount: 2, wantEcho: true,
		},
		{
			name: "member sees all doc metadata with the invisible one flagged restricted",
			ws:   &domain.Workspace{ID: "ws-1", Name: "docs"},
			role: "member", useRoles: true, viewerID: "viewer-1",
			visible:   []string{"d1"},
			wantCount: 2, wantEcho: false, wantQuery: true,
			wantRestricted: []bool{false, true},
		},
		{
			name: "empty visible set flags every doc restricted without error",
			ws:   &domain.Workspace{ID: "ws-1", Name: "docs"},
			role: "member", useRoles: true, viewerID: "viewer-1",
			wantCount: 2, wantEcho: false, wantQuery: true,
			wantRestricted: []bool{true, true},
		},
		{
			name:     "missing role resolver fails closed",
			ws:       &domain.Workspace{ID: "ws-1", Name: "docs"},
			viewerID: "viewer-1",
			wantErr:  domain.ErrForbidden,
		},
		{
			name: "role resolution failure fails closed",
			ws:   &domain.Workspace{ID: "ws-1", Name: "docs"},
			role: "member", useRoles: true, resolveFail: true, viewerID: "viewer-1",
			wantErr: domain.ErrForbidden,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recordingDocRepo{ids: tc.visible}
			rec.docs = docs
			s := NewWorkspaceService(&deleteWorkspaceRepo{workspace: tc.ws}, nil, zap.NewNop())
			s.SetDocRepo(rec)
			switch {
			case tc.resolveFail:
				s.SetTenantRoleResolver(failingTenantRole{})
			case tc.useRoles:
				s.SetTenantRoleResolver(stubTenantRole{role: tc.role})
			}

			views, err := s.ListDocuments(context.Background(), "t1", tc.ws.Name, tc.viewerID)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("ListDocuments err = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr != nil {
				return
			}
			require.Len(t, views, tc.wantCount)
			if tc.wantQuery {
				require.Equal(t, tc.viewerID, rec.gotViewerID)
			}
			if len(views) == 0 {
				return
			}
			if tc.wantEcho {
				require.Equal(t, []string{"u1"}, views[0].AllowedUserIDs)
			} else {
				require.Empty(t, views[0].AllowedUserIDs)
				require.Empty(t, views[0].AllowedRoleIDs)
				require.Empty(t, views[0].CreatedBy)
			}
			if tc.wantRestricted != nil {
				got := make([]bool, len(views))
				for i, v := range views {
					got[i] = v.Restricted
				}
				require.Equal(t, tc.wantRestricted, got)
			}
		})
	}
}

func TestIngestUpload_AccessWhitelistPersisted(t *testing.T) {
	t.Run("whitelist and creator are persisted on the document row", func(t *testing.T) {
		repo := newFakeWorkspaceRepo()
		svc, ki := buildWorkspaceService(repo)
		seedWorkspace(repo, "ws1")
		docRepo := newMockDocRepo()
		ki.SetDocRepo(docRepo)
		svc.SetDocRepo(docRepo)

		fh := newUploadFileHeader(t, "acl.txt", "acl content")
		_, err := svc.IngestUpload(context.Background(), "t1", "ws1", fh, "actor-1",
			[]string{"u1", "u2"}, []string{"admin", "member"})
		require.NoError(t, err)
		// 等待 background embed+persist goroutine 完成。
		require.NoError(t, ki.Shutdown(context.Background()))
		require.Equal(t, 1, docRepo.savedCount())
		doc := docRepo.saved[0]
		require.Equal(t, []string{"u1", "u2"}, doc.AllowedUserIDs)
		require.Equal(t, []string{"admin", "member"}, doc.AllowedRoleIDs)
		require.Equal(t, "actor-1", doc.CreatedBy)
	})
}

func TestDeleteDocument_OwnershipMatrix(t *testing.T) {
	cases := []struct {
		name        string
		wsCreatedBy string
		role        string
		resolveFail bool
		actorID     string
		wantErr     error
	}{
		{name: "tenant owner may delete any document", wsCreatedBy: "", role: "owner", actorID: "u1"},
		{name: "admin may delete own document", wsCreatedBy: "u1", role: "admin", actorID: "u1"},
		{name: "admin cannot delete a foreign document", wsCreatedBy: "other", role: "admin", actorID: "u1",
			wantErr: domain.ErrForbidden},
		{name: "member is denied even for own document", wsCreatedBy: "u1", role: "member", actorID: "u1",
			wantErr: domain.ErrForbidden},
		{name: "role resolution failure fails closed", resolveFail: true, actorID: "u1",
			wantErr: domain.ErrForbidden},
		{name: "empty actor fails closed", role: "owner", actorID: "",
			wantErr: domain.ErrForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ws := &domain.Workspace{ID: "ws-1", Name: "docs", CreatedBy: tc.wsCreatedBy}
			docs := &deleteDocRepo{docs: []*domain.Document{
				{ID: "d1", IngestStatus: constants.IngestStatusCompleted}}}
			s := NewWorkspaceService(&deleteWorkspaceRepo{workspace: ws}, nil, zap.NewNop())
			s.SetDocRepo(docs)
			s.SetVectorStore(&deleteVectorStore{})
			switch {
			case tc.resolveFail:
				s.SetTenantRoleResolver(failingTenantRole{})
			case tc.role != "":
				s.SetTenantRoleResolver(stubTenantRole{role: tc.role})
			}

			err := s.DeleteDocument(context.Background(), "t1", "docs", "d1", tc.actorID)
			require.ErrorIs(t, err, tc.wantErr)
		})
	}
}

func TestSetDocAccess(t *testing.T) {
	ws := &domain.Workspace{ID: "ws-1", Name: "docs", CreatedBy: "u1"}
	newService := func(ws *domain.Workspace, docs *aclDocRepo) *WorkspaceService {
		s := NewWorkspaceService(&deleteWorkspaceRepo{workspace: ws}, nil, zap.NewNop())
		s.SetDocRepo(docs)
		return s
	}

	t.Run("tenant owner replaces the whitelist with normalized roles", func(t *testing.T) {
		docs := &aclDocRepo{deleteDocRepo: deleteDocRepo{docs: []*domain.Document{{ID: "d1"}}}}
		s := newService(ws, docs)
		s.SetTenantRoleResolver(stubTenantRole{role: "owner"})

		err := s.SetDocAccess(context.Background(), "t1", "docs", "d1", "u1",
			[]string{"u9"}, []string{" ADMIN ", "member", "", "  "})
		require.NoError(t, err)
		require.Equal(t, []string{"u9"}, docs.gotUsers)
		require.Equal(t, []string{"admin", "member"}, docs.gotRoles)
	})

	t.Run("non-owner is denied before any repo write", func(t *testing.T) {
		docs := &aclDocRepo{deleteDocRepo: deleteDocRepo{docs: []*domain.Document{{ID: "d1"}}}}
		s := newService(ws, docs)
		s.SetTenantRoleResolver(stubTenantRole{role: "member"})

		err := s.SetDocAccess(context.Background(), "t1", "docs", "d1", "intruder", nil, nil)
		require.ErrorIs(t, err, domain.ErrForbidden)
		require.Nil(t, docs.gotUsers)
	})

	t.Run("missing role resolver fails closed", func(t *testing.T) {
		docs := &aclDocRepo{deleteDocRepo: deleteDocRepo{docs: []*domain.Document{{ID: "d1"}}}}
		s := newService(ws, docs)

		err := s.SetDocAccess(context.Background(), "t1", "docs", "d1", "u1", nil, nil)
		require.ErrorIs(t, err, domain.ErrForbidden)
	})

	t.Run("missing document passes through not found", func(t *testing.T) {
		s := newService(ws, &aclDocRepo{})
		s.SetTenantRoleResolver(stubTenantRole{role: "owner"})

		err := s.SetDocAccess(context.Background(), "t1", "docs", "ghost", "u1", nil, nil)
		require.ErrorIs(t, err, domain.ErrDocumentNotFound)
	})

	t.Run("document lookup failure passes through", func(t *testing.T) {
		boom := errors.New("db down")
		s := newService(ws, &aclDocRepo{getErr: boom})
		s.SetTenantRoleResolver(stubTenantRole{role: "owner"})

		err := s.SetDocAccess(context.Background(), "t1", "docs", "d1", "u1", nil, nil)
		require.ErrorIs(t, err, boom)
	})
}

func TestGetWorkspaceStats_VisibilityGates(t *testing.T) {
	docs := []*domain.Document{{ID: "d1"}, {ID: "d2"}}
	newStatsService := func(role string, visible []string) (*WorkspaceService, *KnowledgeIngest) {
		repo := newFakeWorkspaceRepo()
		svc, ki := buildWorkspaceService(repo)
		seedWorkspace(repo, "ws1")
		rec := &recordingDocRepo{ids: visible}
		rec.docs = docs
		svc.SetDocRepo(rec)
		svc.SetTenantRoleResolver(stubTenantRole{role: role})
		ki.vectorStore = &fixedCountVectorStore{count: 42}
		return svc, ki
	}

	t.Run("member sees visible doc_count and zeroed vector_count", func(t *testing.T) {
		svc, _ := newStatsService("member", []string{"d1"})
		res, err := svc.GetWorkspaceStats(context.Background(), "t1", "ws1", "viewer-1")
		require.NoError(t, err)
		require.Equal(t, 1, res.Stats["doc_count"])
		require.Equal(t, 0, res.Stats["vector_count"])
	})

	t.Run("admin sees full doc_count and the real vector_count", func(t *testing.T) {
		svc, _ := newStatsService("owner", []string{"d1"})
		res, err := svc.GetWorkspaceStats(context.Background(), "t1", "ws1", "viewer-1")
		require.NoError(t, err)
		require.Equal(t, 2, res.Stats["doc_count"])
		require.Equal(t, int64(42), res.Stats["vector_count"])
	})
}
