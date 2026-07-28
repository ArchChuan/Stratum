// Package seeds provides idempotent seed data for built-in platform resources.
package seeds

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/byteBuilderX/stratum/internal/skill/domain"
)

// BuiltinSkill holds the seed definition of a single built-in skill.
type BuiltinSkill struct {
	ID          string
	Name        string
	Description string
	Revision    domain.SkillRevision
}

// BuiltinSkills returns the current set of built-in skills.
// Each skill targets the system assistant agent and ships with a single
// published revision.
func BuiltinSkills() []BuiltinSkill {
	return []BuiltinSkill{
		platformGuide(),
		tenantDiagnostic(),
	}
}

func platformGuide() BuiltinSkill {
	rev := domain.SkillRevision{
		ID:                 "rev-builtin-platform-guide-v1",
		SkillID:            "builtin:platform-guide",
		ParentRevisionID:   "",
		RevisionNo:         1,
		Status:             domain.VersionStatusPublished,
		Source:             "manual",
		GenerationMetadata: map[string]any{},
		Capability: domain.Capability{
			Goal:       "基于官方资料回答 Stratum 平台使用问题",
			WhenToUse:  "用户询问平台功能、概念、使用方法、配置步骤时",
			InputSpec:  `{"question": "string"} — 用户问题`,
			OutputSpec: `{"answer": "string", "citations": ["string"]} — 带引用来源的答案`,
			Examples: []domain.CapabilityExample{{
				Input:          map[string]any{"question": "如何创建 Agent"},
				ExpectedOutput: map[string]any{"answer": "在 Agent 管理页面点击新建..."},
			}},
		},
		ActivationContract: domain.ActivationContract{
			Name:        "platform_guide",
			Description: "基于官方资料提供平台使用指导",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"question": map[string]any{"type": "string"}},
				"required":   []any{"question"},
			},
			OutputSchema: map[string]any{"type": "object"},
			Confirmed:    true,
		},
		Instructions: "先用 stratum_search_official_docs 检索官方资料。基于检索结果回答用户问题。" +
			"每条声明必须引用来源（文档标题 + section）。找不到资料时明确告知证据缺口，禁止编造。",
		Requirements: domain.Requirements{
			MCPToolIDs:            []string{},
			KnowledgeWorkspaceIDs: []string{},
			MemoryScopes:          []string{"conversation"},
		},
		PublishChecks: map[string]any{},
	}
	hash, err := rev.ComputeContentHash()
	if err != nil {
		panic(fmt.Sprintf("builtin skill platform-guide: compute content hash: %v", err))
	}
	rev.ContentHash = hash
	return BuiltinSkill{
		ID:          "builtin:platform-guide",
		Name:        "stratum-platform-guide",
		Description: "基于官方资料提供平台使用指导",
		Revision:    rev,
	}
}

func tenantDiagnostic() BuiltinSkill {
	rev := domain.SkillRevision{
		ID:                 "rev-builtin-tenant-diagnostic-v1",
		SkillID:            "builtin:tenant-diagnostic",
		ParentRevisionID:   "",
		RevisionNo:         1,
		Status:             domain.VersionStatusPublished,
		Source:             "manual",
		GenerationMetadata: map[string]any{},
		Capability: domain.Capability{
			Goal:       "诊断当前租户各模块运行状态",
			WhenToUse:  "用户询问系统状态、排查问题、检查配置时",
			InputSpec:  `{"area": "string"} — 可选，限定诊断范围`,
			OutputSpec: `{"status": "string", "modules": [...], "issues": [...]} — 诊断汇总`,
			Examples: []domain.CapabilityExample{{
				Input:          map[string]any{"area": "agent"},
				ExpectedOutput: map[string]any{"status": "healthy", "modules": []any{}},
			}},
		},
		ActivationContract: domain.ActivationContract{
			Name:        "diagnose_tenant",
			Description: "诊断当前租户各模块运行状态",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{"area": map[string]any{"type": "string"}},
			},
			OutputSchema: map[string]any{"type": "object"},
			Confirmed:    true,
		},
		Instructions: "调用 stratum_diagnose_tenant 收集各模块诊断证据。" +
			"汇总结果时严格分层：已确认事实（有证据支持）、推断（基于证据的合理推断）、证据缺口（无法获取或失败的检查项）。" +
			"禁止将证据缺口报告为系统正常。",
		Requirements: domain.Requirements{
			MCPToolIDs:            []string{},
			KnowledgeWorkspaceIDs: []string{},
			MemoryScopes:          []string{"conversation"},
		},
		PublishChecks: map[string]any{},
	}
	hash, err := rev.ComputeContentHash()
	if err != nil {
		panic(fmt.Sprintf("builtin skill tenant-diagnostic: compute content hash: %v", err))
	}
	rev.ContentHash = hash
	return BuiltinSkill{
		ID:          "builtin:tenant-diagnostic",
		Name:        "stratum-tenant-diagnostic",
		Description: "诊断当前租户各模块运行状态",
		Revision:    rev,
	}
}

// SkillSQL generates the tenant_schema.sql seed INSERT statements for all
// built-in skills, revisions, and agent bindings. All statements use
// ON CONFLICT DO NOTHING for idempotency.
func SkillSQL() string {
	var b strings.Builder
	b.WriteString("-- Builtin skills: platform guide + tenant diagnostic\n")

	for _, sk := range BuiltinSkills() {
		rev := sk.Revision
		capJSON := compactJSON(toMap(rev.Capability))
		contractJSON := compactJSON(toMap(rev.ActivationContract))
		reqJSON := compactJSON(toMap(rev.Requirements))
		publishChecks := compactJSON(rev.PublishChecks)
		if publishChecks == "" {
			publishChecks = "{}"
		}
		genMeta := compactJSON(rev.GenerationMetadata)
		if genMeta == "" {
			genMeta = "{}"
		}

		fmt.Fprintf(&b, `
INSERT INTO skills (id, name, description, status, active_revision_id, created_at, updated_at)
VALUES ('%s', '%s', '%s', 'published', '%s', NOW(), NOW())
ON CONFLICT (id) DO NOTHING;

INSERT INTO skill_revisions (
    id, skill_id, parent_revision_id, revision_no, status, source,
    content_hash, generation_metadata, capability, activation_contract,
    instructions, requirements, publish_checks, created_at, published_at
) VALUES (
    '%s', '%s', NULL, %d, 'published', 'manual',
    '%s', '%s'::jsonb, '%s'::jsonb, '%s'::jsonb,
    '%s', '%s'::jsonb, '%s'::jsonb, NOW(), NOW()
)
ON CONFLICT (id) DO NOTHING;

INSERT INTO agent_skill_links (agent_id, skill_id)
VALUES ('stratum-platform-assistant', '%s')
ON CONFLICT (agent_id, skill_id) DO NOTHING;
`,
			sk.ID, sk.Name, sk.Description, rev.ID,
			rev.ID, sk.ID, rev.RevisionNo,
			rev.ContentHash, genMeta, capJSON, contractJSON,
			escapeSQL(rev.Instructions), reqJSON, publishChecks,
			sk.ID,
		)
	}
	return b.String()
}

func toMap(v any) map[string]any {
	raw, err := json.Marshal(v)
	if err != nil {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return map[string]any{}
	}
	return m
}

func compactJSON(m map[string]any) string {
	if len(m) == 0 {
		return ""
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return ""
	}
	return string(raw)
}

func escapeSQL(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}
