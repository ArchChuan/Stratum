package capgateway

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"go.uber.org/zap"
)

// BudgetPolicy 是压缩路径的时间片策略（Spec 第 4 节）：总预算按
// 剩余/剩余尝试数 分摊到每次尝试，各自独立 ctx——保留 fallback 容灾但
// 不放大用户可感知时延。行为数值定义在 pkg/constants，禁止内联。
type BudgetPolicy struct {
	Total          time.Duration
	NoPrimaryRetry bool
	MaxCandidates  int
}

// DefaultCompactionBudgetPolicy 是压缩路径默认策略：总预算
// constants.CompactionBudgetTotal、主模型不立即重试、候选上限
// constants.CompactionMaxCandidates。
var DefaultCompactionBudgetPolicy = BudgetPolicy{
	Total:          constants.CompactionBudgetTotal,
	NoPrimaryRetry: true,
	MaxCandidates:  constants.CompactionMaxCandidates,
}

// permanentMarker 是 graph/retry.go 同模式的本地副本（DDD：infrastructure
// 不 import 兄弟 context 的 infrastructure，经方法探测鸭子类型识别下游
// permanent 错误，避免跨包类型依赖）。
type permanentMarker interface{ Permanent() bool }

// isPermanent 经 errors.As 探测错误链中的 permanent 标记。
func isPermanent(err error) bool {
	var m permanentMarker
	return errors.As(err, &m)
}

// contextLengthMarker 是 llmgateway ErrContextLengthExceeded 的跨包探测协议。
type contextLengthMarker interface{ ContextLengthExceeded() bool }

// isContextLengthExceeded 报告错误链是否含上下文超限标记（Task 9 语义：
// 上下文超限时压缩重试无意义，直接停链）。
func isContextLengthExceeded(err error) bool {
	var m contextLengthMarker
	return errors.As(err, &m)
}

// compactionSlice 计算下一次尝试的时间片：remaining / attemptsLeft。
// remaining 耗尽（≤0）时回落到最小时间片，保证每次尝试仍有执行窗口。
func compactionSlice(remaining time.Duration, attemptsLeft int) time.Duration {
	slice := remaining / time.Duration(attemptsLeft)
	if slice <= 0 {
		slice = constants.CompactionMinSlice
	}
	return slice
}

// compactionSystemPrompt 指令 LLM 生成保留关键语义的要点摘要。
const compactionSystemPrompt = "你是对话历史压缩器。请把以下对话压成不超过 500 字的要点摘要，" +
	"以第三人称客观记录：保留关键事实、已达成的决定、以及尚未解决的问题；" +
	"剔除寒暄与冗余细节。只输出摘要正文，不要任何前后缀。"

// LLMHistoryCompactor 通过 CapabilityGateway 调用 LLM，实现
// port.HistoryCompactor：把一段对话历史压缩成一条纯文本摘要。
type LLMHistoryCompactor struct {
	gw                  port.CapabilityGateway
	model               string
	logger              *zap.Logger
	compactionMaxTokens int
	systemPrompt        string
}

// NewLLMHistoryCompactor 构造摘要器。gw 为统一路由门面，model 指定用于
// 压缩的模型（可与主对话模型不同，通常选更廉价的），logger 用于观测。
// compactionMaxTokens 是压缩 LLM 调用的最大输出 token 数；传 0 则使用
// 默认值 800。
func NewLLMHistoryCompactor(gw port.CapabilityGateway, model string, logger *zap.Logger, compactionMaxTokens int) *LLMHistoryCompactor {
	if logger == nil {
		logger = zap.NewNop()
	}
	if compactionMaxTokens <= 0 {
		compactionMaxTokens = 800
	}
	return &LLMHistoryCompactor{gw: gw, model: model, logger: logger, compactionMaxTokens: compactionMaxTokens}
}

// WithCompactionPrompt 注入机制基线压缩指令（model_profiles 建档后由
// wiring 解析注入）。空值保持 compactionSystemPrompt 兜底（现状行为）。
func (c *LLMHistoryCompactor) WithCompactionPrompt(p string) *LLMHistoryCompactor {
	c.systemPrompt = p
	return c
}

// CompactHistory 把 messages 拼成一段可读对话，交由 LLM 生成要点摘要。
// 空输入直接返回空摘要；gateway 出错则原样上抛，由调用方降级为截断。
// Spec 第 4 节：总预算按时间片分摊到 1 主 + MaxCandidates 候选的每次尝试，
// 各自独立 ctx；主模型瞬态失败不做 gateway 层立即重试，直接降级候选。
func (c *LLMHistoryCompactor) CompactHistory(ctx context.Context, messages []port.LLMMessage) (string, error) {
	if len(messages) == 0 {
		return "", nil
	}

	convo := renderConversation(messages)

	systemPrompt := c.systemPrompt
	if systemPrompt == "" {
		systemPrompt = compactionSystemPrompt
	}
	req := port.CapabilityRequest{
		Type: port.CapLLM,
		LLM: &port.LLMCapRequest{
			Model: c.model,
			Messages: []port.LLMMessage{
				{Role: "system", Content: systemPrompt},
				{Role: "user", Content: convo},
			},
			Temperature:    0.3,
			MaxTokens:      c.compactionMaxTokens,
			NoPrimaryRetry: DefaultCompactionBudgetPolicy.NoPrimaryRetry,
			MaxCandidates:  DefaultCompactionBudgetPolicy.MaxCandidates,
		},
	}

	policy := DefaultCompactionBudgetPolicy
	attempts := 1 + policy.MaxCandidates
	remaining := policy.Total
	var lastErr error
	for i := 0; i < attempts; i++ {
		slice := compactionSlice(remaining, attempts-i)
		sliceCtx, sliceCancel := context.WithTimeout(ctx, slice)
		start := time.Now()
		resp, err := c.gw.Route(sliceCtx, req)
		sliceCancel()
		remaining -= time.Since(start)
		if err == nil {
			return c.finishSuccess(messages, resp.Content)
		}
		lastErr = err
		// DeadlineExceeded/Canceled：gateway 已立即停链，时间片耗尽继续尝试无意义。
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return "", err
		}
		// 永久错误（含 context_length_exceeded、参数 400）→ 立即停链，
		// 不消费候选时间片。
		if isPermanent(err) || isContextLengthExceeded(err) {
			return "", err
		}
		// 其余视为瞬态失败，进入下一时间片尝试候选。
	}
	c.logger.Warn("history_compactor: gateway route failed",
		zap.String("model", c.model),
		zap.Int("messages", len(messages)),
		zap.Int("attempts", attempts),
		zap.Error(lastErr),
	)
	return "", lastErr
}

// finishSuccess 归一化成功响应并记录摘要观测日志。
func (c *LLMHistoryCompactor) finishSuccess(messages []port.LLMMessage, content string) (string, error) {
	summary := strings.TrimSpace(content)
	c.logger.Debug("history_compactor: compacted history",
		zap.Int("messages", len(messages)),
		zap.Int("summary_len", len([]rune(summary))),
	)
	return summary, nil
}

// renderConversation 把消息按 "Role: Content" 逐行拼成一段对话文本。
func renderConversation(messages []port.LLMMessage) string {
	var b strings.Builder
	for _, m := range messages {
		role := m.Role
		switch role {
		case "user":
			role = "User"
		case "assistant":
			role = "Assistant"
		case "system":
			role = "System"
		}
		b.WriteString(role)
		b.WriteString(": ")
		b.WriteString(m.Content)
		b.WriteByte('\n')
	}
	return b.String()
}
