package domain

import (
	"encoding/json"
	"slices"
)

// AgentVersionSnapshot 是 agent 可编辑面的版本化快照，写入通用产品版本基座
// resource_versions 的 payload，供版本历史展示与回滚重建。
//
// 与 AgentRevision（评测优化候选）语义独立：AgentRevision 只服务评测优化链路，
// 携带 ModelParameters/Bindings 等评测控制面字段；本快照镜像编辑面字段，是
// 用户可见的产品版本历史。快照字段与 PgAgentRepo 的 UPDATE 列对齐，保证回滚
// 重建不丢字段。type/created_by 等不可变字段不进快照（回滚不触碰它们）。
type AgentVersionSnapshot struct {
	Name             string  `json:"name"`
	Description      string  `json:"description"`
	SystemPrompt     string  `json:"system_prompt"`
	LLMModel         string  `json:"llm_model"`
	MaxIterations    int     `json:"max_iterations"`
	MaxContextTokens int     `json:"max_context_tokens"`
	MemoryScope      string  `json:"memory_scope"`
	Temperature      float32 `json:"temperature"`
	ReasoningEffort  string  `json:"reasoning_effort"`
	MaxTokens        int     `json:"max_tokens"`
	// Parameters 是 memory.* 资源作用域参数（dotted key）。temperature 等采样参数
	// 由顶层字段承载，由仓储 pack 进 agents.parameters JSONB，此处不重复。
	Parameters              map[string]any `json:"parameters,omitempty"`
	AllowedSkills           []string       `json:"allowed_skills"`
	MCPToolIDs              []string       `json:"mcp_tool_ids"`
	KnowledgeWorkspaceIDs   []string       `json:"knowledge_workspace_ids"`
	DelegateEnabled         bool           `json:"delegate_enabled"`
	DelegateMaxDepth        int            `json:"delegate_max_depth"`
	DelegateDefaultMaxSteps int            `json:"delegate_default_max_steps"`
}

// SnapshotFromConfig 捕获 cfg 的可编辑面（Update 构建完成、校验通过后调用）。
func SnapshotFromConfig(cfg *AgentConfig) AgentVersionSnapshot {
	if cfg == nil {
		return AgentVersionSnapshot{}
	}
	return AgentVersionSnapshot{
		Name:                    cfg.Name,
		Description:             cfg.Description,
		SystemPrompt:            cfg.SystemPrompt,
		LLMModel:                cfg.LLMModel,
		MaxIterations:           cfg.MaxIterations,
		MaxContextTokens:        cfg.MaxContextTokens,
		MemoryScope:             cfg.MemoryScope,
		Temperature:             cfg.Temperature,
		ReasoningEffort:         cfg.ReasoningEffort,
		MaxTokens:               cfg.MaxTokens,
		Parameters:              cloneAnyMap(cfg.MemoryParameters),
		AllowedSkills:           slices.Clone(cfg.AllowedSkills),
		MCPToolIDs:              slices.Clone(cfg.MCPToolIDs),
		KnowledgeWorkspaceIDs:   slices.Clone(cfg.KnowledgeWorkspaceIDs),
		DelegateEnabled:         cfg.DelegateEnabled,
		DelegateMaxDepth:        cfg.DelegateMaxDepth,
		DelegateDefaultMaxSteps: cfg.DelegateDefaultMaxSteps,
	}
}

// Map 渲染为 resource_versions.payload（snake_case 键，canonical JSON 可哈希）。
// Go 的 encoding/json 对 map 键排序，输出确定，与 versioning/domain 的
// ComputeContentHash 配套。
func (s AgentVersionSnapshot) Map() map[string]any {
	encoded, err := json.Marshal(s)
	if err != nil {
		return map[string]any{}
	}
	var m map[string]any
	_ = json.Unmarshal(encoded, &m)
	return m
}

// SnapshotFromMap 从 resource_versions.payload 重建快照（回滚路径）。未知键
// 忽略（版本历史向前兼容），缺失键回落零值。
func SnapshotFromMap(payload map[string]any) (AgentVersionSnapshot, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return AgentVersionSnapshot{}, err
	}
	var s AgentVersionSnapshot
	if err := json.Unmarshal(encoded, &s); err != nil {
		return AgentVersionSnapshot{}, err
	}
	return s, nil
}

// ToConfig 从快照重建 AgentConfig，供回滚写入。id 保留资源标识，createdBy 保留
// 原始创建者（Update/Rollback 的 SET 列均不触碰 created_by，此处仅用于审计投影
// 与回读的一致性）。
func (s AgentVersionSnapshot) ToConfig(id, createdBy string) *AgentConfig {
	return &AgentConfig{
		ID:                      id,
		Name:                    s.Name,
		Description:             s.Description,
		SystemPrompt:            s.SystemPrompt,
		LLMModel:                s.LLMModel,
		MaxIterations:           s.MaxIterations,
		MaxContextTokens:        s.MaxContextTokens,
		MemoryScope:             s.MemoryScope,
		Temperature:             s.Temperature,
		ReasoningEffort:         s.ReasoningEffort,
		MaxTokens:               s.MaxTokens,
		MemoryParameters:        cloneAnyMap(s.Parameters),
		AllowedSkills:           slices.Clone(s.AllowedSkills),
		MCPToolIDs:              slices.Clone(s.MCPToolIDs),
		KnowledgeWorkspaceIDs:   slices.Clone(s.KnowledgeWorkspaceIDs),
		DelegateEnabled:         s.DelegateEnabled,
		DelegateMaxDepth:        s.DelegateMaxDepth,
		DelegateDefaultMaxSteps: s.DelegateDefaultMaxSteps,
		CreatedBy:               createdBy,
	}
}

// cloneAnyMap 深拷贝 memory.* 参数 map，避免快照与 cfg 共享底层存储。
func cloneAnyMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	out := make(map[string]any, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}
