package capgateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain"
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

// LLMHistoryCompactor 通过 CapabilityGateway 调用 LLM，实现
// port.HistoryCompactor：把一段对话历史压缩成一条纯文本摘要。
type LLMHistoryCompactor struct {
	gw                  port.CapabilityGateway
	logger              *zap.Logger
	promptResolver      port.PlatformPromptResolver
	compactionMaxTokens int
}

// NewLLMHistoryCompactor 构造摘要器。gw 为统一路由门面，logger 用于观测。
// compactionMaxTokens 是压缩 LLM 调用的最大输出 token 数；传 0 则使用
// constants.CompactionMaxTokensCeiling。promptResolver 是平台级压缩配置的
// 唯一来源：提示词/温度/模型（agent.compaction_prompt/_temperature/_model）
// 在每次压缩时统一从这里解析，所有 agent（含内置助手）共用同一套配置，
// 无 per-agent 副本。
func NewLLMHistoryCompactor(gw port.CapabilityGateway, logger *zap.Logger, compactionMaxTokens int, promptResolver port.PlatformPromptResolver) *LLMHistoryCompactor {
	if logger == nil {
		logger = zap.NewNop()
	}
	if compactionMaxTokens <= 0 {
		compactionMaxTokens = constants.CompactionMaxTokensCeiling
	}
	return &LLMHistoryCompactor{
		gw:                  gw,
		logger:              logger,
		promptResolver:      promptResolver,
		compactionMaxTokens: compactionMaxTokens,
	}
}

// resolvePrompt 解析平台级压缩提示词。未配置/空 → fail-closed 错误，禁止
// 空 system prompt 静默调用 LLM（对齐 memory.*_prompt 模式）。
func (c *LLMHistoryCompactor) resolvePrompt(ctx context.Context) (string, error) {
	if c.promptResolver == nil {
		return "", fmt.Errorf("history compactor: %w", domain.ErrCompactionPromptNotConfigured)
	}
	v, ok, err := c.promptResolver.ResolvePlatform(ctx, "agent.compaction_prompt")
	if err != nil {
		return "", fmt.Errorf("history compactor: resolve compaction prompt: %w", err)
	}
	if !ok {
		return "", fmt.Errorf("history compactor: %w", domain.ErrCompactionPromptNotConfigured)
	}
	prompt, ok := v.(string)
	if !ok || strings.TrimSpace(prompt) == "" {
		return "", fmt.Errorf("history compactor: %w", domain.ErrCompactionPromptNotConfigured)
	}
	return prompt, nil
}

// resolveTemperature 解析平台级压缩温度 agent.compaction_temperature。
// 未配置/0 → constants.CompactionDefaultTemperature；随后钳制 [0,1]
// （Qwen/Zhipu 拒收 >1 → 网关 500）。
func (c *LLMHistoryCompactor) resolveTemperature(ctx context.Context) (float32, error) {
	temperature := float32(constants.CompactionDefaultTemperature)
	if c.promptResolver != nil {
		v, ok, err := c.promptResolver.ResolvePlatform(ctx, "agent.compaction_temperature")
		if err != nil {
			return 0, fmt.Errorf("history compactor: resolve compaction temperature: %w", err)
		}
		if f, convOK := toFloat32(v); ok && convOK && f != 0 {
			temperature = f
		}
	}
	if temperature < constants.CompactionTemperatureMin {
		temperature = constants.CompactionTemperatureMin
	}
	if temperature > constants.CompactionTemperatureMax {
		temperature = constants.CompactionTemperatureMax
	}
	return temperature, nil
}

// resolveModel 解析平台级压缩模型 agent.compaction_model。未配置/空 →
// 空串交由网关默认模型解析（对齐 memory.*_model 的“空 = client 默认”）。
func (c *LLMHistoryCompactor) resolveModel(ctx context.Context) (string, error) {
	if c.promptResolver == nil {
		return "", nil
	}
	v, ok, err := c.promptResolver.ResolvePlatform(ctx, "agent.compaction_model")
	if err != nil {
		return "", fmt.Errorf("history compactor: resolve compaction model: %w", err)
	}
	if !ok {
		return "", nil
	}
	model, _ := v.(string)
	return strings.TrimSpace(model), nil
}

// toFloat32 converts a ResolvePlatform numeric value (JSON numbers arrive as
// float64) to float32; non-numeric values return ok=false.
func toFloat32(v any) (float32, bool) {
	switch n := v.(type) {
	case float64:
		return float32(n), true
	case float32:
		return n, true
	case int:
		return float32(n), true
	case int64:
		return float32(n), true
	default:
		return 0, false
	}
}

// CompactHistory 把 messages 拼成一段可读对话，交由 LLM 生成要点摘要。
// 空输入直接返回空摘要；gateway 出错则原样上抛，由调用方降级为截断。
// Spec 第 4 节：总预算按时间片分摊到 1 主 + MaxCandidates 候选的每次尝试，
// 各自独立 ctx；主模型瞬态失败不做 gateway 层立即重试，直接降级候选。
func (c *LLMHistoryCompactor) CompactHistory(ctx context.Context, messages []port.LLMMessage) (string, error) {
	if len(messages) == 0 {
		return "", nil
	}

	settings, err := c.resolveSettings(ctx)
	if err != nil {
		return "", err
	}
	convo := renderConversation(messages)

	req := port.CapabilityRequest{
		Type: port.CapLLM,
		LLM: &port.LLMCapRequest{
			Model: settings.model,
			Messages: []port.LLMMessage{
				{Role: "system", Content: settings.prompt},
				{Role: "user", Content: convo},
			},
			Temperature:    settings.temperature,
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
		zap.String("model", settings.model),
		zap.Int("messages", len(messages)),
		zap.Int("attempts", attempts),
		zap.Error(lastErr),
	)
	return "", lastErr
}

// compactionSettings 是单次压缩的平台级三值（提示词/温度/模型）。
type compactionSettings struct {
	prompt      string
	temperature float32
	model       string
}

// resolveSettings 一次性解析压缩三值：prompt 未配置 fail-closed，temperature
// 0 = 默认常量，model 空 = 网关默认。
func (c *LLMHistoryCompactor) resolveSettings(ctx context.Context) (compactionSettings, error) {
	prompt, err := c.resolvePrompt(ctx)
	if err != nil {
		return compactionSettings{}, err
	}
	temperature, err := c.resolveTemperature(ctx)
	if err != nil {
		return compactionSettings{}, err
	}
	model, err := c.resolveModel(ctx)
	if err != nil {
		return compactionSettings{}, err
	}
	return compactionSettings{prompt: prompt, temperature: temperature, model: model}, nil
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
// 工具调用对（assistant + ToolCalls 与其 tool 结果）渲染为配对格式
// [Tool] name(args) → result：工具名与参数不再丢失（R3），result 只取
// 已脱敏的 tool 消息 Content（D8——上游把 guarded.ModelContent 写入
// Content，raw 永不进渲染）。未配对的 tool 结果（无对应调用）以
// [Tool result] 行输出，避免静默丢失。普通消息保持原有 Role: Content 格式。
func renderConversation(messages []port.LLMMessage) string {
	callIDs, results := collectToolPairs(messages)
	var b strings.Builder
	for _, m := range messages {
		renderLine(&b, m, callIDs, results)
	}
	return b.String()
}

// collectToolPairs 第一遍扫描：收集所有已声明工具调用 ID 与其 tool 结果，
// 供渲染时配对（未配对的 tool 结果以 [Tool result] 行保留，不静默丢失）。
func collectToolPairs(messages []port.LLMMessage) (map[string]struct{}, map[string]string) {
	callIDs := make(map[string]struct{}, len(messages))
	results := make(map[string]string, len(messages))
	for _, m := range messages {
		for _, tc := range m.ToolCalls {
			callIDs[tc.ID] = struct{}{}
		}
		if m.Role == "tool" && m.ToolCallID != "" {
			results[m.ToolCallID] = m.Content
		}
	}
	return callIDs, results
}

// renderLine 渲染单条消息：工具调用对输出 [Tool] name(args) → result，
// 未配对 tool 结果输出 [Tool result]，普通消息保持 Role: Content。
func renderLine(b *strings.Builder, m port.LLMMessage, callIDs map[string]struct{}, results map[string]string) {
	switch {
	case len(m.ToolCalls) > 0:
		for _, tc := range m.ToolCalls {
			b.WriteString("[Tool] " + tc.Name + "(" + marshalArgs(tc.Arguments) + ") → " + results[tc.ID] + "\n")
		}
	case m.Role == "tool":
		if _, paired := callIDs[m.ToolCallID]; !paired {
			b.WriteString("[Tool result] " + m.Content + "\n")
		}
	default:
		b.WriteString(displayRole(m.Role) + ": " + m.Content + "\n")
	}
}

// displayRole 把 role 映射为渲染用的大小写首字母名称；未识别 role 原样输出。
func displayRole(role string) string {
	switch role {
	case "user":
		return "User"
	case "assistant":
		return "Assistant"
	case "system":
		return "System"
	}
	return role
}

// marshalArgs 把工具调用参数序列化为渲染文本（与循环侧 toEstimate 同源：
// json.Marshal；失败返回空串——仅影响渲染，不阻断压缩）。
func marshalArgs(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	if s, err := json.Marshal(args); err == nil {
		return string(s)
	}
	return ""
}
