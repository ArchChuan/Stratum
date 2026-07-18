package workers

import (
	"context"
	"fmt"

	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
)

// TenantLLMClient is the minimal completion capability used by memory workers.
type TenantLLMClient interface {
	Complete(context.Context, *llmgateway.CompletionRequest) (*llmgateway.CompletionResponse, error)
}

// TenantLLMResolver resolves the current client for a tenant at operation time.
type TenantLLMResolver func(context.Context, string) (TenantLLMClient, error)

func resolveTenantLLM(ctx context.Context, tenantID string, resolver TenantLLMResolver) (TenantLLMClient, error) {
	if resolver == nil {
		return nil, fmt.Errorf("resolve tenant llm: resolver unavailable")
	}
	client, err := resolver(ctx, tenantID)
	if err != nil {
		return nil, fmt.Errorf("resolve tenant llm: %w", err)
	}
	if client == nil {
		return nil, fmt.Errorf("resolve tenant llm: client unavailable")
	}
	return client, nil
}
