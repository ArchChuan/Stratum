package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.uber.org/zap"

	llmdomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	memport "github.com/byteBuilderX/stratum/internal/memory/domain/port"
	pipeline "github.com/byteBuilderX/stratum/internal/memory/infrastructure/pipeline"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// LLMSuperseder adapts an LLM client or tenant resolver to memport.LLMSuperseder.
type LLMSuperseder struct {
	client        pipeline.LLMClient
	tenantID      string
	resolver      TenantLLMResolver
	paramResolver memport.PlatformParamResolver
	logger        *zap.Logger
}

func NewLLMSuperseder(client pipeline.LLMClient) *LLMSuperseder {
	return &LLMSuperseder{client: client}
}

// NewResolvingLLMSuperseder resolves the tenant client for every judgment.
func NewResolvingLLMSuperseder(tenantID string, resolver TenantLLMResolver) *LLMSuperseder {
	return &LLMSuperseder{tenantID: tenantID, resolver: resolver}
}

// WithLogger 注入降级日志记录器（结构化失败白名单摘要）。nil 安全。
func (s *LLMSuperseder) WithLogger(l *zap.Logger) *LLMSuperseder {
	s.logger = l
	return s
}

// WithParamResolver sets the platform parameter resolver used to resolve
// per-call supersede model/temperature/prompt. A nil resolver keeps the const
// defaults.
func (s *LLMSuperseder) WithParamResolver(r memport.PlatformParamResolver) *LLMSuperseder {
	s.paramResolver = r
	return s
}

func (s *LLMSuperseder) JudgeSupersede(ctx context.Context, oldFact, newFact string) (*memport.SupersedeJudgment, error) {
	client := s.client
	if s.resolver != nil {
		resolved, err := resolveTenantLLM(ctx, s.tenantID, s.resolver)
		if err != nil {
			return nil, err
		}
		client = resolved
	}
	if client == nil {
		return nil, fmt.Errorf("llm supersede: client unavailable")
	}
	model := resolvePlatformString(ctx, s.paramResolver, "memory.supersede_model", "")
	temperature := resolvePlatformFloat(ctx, s.paramResolver, "memory.supersede_temperature", 0)
	promptTmpl := resolvePlatformString(ctx, s.paramResolver, "memory.supersede_prompt", "")
	if strings.TrimSpace(promptTmpl) == "" {
		// fail-closed：无显式配置不允许空提示词调用判定模型。
		return nil, fmt.Errorf("memory supersede: memory.supersede_prompt not configured (fail-closed)")
	}
	prompt := fmt.Sprintf(promptTmpl, oldFact, newFact)
	// 判定模型为空：交由 llmgateway client 默认解析（pre-refactor 行为）。
	judgment, err := pipeline.CompleteStructured(ctx, client, llmdomain.NewExtractRequest(
		model, "", prompt, temperature, constants.MemorySupersedeJudgeMaxTokens,
	), parseSupersedeJudgment,
		func(j memport.SupersedeJudgment) error { return j.Validate() },
		s.logger, "supersede")
	if err != nil {
		return nil, err
	}
	return &judgment, nil
}

// parseSupersedeJudgment 解析判定 JSON。解析失败由 CompleteStructured
// 带错重试处理（错误位置经 correction 丢回模型）。
func parseSupersedeJudgment(raw string) (memport.SupersedeJudgment, error) {
	var j memport.SupersedeJudgment
	if err := json.Unmarshal([]byte(raw), &j); err != nil {
		return j, fmt.Errorf("parse judgment: %w", err)
	}
	return j, nil
}
