package application

import (
	"context"
	"testing"

	"github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/stretchr/testify/assert"
)

func TestDiagnosticsService(t *testing.T) {
	ctx := context.Background()
	factRepo := new(MockFactRepo)
	entityRepo := new(MockEntityRepo)
	queue := new(MockExtractionQueue)

	factRepo.On("CountActive", ctx, "tenant_123").Return(150, nil)
	factRepo.On("CountSuperseded", ctx, "tenant_123").Return(20, nil)
	queue.On("PendingCount", ctx, "tenant_123").Return(8, nil)
	entityRepo.On("TopByFactCount", ctx, "tenant_123", 10).Return([]port.EntityFactCount{
		{Name: "Python", Count: 45},
		{Name: "JavaScript", Count: 32},
	}, nil)

	svc := NewDiagnosticsService(factRepo, entityRepo, queue)
	diag, err := svc.GetDiagnostics(ctx, "tenant_123")

	assert.NoError(t, err)
	assert.Equal(t, 150, diag.ActiveFactCount)
	assert.Equal(t, 20, diag.SupersededCount)
	assert.Equal(t, 8, diag.QueueLag)
	assert.Len(t, diag.TopEntities, 2)
	assert.Equal(t, "Python", diag.TopEntities[0].Name)
	assert.Equal(t, 45, diag.TopEntities[0].Count)

	factRepo.AssertExpectations(t)
	entityRepo.AssertExpectations(t)
	queue.AssertExpectations(t)
}
