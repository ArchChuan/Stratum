package application

import (
	"context"
	"errors"
	"strings"
	"testing"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/internal/llmgateway/domain/port"
)

func TestModelMgmtListGet(t *testing.T) {
	repo := &modelMgmtRepo{model: domain.Model{ID: "m1", Name: "deepseek-v4"}}
	svc := NewModelMgmtService(repo)

	// 极端情况：repo 错误传播。
	repo.err = errors.New("db down")
	if _, err := svc.List(context.Background(), "t1", port.ModelFilter{}); err == nil {
		t.Fatal("list repo error must propagate")
	}
	if _, err := svc.Get(context.Background(), "t1", "m1"); err == nil {
		t.Fatal("get repo error must propagate")
	}

	repo.err = nil
	models, err := svc.List(context.Background(), "t1", port.ModelFilter{})
	if err != nil {
		t.Fatalf("list = %v", err)
	}
	if len(models) != 0 {
		t.Fatalf("models = %+v", models)
	}
	m, err := svc.Get(context.Background(), "t1", "m1")
	if err != nil || m.ID != "m1" {
		t.Fatalf("get = %+v, %v", m, err)
	}
}

func TestModelMgmtUpdate(t *testing.T) {
	inv := &recordingInvalidator{}
	repo := &modelMgmtRepo{model: domain.Model{ID: "m1", ContextWindow: 128, MaxTokens: 64}}
	svc := NewModelMgmtService(repo, inv)

	m, err := svc.Update(context.Background(), "t1", "u1", UpdateModelInput{
		ID: "m1", DisplayName: "DeepSeek V4", ContextWindow: 128, MaxTokens: 64,
		InputPrice: 1.5, OutputPrice: 3.0, Recommended: true,
		Capabilities: []domain.ModelCapability{domain.CapChat},
	})
	if err != nil {
		t.Fatalf("update = %v", err)
	}
	if m.DisplayName != "DeepSeek V4" || m.ContextWindow != 128 || m.MaxTokens != 64 ||
		m.InputPrice != 1.5 || m.OutputPrice != 3.0 || !m.Recommended ||
		len(m.Capabilities) != 1 {
		t.Fatalf("updated = %+v", m)
	}
	if inv.calls != 1 {
		t.Fatalf("invalidations = %d, want 1", inv.calls)
	}
}

func TestModelMgmtUpdateRejectsObservedCapabilityMutation(t *testing.T) {
	repo := &modelMgmtRepo{model: domain.Model{ID: "m1", ContextWindow: 128, MaxTokens: 64}}
	svc := NewModelMgmtService(repo)
	_, err := svc.Update(context.Background(), "t1", "u1", UpdateModelInput{
		ID: "m1", ContextWindow: 256, MaxTokens: 64,
	})
	if err == nil || !strings.Contains(err.Error(), "discovery-managed") {
		t.Fatalf("Update error = %v, want discovery-managed rejection", err)
	}
}

func TestModelMgmtUpdateErrors(t *testing.T) {
	inv := &recordingInvalidator{}
	repo := &modelMgmtRepo{err: errors.New("db down")}
	svc := NewModelMgmtService(repo, inv)

	// 极端情况：Get 失败 → 包装错误，不 invalidate。
	if _, err := svc.Update(context.Background(), "t1", "u1", UpdateModelInput{ID: "m1"}); err == nil {
		t.Fatal("get failure must error")
	}
	if inv.calls != 0 {
		t.Fatalf("must not invalidate on get failure, got %d", inv.calls)
	}

	// 极端情况：Update 失败 → 包装错误，不 invalidate。
	svc2 := NewModelMgmtService(&failUpdateRepo{modelMgmtRepo: modelMgmtRepo{model: domain.Model{ID: "m1"}}}, inv)
	if _, err := svc2.Update(context.Background(), "t1", "u1", UpdateModelInput{ID: "m1"}); err == nil {
		t.Fatal("update failure must error")
	}
	if inv.calls != 0 {
		t.Fatalf("must not invalidate on update failure, got %d", inv.calls)
	}
}

func TestModelMgmtDelete(t *testing.T) {
	inv := &recordingInvalidator{}
	repo := &modelMgmtRepo{}
	svc := NewModelMgmtService(repo, inv)

	// 极端情况：无 invalidator 不 panic。
	svcNoInv := NewModelMgmtService(repo)
	if err := svcNoInv.Delete(context.Background(), "t1", "m1"); err != nil {
		t.Fatalf("delete = %v", err)
	}

	if err := svc.Delete(context.Background(), "t1", "m1"); err != nil {
		t.Fatalf("delete = %v", err)
	}
	if inv.calls != 1 {
		t.Fatalf("invalidations = %d, want 1", inv.calls)
	}
	// 极端情况：repo 失败 → 包装错误。
	repo.err = errors.New("db down")
	if err := svc.Delete(context.Background(), "t1", "m1"); err == nil {
		t.Fatal("delete failure must error")
	}
}

// failUpdateRepo 让 Update 可独立失败。
type failUpdateRepo struct {
	modelMgmtRepo
}

func (r *failUpdateRepo) Get(context.Context, string) (*domain.Model, error) {
	return &r.model, nil
}

func (r *failUpdateRepo) Update(context.Context, *domain.Model, string, *auditdomain.ResourceChangeAuditEvent) error {
	return errors.New("update boom")
}

// fakeCatalog 实现 port.ModelCatalog，返回可脚本化的模型列表。
type fakeCatalog struct {
	chat      []string
	chatErr   error
	embedding []string
	embedErr  error
}

func (f *fakeCatalog) ListChatModels() []string      { return f.chat }
func (f *fakeCatalog) ListEmbeddingModels() []string { return f.embedding }
func (f *fakeCatalog) ListChatModelsByTenant(context.Context) ([]string, error) {
	return f.chat, f.chatErr
}
func (f *fakeCatalog) ListEmbeddingModelsByTenant(context.Context) ([]string, error) {
	return f.embedding, f.embedErr
}

func TestModelServiceCatalogueWithTenant(t *testing.T) {
	catalog := &fakeCatalog{chat: []string{"deepseek-v4"}, embedding: []string{"embedding-3"}}
	svc := NewModelService(catalog)

	chat, embedding := svc.CatalogueWithTenant(context.Background(), "t1")
	if len(chat) != 1 || chat[0] != "deepseek-v4" || len(embedding) != 1 || embedding[0] != "embedding-3" {
		t.Fatalf("catalogue = %+v, %+v", chat, embedding)
	}

	// 极端情况：错误 → 空 slice 非 nil。
	catalog.chatErr = errors.New("db down")
	chat, _ = svc.CatalogueWithTenant(context.Background(), "t1")
	if len(chat) != 0 || chat == nil {
		t.Fatalf("chat on error = %+v", chat)
	}
	// 极端情况：nil 列表 → 空 slice 非 nil。
	catalog.chatErr, catalog.embedErr = nil, nil
	catalog.chat, catalog.embedding = nil, nil
	chat, embedding = svc.CatalogueWithTenant(context.Background(), "t1")
	if chat == nil || embedding == nil || len(chat) != 0 || len(embedding) != 0 {
		t.Fatalf("nil catalogue = %+v, %+v", chat, embedding)
	}
}
