package constants

// DimensionForModel 返回嵌入模型的向量维度——全系统单一事实源
// （跨包行为数字，CLAUDE.md 要求入 pkg/constants）。
// 修正记录：text-embedding-v2 由历史 1536 修正为 1024，与 knowledge 侧
// 旧 vectorDim 一致；存量 1536 维 collection 由 legacy 回退 dim 检查兜底。
func DimensionForModel(name string) int {
	switch name {
	case "text-embedding-v1":
		return 1536 // DashScope v1
	case "text-embedding-v2", "text-embedding-v3", "text-embedding-v4":
		return 1024 // DashScope v2/v3/v4 default
	case "embedding-3":
		return 2048 // Zhipu
	default:
		return 1536 // OpenAI text-embedding-3-small / ada-002
	}
}
