// Package application implements MCP bounded context use-cases.
package application

import (
	"context"
	"errors"
	"fmt"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	auditport "github.com/byteBuilderX/stratum/internal/audit/domain/port"
	"github.com/byteBuilderX/stratum/internal/mcp/domain"
	"github.com/byteBuilderX/stratum/internal/mcp/domain/port"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
	"go.uber.org/zap"
)

// ServerStatusBreakdown summarises connection state across all known servers.
type ServerStatusBreakdown struct {
	Total        int `json:"total"`
	Connected    int `json:"connected"`
	Disconnected int `json:"disconnected"`
	Error        int `json:"error"`
}

// QuotaResponse is the shape returned by GET /mcp/quota.
type QuotaResponse struct {
	TenantID string `json:"tenant_id"`
	Used     int    `json:"used"`
	Limit    int    `json:"limit"`
	Healthy  int    `json:"healthy"`
	Dead     int    `json:"dead"`
}

// MCPService orchestrates MCP HTTP use-cases on top of port interfaces.
type MCPService struct {
	toolRegistry port.ToolRegistry
	manager      port.ServerManager
	toolPolicies port.ToolPolicyRepo
	roles        port.TenantRoleResolver
	failureAudit auditport.FailureAuditRecorder
	logger       *zap.Logger
}

func (s *MCPService) SetToolPolicyRepo(repo port.ToolPolicyRepo) { s.toolPolicies = repo }

// SetTenantRoleResolver injects the tenant role resolver used by ownership
// checks. A nil resolver fails all writes closed (ownership unverifiable).
func (s *MCPService) SetTenantRoleResolver(r port.TenantRoleResolver) { s.roles = r }

// SetFailureAuditRecorder 注入失败资源操作审计。未注入时失败记录跳过
// （不影响主流程错误）。
func (s *MCPService) SetFailureAuditRecorder(r auditport.FailureAuditRecorder) { s.failureAudit = r }

func (s *MCPService) GetToolRisk(ctx context.Context, serverID, toolName string) (domain.ToolRiskLevel, error) {
	if s.toolPolicies == nil {
		return domain.ToolRiskUnclassified, nil
	}
	policy, ok, err := s.toolPolicies.Get(ctx, serverID, toolName)
	if err != nil || !ok {
		return domain.ToolRiskUnclassified, err
	}
	return policy.RiskLevel, nil
}

func (s *MCPService) ListToolPolicies(ctx context.Context) ([]domain.ToolPolicy, error) {
	if s.toolPolicies == nil {
		return []domain.ToolPolicy{}, nil
	}
	return s.toolPolicies.List(ctx)
}

func (s *MCPService) SetToolPolicy(ctx context.Context, policy domain.ToolPolicy) error {
	if err := policy.RiskLevel.Validate(); err != nil {
		return err
	}
	if policy.ServerID == "" || policy.ToolName == "" {
		return errors.New("serverId and toolName are required")
	}
	if s.toolPolicies == nil {
		return errors.New("MCP tool policy repository not configured")
	}
	return s.toolPolicies.Upsert(ctx, policy)
}

// NewMCPService wires the dependencies. Both registry and manager are required.
func NewMCPService(toolRegistry port.ToolRegistry, manager port.ServerManager, logger *zap.Logger) *MCPService {
	return &MCPService{
		toolRegistry: toolRegistry,
		manager:      manager,
		logger:       logger.Named("mcp.service"),
	}
}

// ListServers returns metadata for every known MCP server.
func (s *MCPService) ListServers(ctx context.Context) []*domain.ServerInfo {
	return s.manager.GetAllServerInfo(ctx)
}

// GetServer returns server info for id, or domain.ErrServerNotFound when absent.
func (s *MCPService) GetServer(ctx context.Context, id string) (*domain.ServerInfo, error) {
	info := s.manager.GetServerInfo(ctx, id)
	if info == nil {
		return nil, domain.ErrServerNotFound
	}
	return info, nil
}

// ListTools fetches the live tool catalogue for serverID.
func (s *MCPService) ListTools(ctx context.Context, serverID string) ([]*domain.Tool, error) {
	return s.manager.ListTools(ctx, serverID)
}

// ListResources fetches the live resource catalogue for serverID.
func (s *MCPService) ListResources(ctx context.Context, serverID string) ([]*domain.Resource, error) {
	return s.manager.ListResources(ctx, serverID)
}

// GetQuota returns per-tenant connection accounting.
func (s *MCPService) GetQuota(ctx context.Context) QuotaResponse {
	q := s.manager.Quota(ctx)
	return QuotaResponse{
		TenantID: q.TenantID,
		Used:     q.Used,
		Limit:    q.Limit,
		Healthy:  q.Healthy,
		Dead:     q.Dead,
	}
}

// ServerStatus aggregates connection counts across all servers.
func (s *MCPService) ServerStatus(ctx context.Context) ServerStatusBreakdown {
	servers := s.manager.GetAllServerInfo(ctx)
	out := ServerStatusBreakdown{Total: len(servers)}
	for _, srv := range servers {
		switch srv.Status {
		case "connected":
			out.Connected++
		case "disconnected":
			out.Disconnected++
		case "error":
			out.Error++
		}
	}
	return out
}

// ConnectServer registers a new MCP server config and discovers its tools.
// An existing id takes update semantics (upsert) and keeps the original
// creator. Returns domain.ErrNameConflict on duplicate name. editors carries
// the granted editor set on the create path (written in the same transaction,
// each editor must hold role admin/owner); update paths ignore it.
func (s *MCPService) ConnectServer(ctx context.Context, cfg *domain.ServerConfig, editors []string, actorID string) error {
	// UX 纵深：服务端 stdio 全链禁用，权威拒绝点在内层 client.doConnect
	// （覆盖 5 条连接路径）。这里提前拒绝让写接口直接返回 400，避免租户
	// 写入一个必然连不上的配置；存量 stdio 行改写成 HTTP 不受影响。
	if cfg.Transport == "stdio" {
		return domain.ErrUnsupportedTransport
	}
	stored, getErr := s.manager.GetServerConfig(ctx, cfg.ID)
	if getErr != nil && !errors.Is(getErr, domain.ErrServerNotFound) {
		return getErr
	}
	op, editorActor, before, err := s.resolveConnectOp(ctx, cfg, stored, getErr, actorID)
	if err != nil {
		return err
	}
	audit, err := newChangeAudit(ctx, auditdomain.ResourceKindMCP, cfg.ID, op, actorID, before, MCPSafeProjection(cfg))
	if err != nil {
		return err
	}
	if err := s.manager.Connect(ctx, cfg, editors, editorActor, audit); err != nil {
		s.recordConnectFailure(ctx, cfg, "connect", err)
		return err
	}
	s.logger.Info("mcp.server_connected",
		zap.String("server_id", cfg.ID),
		zap.String("server_name", cfg.Name),
	)
	if err := s.toolRegistry.RegisterServer(ctx, reqctx.TenantIDFromContext(ctx), cfg.ID); err != nil {
		s.logger.Warn("failed to register MCP tools", zap.String("server_id", cfg.ID), zap.Error(err))
	}
	return nil
}

// recordConnectFailure 旁路记录一次失败的 MCP 连接/更新（best-effort）。
// 分类只写短错误码，detail 不携带 URL 或凭据；记录失败仅 WARN 不阻断主错误。
func (s *MCPService) recordConnectFailure(ctx context.Context, cfg *domain.ServerConfig, op string, err error) {
	if s.failureAudit == nil {
		return
	}
	code := auditport.ClassifyFailure(err)
	if errors.Is(err, domain.ErrTransportFailed) {
		code = "transport"
	}
	if recordErr := s.failureAudit.Record(ctx, auditport.ResourceFailure{
		ResourceKind: auditdomain.ResourceKindMCP,
		ResourceID:   cfg.ID,
		Operation:    op,
		ErrorCode:    code,
	}); recordErr != nil {
		s.logger.Warn("failed to record MCP failure audit",
			zap.String("server_id", cfg.ID),
			zap.String("op", op),
			zap.Error(recordErr))
	}
}

// resolveConnectOp decides the upsert semantics of ConnectServer: existing
// rows take update semantics (owner, or a granted editor — editors never grant
// delete rights) keeping the original creator, and new rows make the creator
// the owner after an ownership check. It returns the audit op, the
// update-path editor, and the before-projection for the audit trail (untyped
// nil for create, so the audit layer records no before-state).
func (s *MCPService) resolveConnectOp(ctx context.Context, cfg, stored *domain.ServerConfig, getErr error, actorID string) (string, string, any, error) {
	op := auditdomain.ChangeOpCreate
	switch getErr {
	case nil:
		ea, err := s.resolveUpdateActor(ctx, actorID, stored)
		if err != nil {
			return op, "", nil, err
		}
		cfg.CreatedBy = stored.CreatedBy
		return auditdomain.ChangeOpUpdate, ea, MCPSafeProjection(stored), nil
	default:
		// New server: creator becomes owner; only owner/admin may create.
		if err := s.checkOwnership(ctx, actorID, actorID, nil); err != nil {
			return op, "", nil, err
		}
		cfg.CreatedBy = actorID
		return op, "", nil, nil
	}
}

// DeleteServer permanently removes an MCP server config and cascades to agent relations.
func (s *MCPService) DeleteServer(ctx context.Context, serverID, actorID string) error {
	stored, err := s.loadManagedServer(ctx, serverID)
	if err != nil {
		return err
	}
	if err := s.checkOwnership(ctx, actorID, stored.CreatedBy, nil); err != nil {
		return err
	}
	audit, err := newChangeAudit(ctx, auditdomain.ResourceKindMCP, serverID, auditdomain.ChangeOpDelete, actorID,
		MCPSafeProjection(stored), nil)
	if err != nil {
		return err
	}
	if err := s.manager.Delete(ctx, serverID, audit); err != nil {
		return err
	}
	if err := s.toolRegistry.UnregisterServer(reqctx.TenantIDFromContext(ctx), serverID); err != nil {
		return err
	}
	s.logger.Info("mcp.server_deleted", zap.String("server_id", serverID))
	return nil
}

// DisconnectServer drops the connection to serverID (no audit: connection
// state is not a configuration change). Only the creator/owner may drop the
// connection.
func (s *MCPService) DisconnectServer(ctx context.Context, serverID, actorID string) error {
	stored, err := s.loadManagedServer(ctx, serverID)
	if err != nil {
		return err
	}
	if err := s.checkOwnership(ctx, actorID, stored.CreatedBy, nil); err != nil {
		return err
	}
	if err := s.manager.Disconnect(ctx, serverID); err != nil {
		return err
	}
	s.logger.Info("mcp.server_disconnected", zap.String("server_id", serverID))
	return nil
}

// ReconnectServer restores a previously disconnected MCP server. Only the
// creator/owner may reconnect.
func (s *MCPService) ReconnectServer(ctx context.Context, serverID, actorID string) error {
	stored, err := s.loadManagedServer(ctx, serverID)
	if err != nil {
		return err
	}
	if err := s.checkOwnership(ctx, actorID, stored.CreatedBy, nil); err != nil {
		return err
	}
	if err := s.manager.Reconnect(ctx, serverID); err != nil {
		return err
	}
	s.logger.Info("mcp.server_reconnected", zap.String("server_id", serverID))
	if err := s.toolRegistry.RegisterServer(ctx, reqctx.TenantIDFromContext(ctx), serverID); err != nil {
		s.logger.Warn("failed to register MCP tools after reconnect", zap.String("server_id", serverID), zap.Error(err))
	}
	return nil
}

// UpdateServer disconnects and reconnects an existing MCP server with new config.
func (s *MCPService) UpdateServer(ctx context.Context, cfg *domain.ServerConfig, actorID string) error {
	// 与 ConnectServer 一致：incoming stdio 一律拒绝（存量 stdio 行改写为
	// HTTP 是受支持的修复路径，incoming 非 stdio 不受影响）。
	if cfg.Transport == "stdio" {
		return domain.ErrUnsupportedTransport
	}
	stored, err := s.manager.GetServerConfig(ctx, cfg.ID)
	if err != nil {
		return err
	}
	editorActor, err := s.resolveUpdateActor(ctx, actorID, stored)
	if err != nil {
		return err
	}
	merged := mergeProtectedConfig(stored, cfg)
	merged.CreatedBy = stored.CreatedBy
	audit, err := newChangeAudit(ctx, auditdomain.ResourceKindMCP, cfg.ID, auditdomain.ChangeOpUpdate, actorID,
		MCPSafeProjection(stored), MCPSafeProjection(merged))
	if err != nil {
		return err
	}
	if err := s.manager.UpdateServer(ctx, merged, editorActor, audit); err != nil {
		s.recordConnectFailure(ctx, merged, "update", err)
		return err
	}
	s.logger.Info("mcp.server_updated", zap.String("server_id", cfg.ID))
	if err := s.toolRegistry.RegisterServer(ctx, reqctx.TenantIDFromContext(ctx), cfg.ID); err != nil {
		s.logger.Warn("failed to re-register MCP tools", zap.String("server_id", cfg.ID), zap.Error(err))
	}
	return nil
}

// SetEditors replaces the granted editor set of an MCP server. Only the
// creator or an owner may manage editors (an editor cannot delegate their own
// right); each editor must hold role admin/owner at write time, enforced
// inside the repository transaction (fail closed). The change is audited in
// the same transaction with before/after projections.
func (s *MCPService) SetEditors(ctx context.Context, serverID, actorID string, editorIDs []string) error {
	stored, err := s.loadManagedServer(ctx, serverID)
	if err != nil {
		return err
	}
	// Editors can never grant delete rights, so SetEditors reuses the
	// creator/owner-only base matrix.
	if err := s.checkOwnership(ctx, actorID, stored.CreatedBy, nil); err != nil {
		return err
	}
	tenantID := reqctx.TenantIDFromContext(ctx)
	before, err := s.manager.ListEditors(ctx, tenantID, serverID)
	if err != nil {
		return fmt.Errorf("mcp service set editors: list editors: %w", err)
	}
	audit, err := newChangeAudit(ctx, auditdomain.ResourceKindMCP, serverID, auditdomain.ChangeOpUpdate, actorID,
		MCPSafeProjectionWithEditors(stored, before), MCPSafeProjectionWithEditors(stored, editorIDs))
	if err != nil {
		return err
	}
	if err := s.manager.ReplaceEditors(ctx, tenantID, serverID, editorIDs, actorID, audit); err != nil {
		return err
	}
	s.logger.Info("mcp editors updated", zap.String("server_id", serverID), zap.Int("count", len(editorIDs)))
	return nil
}

// resolveUpdateActor returns the actor id when they are a granted editor of
// the resource (update-only), or "" when they are the creator or an owner.
// Any other actor gets domain.ErrForbidden.
func (s *MCPService) resolveUpdateActor(ctx context.Context, actorID string, current *domain.ServerConfig) (string, error) {
	if err := s.checkOwnership(ctx, actorID, current.CreatedBy, nil); err == nil {
		return "", nil
	}
	tenantID := reqctx.TenantIDFromContext(ctx)
	editors, err := s.manager.ListEditors(ctx, tenantID, current.ID)
	if err != nil {
		return "", err
	}
	if err := s.checkOwnership(ctx, actorID, current.CreatedBy, editors); err != nil {
		return "", err
	}
	return actorID, nil
}

// loadManagedServer fetches a server config (platform-managed 无特判, P4).
func (s *MCPService) loadManagedServer(ctx context.Context, serverID string) (*domain.ServerConfig, error) {
	stored, err := s.manager.GetServerConfig(ctx, serverID)
	if err != nil {
		return nil, err
	}
	return stored, nil
}

func mergeProtectedConfig(stored, incoming *domain.ServerConfig) *domain.ServerConfig {
	merged := cloneServerConfig(incoming)
	if stored == nil {
		return merged
	}
	// stdio 已禁用（service 层 + doConnect 双重拒绝），stdio 的 env 敏感值
	// 合并分支随之失效；HTTP transport 下只剩 headers 合并。
	if stored.Transport == incoming.Transport {
		mergeSensitiveValues(merged.Headers, stored.Headers)
	}
	if stored.Auth == nil || merged.Auth == nil || stored.Auth.Type != merged.Auth.Type {
		return merged
	}
	switch merged.Auth.Type {
	case domain.AuthTypeBearer:
		if merged.Auth.Token == "" {
			merged.Auth.Token = stored.Auth.Token
		}
	case domain.AuthTypeAPIKey:
		if merged.Auth.APIKeyValue == "" {
			merged.Auth.APIKeyValue = stored.Auth.APIKeyValue
		}
	case domain.AuthTypeOAuth2:
		if merged.Auth.OAuth2ClientSecret == "" {
			merged.Auth.OAuth2ClientSecret = stored.Auth.OAuth2ClientSecret
		}
	}
	return merged
}

func cloneServerConfig(cfg *domain.ServerConfig) *domain.ServerConfig {
	cloned := *cfg
	cloned.Args = append([]string(nil), cfg.Args...)
	cloned.Capabilities = append([]string(nil), cfg.Capabilities...)
	cloned.Env = cloneStringMap(cfg.Env)
	cloned.Headers = cloneStringMap(cfg.Headers)
	if cfg.Auth != nil {
		auth := *cfg.Auth
		auth.OAuth2Scopes = append([]string(nil), cfg.Auth.OAuth2Scopes...)
		cloned.Auth = &auth
	}
	if cfg.Retry != nil {
		retry := *cfg.Retry
		cloned.Retry = &retry
	}
	return &cloned
}

func cloneStringMap(values map[string]string) map[string]string {
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func mergeSensitiveValues(target, stored map[string]string) {
	for key, value := range stored {
		if _, supplied := target[key]; !supplied && domain.IsSensitiveConfigKey(key) {
			target[key] = value
		}
	}
}

// GetServerConfig returns the full configuration for serverID.
func (s *MCPService) GetServerConfig(ctx context.Context, serverID string) (*domain.ServerConfig, error) {
	return s.manager.GetServerConfig(ctx, serverID)
}

// ListEditors returns the granted editor set of an MCP server.
func (s *MCPService) ListEditors(ctx context.Context, tenantID, serverID string) ([]string, error) {
	return s.manager.ListEditors(ctx, tenantID, serverID)
}

// IsNameConflict reports whether err is the canonical mcp name-conflict sentinel.
func IsNameConflict(err error) bool {
	return errors.Is(err, domain.ErrNameConflict)
}
