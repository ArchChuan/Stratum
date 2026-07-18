package workers

import (
	"context"
	"fmt"
	"strings"

	llmgateway "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	memport "github.com/byteBuilderX/stratum/internal/memory/domain/port"
	pipeline "github.com/byteBuilderX/stratum/internal/memory/infrastructure/pipeline"
)

// LLMEntityProfiler adapts pipeline.LLMClient to memport.EntityProfiler. It
// distills a set of scattered facts about one entity into a single durable
// profile paragraph — the top tier of long-term consolidation, turning
// low-value fragments into a compact, high-value memory the injector can surface
// cheaply.
type LLMEntityProfiler struct {
	client   pipeline.LLMClient
	tenantID string
	resolver TenantLLMResolver
}

// Compile-time assertion that LLMEntityProfiler satisfies the port.
var _ memport.EntityProfiler = (*LLMEntityProfiler)(nil)

func NewLLMEntityProfiler(client pipeline.LLMClient) *LLMEntityProfiler {
	return &LLMEntityProfiler{client: client}
}

// NewResolvingLLMEntityProfiler resolves the tenant's LLM client on every
// profile generation so that live tenant settings changes take effect without a
// worker restart — mirrors NewResolvingLLMSuperseder.
func NewResolvingLLMEntityProfiler(tenantID string, resolver TenantLLMResolver) *LLMEntityProfiler {
	return &LLMEntityProfiler{tenantID: tenantID, resolver: resolver}
}

// GenerateProfile builds a concise natural-language profile of the entity from
// the supplied facts. The output is plain prose (not JSON) so it can be injected
// verbatim as long-term context. Returns an empty profile with no error when no
// facts are supplied, so callers can safely skip the entity.
func (p *LLMEntityProfiler) GenerateProfile(ctx context.Context, entityName, entityType string, facts []string) (string, error) {
	if len(facts) == 0 {
		return "", nil
	}
	var b strings.Builder
	for i, f := range facts {
		fmt.Fprintf(&b, "%d. %s\n", i+1, f)
	}
	prompt := fmt.Sprintf(`根据以下已知事实，为实体生成一段简洁、准确的长期画像。

实体名称：%s
实体类型：%s

已知事实：
%s
要求：
- 综合所有事实，提炼出稳定、长期有效的核心特征，忽略一次性或临时性的细节
- 用第三人称陈述，控制在 3 句话以内
- 只依据给定事实，不臆测、不编造
- 直接输出画像正文，不加任何前缀、标题或解释`, entityName, entityType, b.String())

	client := p.client
	if p.resolver != nil {
		resolved, err := resolveTenantLLM(ctx, p.tenantID, p.resolver)
		if err != nil {
			return "", err
		}
		client = resolved
	}
	if client == nil {
		return "", fmt.Errorf("llm generate profile: client unavailable")
	}

	resp, err := client.Complete(ctx, &llmgateway.CompletionRequest{
		Messages:  []llmgateway.Message{{Role: "user", Content: prompt}},
		MaxTokens: 512,
	})
	if err != nil {
		return "", fmt.Errorf("llm generate profile: %w", err)
	}
	return strings.TrimSpace(resp.Content), nil
}
