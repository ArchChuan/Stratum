package domain

import "encoding/json"

// KnowledgeWorkspaceSnapshot 是知识库工作区可编辑面的版本化快照，写入通用产品
// 版本基座 resource_versions 的 payload，供版本历史展示与回滚重建。快照只含
// 工作区可编辑面（名称/描述/RAG 配置），不含 Milvus 向量数据、文档列表与文档
// 访问白名单（回滚不触碰它们）。id/created_by 等不可变字段不进快照。
//
// WorkspaceConfig 字段无显式 json tag，以 Go 字段名（PascalCase）序列化；
// Map() 经 encoding/json 对 map 键排序，输出确定，与 versioning/domain 的
// ComputeContentHash 配套。
type KnowledgeWorkspaceSnapshot struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Config      WorkspaceConfig `json:"config"`
}

// SnapshotFromWorkspace 捕获 ws 的可编辑面（Update 构建完成、校验通过后调用）。
func SnapshotFromWorkspace(ws *Workspace) KnowledgeWorkspaceSnapshot {
	if ws == nil {
		return KnowledgeWorkspaceSnapshot{}
	}
	return KnowledgeWorkspaceSnapshot{
		Name:        ws.Name,
		Description: ws.Description,
		Config:      ws.Config,
	}
}

// Map 渲染为 resource_versions.payload（snake_case 键，canonical JSON 可哈希）。
func (s KnowledgeWorkspaceSnapshot) Map() map[string]any {
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
func SnapshotFromMap(payload map[string]any) (KnowledgeWorkspaceSnapshot, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return KnowledgeWorkspaceSnapshot{}, err
	}
	var s KnowledgeWorkspaceSnapshot
	if err := json.Unmarshal(encoded, &s); err != nil {
		return KnowledgeWorkspaceSnapshot{}, err
	}
	return s, nil
}

// ToWorkspace 从快照重建 Workspace，供回滚写入与审计投影。id 保留资源标识；
// CreatedBy 留空（UPDATE/Rollback 的 SET 列不触碰 created_by，回滚审计投影
// 的 before 来自当前行）。
func (s KnowledgeWorkspaceSnapshot) ToWorkspace(id string) *Workspace {
	return &Workspace{
		ID:          id,
		Name:        s.Name,
		Description: s.Description,
		Config:      s.Config,
	}
}
