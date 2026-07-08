package tokenutil

// EstimateText 估算单段文本的 token 数。
// 算法：UTF-8 字节数 / 3。
// 中文 3 字节/字符 ≈ 1 token；英文平均约 4 字节/token，
// 混合文本误差 <20%，无需引入 tokenizer 依赖。
func EstimateText(s string) int {
	if s == "" {
		return 0
	}
	n := len(s) / 3
	if n == 0 {
		return 1
	}
	return n
}

// Message 是估算用的通用消息结构，不依赖任何 domain 类型。
type Message struct {
	Role    string
	Content string
}

// EstimateMessages 估算消息列表的总 token 数。
// 每条消息额外计 4 token（role/分隔符开销）。
func EstimateMessages(msgs []Message) int {
	total := 0
	for _, m := range msgs {
		total += EstimateText(m.Role) + EstimateText(m.Content) + 4
	}
	return total
}
