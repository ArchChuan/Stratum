package capgateway

import (
	"context"
	"strings"
	"time"

	"github.com/byteBuilderX/stratum/internal/agent/domain/port"
	"go.uber.org/zap"
)

// compactionMaxTokens 限制摘要输出的 token 预算，防止摘要本身反噬上下文。
const compactionMaxTokens = 800

const historyCompactionTimeout = 5 * time.Second

// compactionSystemPrompt 指令 LLM 生成保留关键语义的要点摘要。
const compactionSystemPrompt = "你是对话历史压缩器。请把以下对话压成不超过 500 字的要点摘要，" +
	"以第三人称客观记录：保留关键事实、已达成的决定、以及尚未解决的问题；" +
	"剔除寒暄与冗余细节。只输出摘要正文，不要任何前后缀。"

// LLMHistoryCompactor 通过 CapabilityGateway 调用 LLM，实现
// port.HistoryCompactor：把一段对话历史压缩成一条纯文本摘要。
type LLMHistoryCompactor struct {
	gw     port.CapabilityGateway
	model  string
	logger *zap.Logger
}

// NewLLMHistoryCompactor 构造摘要器。gw 为统一路由门面，model 指定用于
// 压缩的模型（可与主对话模型不同，通常选更廉价的），logger 用于观测。
func NewLLMHistoryCompactor(gw port.CapabilityGateway, model string, logger *zap.Logger) *LLMHistoryCompactor {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &LLMHistoryCompactor{gw: gw, model: model, logger: logger}
}

// CompactHistory 把 messages 拼成一段可读对话，交由 LLM 生成要点摘要。
// 空输入直接返回空摘要；gateway 出错则原样上抛，由调用方降级为截断。
func (c *LLMHistoryCompactor) CompactHistory(ctx context.Context, messages []port.LLMMessage) (string, error) {
	if len(messages) == 0 {
		return "", nil
	}

	convo := renderConversation(messages)

	req := port.CapabilityRequest{
		Type: port.CapLLM,
		LLM: &port.LLMCapRequest{
			Model: c.model,
			Messages: []port.LLMMessage{
				{Role: "system", Content: compactionSystemPrompt},
				{Role: "user", Content: convo},
			},
			Temperature: 0.3,
			MaxTokens:   compactionMaxTokens,
		},
	}

	compactionTimeout := historyCompactionTimeout
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if half := remaining / 2; half < compactionTimeout {
			compactionTimeout = half
		}
	}
	compactionCtx, cancel := context.WithTimeout(ctx, max(compactionTimeout, time.Millisecond))
	defer cancel()
	resp, err := c.gw.Route(compactionCtx, req)
	if err != nil {
		c.logger.Warn("history_compactor: gateway route failed",
			zap.String("model", c.model),
			zap.Int("messages", len(messages)),
			zap.Error(err),
		)
		return "", err
	}

	summary := strings.TrimSpace(resp.Content)
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
