package port

import "context"

// HistoryCompactor 把一段较老的对话历史压缩成一条简短摘要，
// 在不丢失关键语义的前提下腾出 token 预算。
//
// 与现有的“最老先丢”硬截断（BuildContextMessages）相比，压缩保留了
// 早期对话的信息密度：实现通常调用 LLM 生成摘要。
//
// 契约：
//   - messages 按时间正序传入（最老在前），仅包含 user/assistant 轮次，
//     不含 tool_call / tool_result（进图前的 chatStore 历史即满足此约束）。
//   - 返回的 summary 为纯文本，调用方会将其作为上下文注入。
//   - 任何失败都应返回 error，调用方据此降级为截断，绝不阻断主流程。
type HistoryCompactor interface {
	CompactHistory(ctx context.Context, messages []LLMMessage) (summary string, err error)
}
