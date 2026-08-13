package workers

import (
	"context"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"

	memport "github.com/byteBuilderX/stratum/internal/memory/domain/port"
	pipeline "github.com/byteBuilderX/stratum/internal/memory/infrastructure/pipeline"
)

// supersedePromptTemplate 是判断模板兜底（现状硬编码值）。机制基线建档后
// 由 wiring 注入覆盖，空值维持现状行为。
const supersedePromptTemplate = `判断新事实是否应该取代旧事实。

旧事实：%s
新事实：%s

判断标准：
- 如果新事实是对旧事实的更新、纠正或推翻，则应取代（supersedes: true）
- 如果两者描述不同方面或可以并存，则不取代（supersedes: false）
- 如果新事实只是旧事实的子集或更模糊的表达，则不取代

只输出 JSON，不加任何说明：
{"supersedes": true/false, "reason": "简短说明"}`

// LLMSuperseder adapts an LLM client or tenant resolver to memport.LLMSuperseder.
type LLMSuperseder struct {
	client      pipeline.LLMClient
	tenantID    string
	resolver    TenantLLMResolver
	judgePrompt string
	logger      *zap.Logger
}

func NewLLMSuperseder(client pipeline.LLMClient) *LLMSuperseder {
	return &LLMSuperseder{client: client}
}

// NewResolvingLLMSuperseder resolves the tenant client for every judgment.
func NewResolvingLLMSuperseder(tenantID string, resolver TenantLLMResolver) *LLMSuperseder {
	return &LLMSuperseder{tenantID: tenantID, resolver: resolver}
}

// WithJudgePrompt overrides the supersede judgment template with the
// mechanism baseline prompt. Empty keeps supersedePromptTemplate.
func (s *LLMSuperseder) WithJudgePrompt(p string) *LLMSuperseder {
	s.judgePrompt = p
	return s
}

// WithLogger 注入降级日志记录器（结构化失败白名单摘要）。nil 安全。
func (s *LLMSuperseder) WithLogger(l *zap.Logger) *LLMSuperseder {
	s.logger = l
	return s
}

// judgePromptOr 返回生效模板：基线注入值优先，空则兜底内置常量。
func (s *LLMSuperseder) judgePromptOr() string {
	if s.judgePrompt != "" {
		return s.judgePrompt
	}
	return supersedePromptTemplate
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
	prompt := fmt.Sprintf(s.judgePromptOr(), oldFact, newFact)
	judgment, err := pipeline.CompleteStructured(ctx, client, &memport.CompletionRequest{
		Messages:  []memport.CompletionMessage{{Role: "user", Content: prompt}},
		MaxTokens: 256,
	}, parseSupersedeJudgment,
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
