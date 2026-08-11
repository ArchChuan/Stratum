package application

import (
	"context"
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/internal/llmgateway/domain/port"
)

type modelMgmtRepo struct {
	model domain.Model
	err   error
}

func (r *modelMgmtRepo) Create(context.Context, string, *domain.Model) error { return r.err }
func (r *modelMgmtRepo) Get(context.Context, string, string) (*domain.Model, error) {
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
func (r *modelMgmtRepo) SetDefaultEmbedding(context.Context, string, string, bool) error {
	return r.err
}

type recordingInvalidator struct {
	tenants []string
}

func (i *recordingInvalidator) Invalidate(tenantID string) {
	i.tenants = append(i.tenants, tenantID)
}

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
