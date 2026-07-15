package skilladapter

import (
	"context"
	"fmt"
	"time"

	"github.com/byteBuilderX/stratum/internal/evaluation/domain"
	"github.com/byteBuilderX/stratum/internal/evaluation/domain/port"
	"github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/google/uuid"
)

type VersionExecutor interface {
	ExecuteVersion(ctx context.Context, versionID string, input any) (any, error)
}

type Adapter struct {
	executor VersionExecutor
}

func New(executor VersionExecutor) *Adapter {
	return &Adapter{executor: executor}
}

func (a *Adapter) ExecuteRevision(
	ctx context.Context,
	tenantID string,
	ref domain.ResourceRef,
	testCase domain.EvalCase,
) (port.ExecutionResult, error) {
	if ref.Kind != domain.ResourceKindSkill {
		return port.ExecutionResult{}, fmt.Errorf("skill evaluation adapter: unsupported resource kind %q", ref.Kind)
	}
	ctx = postgres.WithTenant(ctx, &postgres.TenantContext{TenantID: tenantID})
	started := time.Now()
	output, err := a.executor.ExecuteVersion(ctx, ref.RevisionID, testCase.Input)
	if err != nil {
		return port.ExecutionResult{}, err
	}
	return port.ExecutionResult{
		Output:     unwrapOutput(output),
		TraceID:    uuid.Must(uuid.NewV7()).String(),
		DurationMs: int(time.Since(started).Milliseconds()),
	}, nil
}

func unwrapOutput(output any) any {
	values, ok := output.(map[string]any)
	if !ok {
		return output
	}
	if content, exists := values["content"]; exists {
		return content
	}
	if value, exists := values["output"]; exists {
		return value
	}
	return output
}
