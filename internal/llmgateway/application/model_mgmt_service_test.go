package application

import (
	"context"
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/internal/llmgateway/domain/port"
)

type modelMgmtRepo struct {
	model        domain.Model
	models       []domain.Model
	tenant       string
	err          error
	defaultCalls []modelDefaultCall
}

// modelDefaultCall records one SetDefaultEmbedding repo invocation.
type modelDefaultCall struct {
	tenantID string
	id       string
	enabled  bool
}

func (r *modelMgmtRepo) Create(context.Context, string, *domain.Model) error { return r.err }
func (r *modelMgmtRepo) Get(_ context.Context, tenantID string, id string) (*domain.Model, error) {
	// tenant 字段设置后按租户过滤：外部租户视为未命中，返回空结果（沿用
	// fallback 的 nil-error 约定），由服务层 fail-closed 校验拒绝；repo 层
	// 的租户所有权过滤（Task 1）才是深层保证。
	if r.tenant != "" && tenantID != r.tenant {
		empty := domain.Model{}
		return &empty, r.err
	}
	for i := range r.models {
		if r.models[i].ID == id {
			m := r.models[i]
			return &m, r.err
		}
	}
	model := r.model
	return &model, r.err
}
func (r *modelMgmtRepo) List(context.Context, string, port.ModelFilter) ([]domain.Model, error) {
	return nil, r.err
}
func (r *modelMgmtRepo) Update(context.Context, string, *domain.Model) error { return r.err }
func (r *modelMgmtRepo) UpsertDiscovered(
	context.Context, string, string, []domain.Model,
) ([]domain.Model, error) {
	return nil, r.err
}
func (r *modelMgmtRepo) Delete(context.Context, string, string) error       { return r.err }
func (r *modelMgmtRepo) Toggle(context.Context, string, string, bool) error { return r.err }
func (r *modelMgmtRepo) SetDefaultEmbedding(_ context.Context, tenantID, id string, enabled bool) error {
	r.defaultCalls = append(r.defaultCalls, modelDefaultCall{tenantID: tenantID, id: id, enabled: enabled})
	return r.err
}

type recordingInvalidator struct {
	tenants []string
}

func (i *recordingInvalidator) Invalidate(tenantID string) {
	i.tenants = append(i.tenants, tenantID)
}

// invalidatorFunc adapts a plain func to ModelCacheInvalidator for concise test setup.
type invalidatorFunc func(tenantID string)

func (f invalidatorFunc) Invalidate(tenantID string) { f(tenantID) }

func TestModelMgmtServiceInvalidatesRegistryAfterSuccessfulMutation(t *testing.T) {
	invalidator := &recordingInvalidator{}
	svc := NewModelMgmtService(&modelMgmtRepo{}, invalidator)

	if err := svc.Toggle(context.Background(), "tenant-1", "model-1", false); err != nil {
		t.Fatalf("Toggle: %v", err)
	}
	if len(invalidator.tenants) != 1 || invalidator.tenants[0] != "tenant-1" {
		t.Fatalf("invalidations = %v", invalidator.tenants)
	}
}

func TestModelMgmtServiceDoesNotInvalidateAfterFailedMutation(t *testing.T) {
	invalidator := &recordingInvalidator{}
	svc := NewModelMgmtService(&modelMgmtRepo{err: errors.New("write failed")}, invalidator)

	if err := svc.Toggle(context.Background(), "tenant-1", "model-1", false); err == nil {
		t.Fatal("Toggle must return the repository error")
	}
	if len(invalidator.tenants) != 0 {
		t.Fatalf("invalidations = %v, want none", invalidator.tenants)
	}
}

func TestModelMgmtServiceSetDefaultEmbedding(t *testing.T) {
	t.Run("rejects non-embedding model when enabling", func(t *testing.T) {
		repo := &modelMgmtRepo{models: []domain.Model{
			{ID: "m1", Name: "chat-x", Capabilities: []domain.ModelCapability{domain.CapChat}, Enabled: true},
		}}
		svc := NewModelMgmtService(repo, invalidatorFunc(func(tenantID string) {
			t.Fatal("must not invalidate on rejected set")
		}))
		if err := svc.SetDefaultEmbedding(context.Background(), "t1", "m1", true); err == nil {
			t.Fatal("expected error for non-embedding model")
		}
	})
	t.Run("rejects disabled model when enabling", func(t *testing.T) {
		repo := &modelMgmtRepo{models: []domain.Model{
			{ID: "m1", Name: "embed-x", Capabilities: []domain.ModelCapability{domain.CapEmbedding}, Enabled: false},
		}}
		svc := NewModelMgmtService(repo)
		if err := svc.SetDefaultEmbedding(context.Background(), "t1", "m1", true); err == nil {
			t.Fatal("expected error for disabled model")
		}
	})
	t.Run("invalidates registry after successful set", func(t *testing.T) {
		repo := &modelMgmtRepo{models: []domain.Model{
			{ID: "m1", Name: "embed-x", Capabilities: []domain.ModelCapability{domain.CapEmbedding}, Enabled: true},
		}}
		invalidated := false
		svc := NewModelMgmtService(repo, invalidatorFunc(func(tenantID string) { invalidated = true }))
		if err := svc.SetDefaultEmbedding(context.Background(), "t1", "m1", true); err != nil {
			t.Fatal(err)
		}
		if !invalidated {
			t.Fatal("expected registry invalidation")
		}
	})
	t.Run("clears default embedding when disabling", func(t *testing.T) {
		repo := &modelMgmtRepo{models: []domain.Model{
			{ID: "m1", Name: "embed-x", Capabilities: []domain.ModelCapability{domain.CapEmbedding}, Enabled: true},
		}}
		invalidated := false
		svc := NewModelMgmtService(repo, invalidatorFunc(func(tenantID string) { invalidated = true }))
		if err := svc.SetDefaultEmbedding(context.Background(), "t1", "m1", false); err != nil {
			t.Fatal(err)
		}
		if len(repo.defaultCalls) != 1 || repo.defaultCalls[0].id != "m1" || repo.defaultCalls[0].enabled {
			t.Fatalf("SetDefaultEmbedding calls = %+v, want one clear for m1", repo.defaultCalls)
		}
		if !invalidated {
			t.Fatal("expected registry invalidation on clear")
		}
	})
	t.Run("rejects model from another tenant when enabling", func(t *testing.T) {
		repo := &modelMgmtRepo{tenant: "t1", models: []domain.Model{
			{ID: "m1", Name: "embed-x", Capabilities: []domain.ModelCapability{domain.CapEmbedding}, Enabled: true},
		}}
		svc := NewModelMgmtService(repo)
		err := svc.SetDefaultEmbedding(context.Background(), "t2", "m1", true)
		if err == nil {
			t.Fatal("expected error for foreign tenant model")
		}
		if !errors.Is(err, domain.ErrModelNotEmbeddingEnabled) {
			t.Fatalf("err = %v, want ErrModelNotEmbeddingEnabled", err)
		}
		if len(repo.defaultCalls) != 0 {
			t.Fatalf("must not mutate for foreign tenant, calls = %+v", repo.defaultCalls)
		}
	})
}
