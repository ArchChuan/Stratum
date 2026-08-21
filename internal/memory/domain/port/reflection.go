package port

import (
	"context"

	"github.com/byteBuilderX/stratum/internal/memory/domain"
)

// ReflectionTask 是一条工具轨迹反思任务。Skeleton 是 TrajectorySkeleton 的
// JSON 序列化（agent 侧构建），不携带原始 tool steps。
type ReflectionTask struct {
	TenantID       string `json:"tenant_id"`
	UserID         string `json:"user_id"`
	AgentID        string `json:"agent_id,omitempty"`
	ConversationID string `json:"conversation_id,omitempty"`
	Scope          string `json:"scope"`
	ExecutionID    string `json:"execution_id"`
	Skeleton       []byte `json:"skeleton"` // TrajectorySkeleton JSON
	// ExplicitMemory 表示用户显式"记住"指令，作为反思触发 gate 的显式档位。
	ExplicitMemory bool `json:"explicit_memory,omitempty"`
}

// ReflectionEvidence 是反思条目的轨迹 provenance：只存 execution_id 引用，
// 不存原始步骤。step 区间与工具名集合仅在反思模型输入中出现，不持久化。
type ReflectionEvidence struct {
	ExecutionID string `json:"execution_id"`
}

// ReflectionEntry 是反思模型产出的结构化记忆候选，形状复用 ExtractedFact，
// 额外携带轨迹 provenance。
type ReflectionEntry struct {
	Content    string             `json:"content"`
	Importance float64            `json:"importance"`
	Confidence *float64           `json:"confidence,omitempty"`
	FactType   string             `json:"fact_type"`
	Entities   []string           `json:"entities,omitempty"`
	Evidence   ReflectionEvidence `json:"evidence"`
}

// ToExtractedFact 把反思候选转换为提取事实形状，供共享质量链处理。
func (e *ReflectionEntry) ToExtractedFact() *ExtractedFact {
	return &ExtractedFact{
		Content:    e.Content,
		Importance: e.Importance,
		Confidence: e.Confidence,
		FactType:   e.FactType,
		Entities:   e.Entities,
	}
}

// TrajectoryReflector 把压缩后的轨迹骨架提炼为结构化记忆候选。提示词与
// 模型均为平台级参数（memory.reflection_prompt / memory.reflection_model），
// 不与 agent 绑定。
type TrajectoryReflector interface {
	Reflect(ctx context.Context, tenantID string, skeleton domain.TrajectorySkeleton, existing string) ([]*ReflectionEntry, error)
}

// ReflectionService processes a reflection task end-to-end (gate → LLM 提炼 →
// 证据门 → 持久化)。由 MemoryService 实现。
type ReflectionService interface {
	ReflectAndPersist(ctx context.Context, task *ReflectionTask) error
}
