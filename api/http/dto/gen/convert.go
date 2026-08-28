package gen

import (
	"fmt"

	agentdomain "github.com/byteBuilderX/stratum/internal/agent/domain"
	collabdomain "github.com/byteBuilderX/stratum/internal/collab/domain"
	"github.com/byteBuilderX/stratum/internal/mcp/domain"
	scheddomain "github.com/byteBuilderX/stratum/internal/scheduler/domain"
)

// ToCollabResponse 与手写 dto.ToCollabResponse 逐行一致(迁移保留)。
func ToCollabResponse(c collabdomain.Collaboration) CollabResponse {
	return CollabResponse{
		ID:              c.ID,
		TaskDescription: c.TaskDescription,
		Strategy:        string(c.Strategy),
		Status:          string(c.Status),
		CreatedBy:       c.CreatedBy,
		Participants:    c.Participants,
		CreatedAt:       c.CreatedAt,
		StartedAt:       c.StartedAt,
		CompletedAt:     c.CompletedAt,
	}
}

// ToTaskStepResponse 与手写 dto.ToTaskStepResponse 逐行一致(迁移保留)。
func ToTaskStepResponse(s collabdomain.TaskStep) TaskStepResponse {
	return TaskStepResponse{
		ID:           s.ID,
		PlanID:       s.PlanID,
		AgentID:      s.AgentID,
		Dependencies: s.Dependencies,
		Status:       string(s.Status),
		Input:        s.Input,
		Output:       s.Output,
		Error:        s.Error,
		CreatedAt:    s.CreatedAt,
	}
}

// ServerConfig 与手写 dto.(MCPServerConfigRequest).ServerConfig 逐行一致(迁移保留)。
// system_key 已随契约删除：外部提交被 json.Unmarshal 静默忽略（列同时删除，无处落库）。
func (r MCPServerConfigRequest) ServerConfig() (*domain.ServerConfig, error) {
	return &domain.ServerConfig{
		ID: r.ID, Name: r.Name, Version: r.Version, Transport: r.Transport, Command: r.Command,
		Args: r.Args, URL: r.URL, Env: r.Env, Headers: r.Headers, Capabilities: r.Capabilities,
		Timeout: r.Timeout, Auth: r.Auth, Retry: r.Retry,
	}, nil
}

// NewMCPServerConfigResponse 与手写 dto.NewMCPServerConfigResponse 逐行一致
// (迁移保留,含 filterMCPConfigValues/authCredentialConfigured)。
func NewMCPServerConfigResponse(cfg *domain.ServerConfig) MCPServerConfigResponse {
	response := MCPServerConfigResponse{
		ID:           cfg.ID,
		Name:         cfg.Name,
		Version:      cfg.Version,
		Transport:    cfg.Transport,
		Command:      cfg.Command,
		Args:         append([]string(nil), cfg.Args...),
		URL:          cfg.URL,
		Env:          filterMCPConfigValues(cfg.Env),
		Headers:      filterMCPConfigValues(cfg.Headers),
		Capabilities: append([]string(nil), cfg.Capabilities...),
		Timeout:      cfg.Timeout,
		Retry:        cfg.Retry,
	}
	if cfg.Auth != nil {
		response.Auth = &MCPAuthConfigResponse{
			Type:                 cfg.Auth.Type,
			APIKeyHeader:         cfg.Auth.APIKeyHeader,
			OAuth2ClientID:       cfg.Auth.OAuth2ClientID,
			OAuth2TokenURL:       cfg.Auth.OAuth2TokenURL,
			OAuth2Scopes:         append([]string(nil), cfg.Auth.OAuth2Scopes...),
			CredentialConfigured: authCredentialConfigured(cfg.Auth),
		}
	}
	return response
}

// IsSensitiveMCPConfigKey 与手写 dto.IsSensitiveMCPConfigKey 逐行一致(迁移保留)。
func IsSensitiveMCPConfigKey(key string) bool {
	return domain.IsSensitiveConfigKey(key)
}

// filterMCPConfigValues 与手写 dto.filterMCPConfigValues 逐行一致(迁移保留)。
func filterMCPConfigValues(values map[string]string) map[string]string {
	filtered := make(map[string]string)
	for key, value := range values {
		if !IsSensitiveMCPConfigKey(key) {
			filtered[key] = value
		}
	}
	return filtered
}

// authCredentialConfigured 与手写 dto.authCredentialConfigured 逐行一致(迁移保留)。
func authCredentialConfigured(auth *domain.AuthConfig) bool {
	switch auth.Type {
	case domain.AuthTypeBearer:
		return auth.Token != ""
	case domain.AuthTypeAPIKey:
		return auth.APIKeyValue != ""
	case domain.AuthTypeOAuth2:
		return auth.OAuth2ClientSecret != ""
	default:
		return false
	}
}

// RunInput 与手写 dto.(StartWorkflowRunRequest).RunInput 逐行一致(迁移保留,
// 含 fields.task 保留字校验)。
func (r StartWorkflowRunRequest) RunInput() (map[string]any, error) {
	if _, exists := r.Fields["task"]; exists {
		return nil, fmt.Errorf("fields.task is reserved")
	}
	input := make(map[string]any, len(r.Fields)+1)
	input["task"] = r.Task
	for key, value := range r.Fields {
		input[key] = value
	}
	return input, nil
}

// ToScheduledTaskResponse 与手写 dto.ToScheduledTaskResponse 逐行一致(迁移保留)。
func ToScheduledTaskResponse(t scheddomain.ScheduledTask) ScheduledTaskResponse {
	return ScheduledTaskResponse{
		ID:               t.ID,
		Name:             t.Name,
		WorkflowID:       t.WorkflowID,
		VersionID:        t.VersionID,
		InputTemplate:    t.InputTemplate,
		CronExpr:         t.CronExpr,
		Enabled:          t.Enabled,
		NextFireAt:       t.NextFireAt,
		LastRunAt:        t.LastRunAt,
		LastRunStatus:    t.LastRunStatus,
		LastErrorMessage: t.LastErrorMessage,
		CreatedBy:        t.CreatedBy,
		CreatedAt:        t.CreatedAt,
		UpdatedAt:        t.UpdatedAt,
		WorkflowName:     t.WorkflowName,
		VersionNo:        t.VersionNo,
		VersionName:      t.VersionName,
	}
}

// ToOperationProposalResponse 与手写 dto.ToOperationProposalResponse 逐行一致(迁移保留)。
func ToOperationProposalResponse(p agentdomain.OperationProposal) OperationProposalResponse {
	return OperationProposalResponse{
		ID:              p.ID,
		AgentID:         p.AgentID,
		TargetAgentID:   p.TargetAgentID,
		OpType:          p.OpType,
		Delegation:      p.Delegation,
		MaxDailyCostUSD: p.MaxDailyCostUSD,
		//nolint:gosec // MaxDailyExecutions 是配置上限(用户设置的每日执行配额),不可能溢出 int32(proto 契约)
		MaxDailyExecutions: int32(p.MaxDailyExecutions), // domain int → gen int32
		PayloadSummary:     p.PayloadSummary,
		Status:             p.Status,
		ProposerID:         p.ProposerID,
		ReviewedBy:         p.ReviewedBy,
		ReviewNote:         p.ReviewNote,
		CreatedAt:          p.CreatedAt,
		ResolvedAt:         p.ResolvedAt,
		ExpiresAt:          p.ExpiresAt,
	}
}

// NewResourceChangeProposalResponse 与手写 dto.NewResourceChangeProposalResponse
// 逐行一致(迁移保留,含 OperationCreate 时 baselineProjection 置 nil 的业务逻辑)。
func NewResourceChangeProposalResponse(
	proposal agentdomain.ResourceChangeProposal,
	events []agentdomain.ProposalEvent,
) ResourceChangeProposalResponse {
	if events == nil {
		events = []agentdomain.ProposalEvent{}
	}
	baselineProjection := proposal.BaselineProjection
	if proposal.Operation == agentdomain.OperationCreate {
		baselineProjection = nil
	}
	return ResourceChangeProposalResponse{
		ID: proposal.ID, ConversationID: proposal.ConversationID, ProposerID: proposal.ProposerID,
		ConfirmerID: proposal.ConfirmerID, ResourceKind: proposal.ResourceKind, ResourceID: proposal.ResourceID,
		Operation: proposal.Operation, BaselineFingerprint: proposal.BaselineFingerprint,
		BaselineProjection: baselineProjection,
		Payload:            proposal.Payload, Summary: proposal.Summary, Status: proposal.Status,
		ErrorCode: proposal.ErrorCode, ApplyResult: proposal.ApplyResult, Events: events,
		//nolint:gosec // EditCount 是业务计数器,不可能溢出 int32(proto 契约)
		EditCount: int32(proposal.EditCount), // domain int → gen int32
		ExpiresAt: proposal.ExpiresAt, CreatedAt: proposal.CreatedAt, UpdatedAt: proposal.UpdatedAt,
	}
}
