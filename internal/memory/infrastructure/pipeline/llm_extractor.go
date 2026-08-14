package pipeline

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.uber.org/zap"

	llmdomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	memport "github.com/byteBuilderX/stratum/internal/memory/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
)

// extractionSystemPrompt 是抽取模板（现状硬编码值，mechanism 移除后为唯一权威）。
const extractionSystemPrompt = `你是一个长期记忆提取助手，负责从对话中提取关于用户（%s）的有价值事实，供 AI 助手（%s）在未来对话中使用。

提取规则（严格执行）：
- 只提取用户明确陈述、确认或展现的事实
- 不提取：用户的提问、问候语、AI 助手的回复内容、工具调用的输出
- 不提取泛化描述（如"用户提到了某件事"），只提取具体事实
- 优先精确性：「用户偏好在 VS Code 中使用暗色主题」优于「用户有主题偏好」
- 最多提取 %d 条事实；宁少勿滥，低价值事实直接忽略

fact_type 分类：
- preference：用户的喜好、偏好、习惯
- skill：用户掌握的技能或专业知识
- event：已发生的具体事件（过去时）
- state：用户当前的状态或处境
- relationship：用户与某人/某组织的关系
- other：不属于以上分类的陈述性事实

只输出 JSON 数组，不加任何说明或 markdown 标记：
[{"content":"...","importance":0.0-1.0,"fact_type":"...","confidence":0.0-1.0,"entities":["实体名"]}]`

// LLMExtractor adapts LLMClient to memport.LLMExtractor.
type LLMExtractor struct {
	client   LLMClient
	resolver memport.ResourceParamResolver
	// tenantID is captured at construction (the extractor is built per tenant
	// by the wiring seam); agentID arrives per ExtractFacts call.
	tenantID string
	logger   *zap.Logger
}

func NewLLMExtractor(client LLMClient) *LLMExtractor {
	return &LLMExtractor{client: client}
}

// SetResourceResolver wires the per-agent resource parameter resolver
// (registry-backed); nil keeps the pkg/constants defaults.
func (e *LLMExtractor) SetResourceResolver(r memport.ResourceParamResolver) { e.resolver = r }

// SetTenantID sets the tenant identity for parameter resolution. The extractor
// is constructed per tenant by the wiring seam, so the tenant is stable for
// the extractor's lifetime.
func (e *LLMExtractor) SetTenantID(t string) { e.tenantID = t }

// WithLogger 注入降级日志记录器（结构化失败白名单摘要）。nil 安全。
func (e *LLMExtractor) WithLogger(l *zap.Logger) *LLMExtractor {
	e.logger = l
	return e
}

// maxFacts resolves memory.max_facts_per_extraction for the target agent,
// falling back to the constant default when unset, unresolved or unavailable.
func (e *LLMExtractor) maxFacts(ctx context.Context, agentID string) int {
	if e.resolver == nil {
		return constants.MemoryMaxFactsPerExtraction
	}
	v, ok, err := e.resolver.Resolve(ctx, e.tenantID, agentID, "memory.max_facts_per_extraction")
	if err != nil || !ok {
		return constants.MemoryMaxFactsPerExtraction
	}
	return coerceResourceInt(v, constants.MemoryMaxFactsPerExtraction)
}

func (e *LLMExtractor) ExtractFacts(ctx context.Context, userID, agentID string, message string) ([]*memport.ExtractedFact, error) {
	// 抽取模型为空：交由 llmgateway client 默认解析（pre-refactor 行为）。
	system := fmt.Sprintf(extractionSystemPrompt, userID, agentID, e.maxFacts(ctx, agentID))
	req := llmdomain.NewExtractRequest("", system, message, 0, constants.MemoryExtractLLMMaxTokens)
	return extractFactsStructured(ctx, e.client, req, e.logger)
}

// extractFactsStructured 走 CompleteStructured 的带错重试管线，并实现部分成功
// 语义：逐条 Validate，≥1 条通过立即返回通过子集（不为小问题浪费重试）；
// 0 条通过才触发带错重试，耗尽返回 typed error（保留 MarkFailed/DLQ）。
func extractFactsStructured(
	ctx context.Context,
	client llmdomain.Completer,
	req *llmdomain.CompletionRequest,
	logger *zap.Logger,
) ([]*memport.ExtractedFact, error) {
	var valid []*memport.ExtractedFact
	_, err := CompleteStructured(ctx, client, req, parseExtractedFacts,
		func(facts []*memport.ExtractedFact) error {
			if len(facts) == 0 {
				// 模型明确表示无事实（[]）：合法结果，非校验失败，
				// 调用方据此跳过抽取，不触发带错重试。
				valid = nil
				return nil
			}
			valid = facts[:0]
			allInvalid := true
			for _, f := range facts {
				if f.Validate() == nil {
					valid = append(valid, f)
					allInvalid = false
				}
			}
			if allInvalid {
				return &memport.ValidationError{
					Location: "facts", FieldName: "facts",
					Reason: "no fact passed validation",
				}
			}
			return nil
		}, logger, "extract_facts")
	if err != nil {
		return nil, err
	}
	return valid, nil
}

// parseExtractedFacts 从 LLM 原始输出中剥离非 JSON 前缀并解析事实数组。
// Token 截断时按最后完整对象恢复（recoverTruncatedArray）。
func parseExtractedFacts(raw string) ([]*memport.ExtractedFact, error) {
	start := strings.Index(raw, "[")
	if start == -1 {
		return nil, fmt.Errorf("parse extracted facts: no JSON array in response")
	}
	body := raw[start:]
	var facts []*memport.ExtractedFact
	if err := json.NewDecoder(strings.NewReader(body)).Decode(&facts); err != nil {
		// Token limit may have truncated the JSON mid-object; recover by closing at the last complete item.
		if recovered := recoverTruncatedArray(body); recovered != "" {
			var recoveredFacts []*memport.ExtractedFact
			if err2 := json.Unmarshal([]byte(recovered), &recoveredFacts); err2 == nil {
				return recoveredFacts, nil
			}
		}
		return nil, fmt.Errorf("parse extracted facts: %w", err)
	}
	return facts, nil
}

// recoverTruncatedArray finds the last complete JSON object in a truncated array and closes it.
func recoverTruncatedArray(s string) string {
	last := strings.LastIndex(s, "},")
	if last == -1 {
		last = strings.LastIndex(s, "}")
	} else {
		last++ // include the }
	}
	if last == -1 {
		return ""
	}
	return s[:last+1] + "]"
}
