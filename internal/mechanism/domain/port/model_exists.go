package port

import "context"

// ModelCapability 是模型能力类型。值语义对齐 llmgateway domain 常量
// （chat/embedding）；mechanism 侧独立定义，避免跨 context import。
type ModelCapability string

const (
	CapChat      ModelCapability = "chat"
	CapEmbedding ModelCapability = "embedding"
)

// ModelExists 校验模型名在平台全局目录中存在（enabled + provider enabled +
// 能力匹配）。UpsertProfile 用它在写入前拒绝引用不存在模型的档案
// （fail-closed：目录故障传播错误，不默认放行）。
type ModelExists interface {
	Exists(ctx context.Context, model string, capability ModelCapability) (bool, error)
}
