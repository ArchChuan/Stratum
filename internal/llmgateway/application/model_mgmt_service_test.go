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
	err          error
	defaultCalls []modelDefaultCall
}

// modelDefaultCall records one SetDefaultEmbedding repo invocation.
type modelDefaultCall struct {
	id      string
	enabled bool
}

func (r *modelMgmtRepo) Create(context.Context, *domain.Model) error { return r.err }
func (r *modelMgmtRepo) Get(_ context.Context, id string) (*domain.Model, error) {
	for i := range r.models {
		if r.models[i].ID == id {
			m := r.models[i]
			return &m, r.err
		}
	}
	model := r.model
	return &model, r.err
}
func (r *modelMgmtRepo) List(context.Context, port.ModelFilter) ([]domain.Model, error) {
	return nil, r.err
}
func (r *modelMgmtRepo) Update(context.Context, *domain.Model) error { return r.err }
func (r *modelMgmtRepo) UpsertDiscovered(
	context.Context, string, []domain.Model,
) ([]domain.Model, error) {
	return nil, r.err
}
func (r *modelMgmtRepo) Delete(context.Context, string) error       { return r.err }
func (r *modelMgmtRepo) Toggle(context.Context, string, bool) error { return r.err }
func (r *modelMgmtRepo) SetDefaultEmbedding(_ context.Context, id string, enabled bool) error {
	r.defaultCalls = append(r.defaultCalls, modelDefaultCall{id: id, enabled: enabled})
	return r.err
}

type recordingInvalidator struct {
	calls int
}

func (i *recordingInvalidator) Invalidate() {
	i.calls++
}

// invalidatorFunc adapts a plain func to ModelCacheInvalidator for concise test setup.
type invalidatorFunc func()

func (f invalidatorFunc) Invalidate() { f() }

func TestModelMgmtServiceInvalidatesRegistryAfterSuccessfulMutation(t *testing.T) {
	invalidator := &recordingInvalidator{}
	svc := NewModelMgmtService(&modelMgmtRepo{}, invalidator)

	if err := svc.Toggle(context.Background(), "tenant-1", "model-1", false); err != nil {
		t.Fatalf("Toggle: %v", err)
	}
	if invalidator.calls != 1 {
		t.Fatalf("invalidations = %d, want 1", invalidator.calls)
	}
}

func TestModelMgmtServiceDoesNotInvalidateAfterFailedMutation(t *testing.T) {
	invalidator := &recordingInvalidator{}
	svc := NewModelMgmtService(&modelMgmtRepo{err: errors.New("write failed")}, invalidator)

	if err := svc.Toggle(context.Background(), "tenant-1", "model-1", false); err == nil {
		t.Fatal("Toggle must return the repository error")
	}
	if invalidator.calls != 0 {
		t.Fatalf("invalidations = %d, want none", invalidator.calls)
	}
}

func TestModelMgmtServiceSetDefaultEmbedding(t *testing.T) {
	t.Run("rejects non-embedding model when enabling", func(t *testing.T) {
		repo := &modelMgmtRepo{models: []domain.Model{
			{ID: "m1", Name: "chat-x", Capabilities: []domain.ModelCapability{domain.CapChat}, Enabled: true},
		}}
		svc := NewModelMgmtService(repo, invalidatorFunc(func() {
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
		svc := NewModelMgmtService(repo, invalidatorFunc(func() { invalidated = true }))
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
		svc := NewModelMgmtService(repo, invalidatorFunc(func() { invalidated = true }))
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
}
