package domain

import "errors"

// Sentinel errors shared across the Agent domain. Application aliases these
// where callers must preserve errors.Is checks across layers.
var (
	ErrNotFound                      = errors.New("agent not found")
	ErrNameConflict                  = errors.New("agent name already exists")
	ErrInvalidSkill                  = errors.New("skill not found")
	ErrForbidden                     = errors.New("resource ownership forbidden")
	ErrEditorNotEligible             = errors.New("editor must hold admin or owner role")
	ErrInvalidOfficialEvidenceQuery  = errors.New("official evidence query is empty")
	ErrOfficialEvidenceNotFound      = errors.New("official evidence not found")
	ErrDiagnosticForbidden           = errors.New("diagnostic forbidden")
	ErrDiagnosticEvidenceUnavailable = errors.New("diagnostic evidence unavailable")
	ErrKnowledgeRevisionUnavailable  = errors.New("knowledge revision unavailable")
	ErrAssistantModelUnavailable     = errors.New("system assistant model unavailable")
	ErrInvalidAgentModel             = errors.New("invalid system assistant model")
	ErrInvalidSamplingParameters     = errors.New("invalid sampling parameters")
	ErrInvalidMaxIterations          = errors.New("invalid max iterations")
	ErrProposalInvalid               = errors.New("proposal invalid")
	ErrProposalNotFound              = errors.New("proposal not found")
	ErrProposalStale                 = errors.New("proposal stale")
	ErrProposalExpired               = errors.New("proposal expired")
	ErrProposalForbidden             = errors.New("proposal forbidden")
	ErrProposalAlreadyClaimed        = errors.New("proposal already claimed")
	ErrProposalApplyFailed           = errors.New("proposal apply failed")
	ErrProposalUnknownOutcome        = errors.New("proposal outcome unknown")
	ErrOperationProposalNotFound     = errors.New("operation proposal not found")
	ErrOperationProposalResolved     = errors.New("operation proposal already resolved")
	ErrOperationProposalPending      = errors.New("operation proposal already pending")
	ErrOperationProposalExpired      = errors.New("operation proposal approval expired")
	// ErrPlatformManagedSkillBinding 标记"内置 skill 挂载到普通 agent"边界
	// (builtin: 前缀)。区别于 skill domain 的 ErrPlatformManagedSkill —— 后者
	// 守卫 skill 资源自身的写路径,这里守卫 agent 挂载关系,agent 不 import 兄弟 domain。
	ErrPlatformManagedSkillBinding = errors.New("platform-managed skill cannot be bound to a regular agent")
	// ErrPlatformManagedMCPServerBinding 标记"平台托管 MCP server 挂载到普通 agent"边界。
	ErrPlatformManagedMCPServerBinding = errors.New("platform-managed MCP server cannot be bound to a regular agent")
	// ErrSystemPromptNotConfigured 标记平台全局系统提示词 agent.system_prompt 未配置
	// （fail-closed：禁止空后缀静默执行）。错误文本保留 "not configured (fail-closed)"
	// 后缀以兼容既有日志检索；作为 sentinel 供 middleware 映射为可读中文。
	ErrSystemPromptNotConfigured = errors.New("agent.system_prompt not configured (fail-closed)")
	// ErrCompactionPromptNotConfigured 标记平台压缩提示词 agent.compaction_prompt 未配置
	// （fail-closed：禁止空 system prompt 静默调用 LLM）。
	ErrCompactionPromptNotConfigured = errors.New("agent.compaction_prompt not configured (fail-closed)")
)
