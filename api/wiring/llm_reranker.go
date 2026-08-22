package wiring

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	knowledgeport "github.com/byteBuilderX/stratum/internal/knowledge/domain/port"
	llmgatewaydomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/observability"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"go.uber.org/zap"
)

// llmReranker 是 builtin-score-v1 的 LLM 语义重排器（listwise）：复用平台 LLM
// 网关对候选按查询相关性打分，接口与外部 reranker 同形
// （knowledgeport.Reranker）。放在组合根（与 knowledgeJudge 同先例），
// knowledge/infrastructure/rerank 保持对兄弟 context 零依赖。模型在运行期
// 从请求读取（workspace 显式配置），wiring 不做平台级模型绑定。
type llmReranker struct {
	completer llmgatewaydomain.LLMCompleter // Gateway 结构性满足
	timeout   time.Duration                 // 单次调用预算（≤0 回落 RerankLLMTimeout）
	metrics   observability.MetricsProvider // 可为 nil（跳过指标记录）
	logger    *zap.Logger
}

func newLLMReranker(
	completer llmgatewaydomain.LLMCompleter,
	timeout time.Duration,
	metrics observability.MetricsProvider,
	logger *zap.Logger,
) *llmReranker {
	return &llmReranker{completer: completer, timeout: timeout, metrics: metrics, logger: logger}
}

func (r *llmReranker) rerankTimeout() time.Duration {
	if r.timeout <= 0 {
		return constants.RerankLLMTimeout
	}
	return r.timeout
}

// Rerank 对 req.Documents 按 req.Query 相关度 listwise 打分并返回打分结果。
// 候选正文在本层内部按 RerankLLMMaxDocRunes 截断（调用方传完整候选）。
// 返回 LLM 输出的候选结果（index 去重、非法 index 跳过）；结果不足 topN
// 的补尾与最终排序由调用方负责。失败/超时/解析失败返回 error（fail-open，
// 调用方降级为召回分数排序）。
func (r *llmReranker) Rerank(ctx context.Context, req knowledgeport.RerankRequest) ([]knowledgeport.RerankResult, error) {
	// 空模型显式拒绝（fail-open 由调用方降级）：Gateway.resolveChain 会对空模型
	// 静默回填 provider 默认模型，不挡在这里则未配置模型的 builtin 会用错模型
	// 重排而非降级（review C1）。
	if req.Model == "" {
		return nil, errors.New("llm rerank: empty model")
	}
	ctx, cancel := context.WithTimeout(ctx, r.rerankTimeout())
	defer cancel()

	var prompt strings.Builder
	prompt.WriteString("你是严谨的检索相关性评分法官。给定查询，对下列候选文档片段按与查询的相关性打分（0 到 1，越高越相关），分数要有区分度。只输出 JSON，不输出其他内容。\n\nQuery:\n")
	prompt.WriteString(req.Query)
	prompt.WriteString("\n\nCandidates:\n")
	for i, doc := range req.Documents {
		fmt.Fprintf(&prompt, "%d. %s\n", i, truncateRunes(doc, constants.RerankLLMMaxDocRunes))
	}
	prompt.WriteString("\n输出 JSON：{\"scores\":[{\"index\":<候选编号>,\"score\":<0..1>},...]}，为每个候选恰好输出一个条目。")

	zero := float64(0) // 显式 0 = 确定性采样，避免 provider 默认温度（review M4/F2）
	start := time.Now()
	resp, err := r.completer.Complete(ctx, &llmgatewaydomain.CompletionRequest{
		Model:     req.Model,
		MaxTokens: constants.RerankLLMMaxTokens,
		ResponseFormat: &llmgatewaydomain.ResponseFormat{
			Type: "json_object",
		},
		Temperature: &zero,
		Messages: []llmgatewaydomain.Message{
			{Role: "system", Content: "你是严谨的检索相关性评分法官。只输出 JSON，不输出其他内容。"},
			{Role: "user", Content: prompt.String()},
		},
	})
	if err != nil {
		r.record(ctx, "error")
		return nil, fmt.Errorf("llm rerank: %w", err)
	}
	r.recordDuration(start)

	var parsed struct {
		Scores []struct {
			Index int     `json:"index"`
			Score float32 `json:"score"`
		} `json:"scores"`
	}
	if err := json.Unmarshal([]byte(resp.Content), &parsed); err != nil {
		r.record(ctx, "error")
		return nil, fmt.Errorf("llm rerank: parse scores: %w", err)
	}
	results := make([]knowledgeport.RerankResult, 0, len(parsed.Scores))
	seen := make(map[int]struct{}, len(parsed.Scores))
	for _, s := range parsed.Scores {
		if s.Index < 0 || s.Index >= len(req.Documents) {
			continue // 防御 LLM 幻觉输出非法 index
		}
		if _, ok := seen[s.Index]; ok {
			continue // 重复 index 保留首次出现
		}
		seen[s.Index] = struct{}{}
		results = append(results, knowledgeport.RerankResult{Index: s.Index, Score: s.Score})
	}
	r.record(ctx, "ok")
	return results, nil
}

// record 记录重排请求指标（三态之一：ok/error/degraded）。标签固定
// "builtin-llm"（不暴露平台重排模型名）；tenant 从 ctx 取号（RerankRequest
// 无 tenant 字段，与 Cohere 一致）。metrics 可为 nil（跳过记录）。
func (r *llmReranker) record(ctx context.Context, status string) {
	if r.metrics != nil {
		r.metrics.IncRerankRequest(reqctx.TenantIDFromContext(ctx), "builtin-llm", status)
	}
}

// recordDuration 记录单次重排调用耗时（HTTP 往返成功返回后）。
func (r *llmReranker) recordDuration(start time.Time) {
	if r.metrics != nil {
		r.metrics.RecordRerankDuration("builtin-llm", time.Since(start).Seconds())
	}
}
