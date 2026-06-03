// Package mcp provides MCP (Model Context Protocol) client implementation.
package mcp

import (
	"context"
	"fmt"
	"sync"

	"github.com/byteBuilderX/ClawHermes-AI-Go/internal/skill"
	"go.uber.org/zap"
)

// MCPSkillWrapper 将 MCP 工具包装为 Skill
type MCPSkillWrapper struct {
	ctx         context.Context
	ID          string
	Name        string
	Description string
	Type        string
	Tool        *MCPTool
	ServerID    string
	Manager     *ClientManager
	logger      *zap.Logger
}

// GetID 获取 ID
func (w *MCPSkillWrapper) GetID() string {
	return w.ID
}

// GetName 获取名称
func (w *MCPSkillWrapper) GetName() string {
	return w.Name
}

// GetDescription 获取描述
func (w *MCPSkillWrapper) GetDescription() string {
	return w.Description
}

// GetType 获取类型
func (w *MCPSkillWrapper) GetType() string {
	return w.Type
}

// Execute 执行工具
func (w *MCPSkillWrapper) Execute(input any) (any, error) {
	ctx := w.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	result, err := w.Manager.CallTool(ctx, w.ServerID, w.Tool.Name, input)
	if err != nil {
		w.logger.Error("failed to execute MCP tool",
			zap.String("tool", w.Tool.Name),
			zap.String("server_id", w.ServerID),
			zap.Error(err))
		return nil, err
	}

	return result, nil
}

// MCPSkillAdapter 适配器，管理 MCP Skills
type MCPSkillAdapter struct {
	serverID string
	manager  *ClientManager
	skills   map[string]*MCPSkillWrapper
	mu       sync.RWMutex
	logger   *zap.Logger
}

// NewMCPSkillAdapter 创建新的适配器
func NewMCPSkillAdapter(serverID string, manager *ClientManager, logger *zap.Logger) *MCPSkillAdapter {
	return &MCPSkillAdapter{
		serverID: serverID,
		manager:  manager,
		skills:   make(map[string]*MCPSkillWrapper),
		logger:   logger.Named("mcp.skill_adapter").With(zap.String("server_id", serverID)),
	}
}

// DiscoverSkills 发现并创建 Skills
func (a *MCPSkillAdapter) DiscoverSkills(ctx context.Context) ([]*MCPSkillWrapper, error) {
	tools, err := a.manager.ListTools(ctx, a.serverID)
	if err != nil {
		return nil, err
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	var skills []*MCPSkillWrapper
	for _, tool := range tools {
		skillID := fmt.Sprintf("mcp:%s:%s", a.serverID, tool.Name)

		wrapper := &MCPSkillWrapper{
			ctx:         ctx,
			ID:          skillID,
			Name:        tool.Name,
			Description: tool.Description,
			Type:        "mcp",
			Tool:        tool,
			ServerID:    a.serverID,
			Manager:     a.manager,
			logger:      a.logger,
		}

		a.skills[skillID] = wrapper
		skills = append(skills, wrapper)
	}

	a.logger.Info("discovered MCP skills", zap.Int("count", len(skills)))
	return skills, nil
}

// GetSkill 获取 Skill
func (a *MCPSkillAdapter) GetSkill(skillID string) skill.Skill {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if wrapper, exists := a.skills[skillID]; exists {
		return wrapper
	}
	return nil
}

// GetAllSkills 获取所有 Skills
func (a *MCPSkillAdapter) GetAllSkills() []skill.Skill {
	a.mu.RLock()
	defer a.mu.RUnlock()

	var skills []skill.Skill
	for _, wrapper := range a.skills {
		skills = append(skills, wrapper)
	}
	return skills
}

// MCPSkillRegistry 管理所有 MCP Skills
type MCPSkillRegistry struct {
	adapters map[string]*MCPSkillAdapter
	manager  *ClientManager
	mu       sync.RWMutex
	logger   *zap.Logger
}

// NewMCPSkillRegistry 创建新的注册表
func NewMCPSkillRegistry(manager *ClientManager, logger *zap.Logger) *MCPSkillRegistry {
	return &MCPSkillRegistry{
		adapters: make(map[string]*MCPSkillAdapter),
		manager:  manager,
		logger:   logger.Named("mcp.skill_registry"),
	}
}

// RegisterServer 注册 MCP 服务器
func (r *MCPSkillRegistry) RegisterServer(ctx context.Context, serverID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.adapters[serverID]; exists {
		return fmt.Errorf("server already registered: %s", serverID)
	}

	adapter := NewMCPSkillAdapter(serverID, r.manager, r.logger)

	// 发现 Skills
	_, err := adapter.DiscoverSkills(ctx)
	if err != nil {
		return err
	}

	r.adapters[serverID] = adapter
	r.logger.Info("registered MCP server", zap.String("server_id", serverID))

	return nil
}

// UnregisterServer 注销 MCP 服务器
func (r *MCPSkillRegistry) UnregisterServer(serverID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.adapters[serverID]; !exists {
		return fmt.Errorf("server not found: %s", serverID)
	}

	delete(r.adapters, serverID)
	r.logger.Info("unregistered MCP server", zap.String("server_id", serverID))

	return nil
}

// GetSkill 获取 Skill
func (r *MCPSkillRegistry) GetSkill(skillID string) skill.Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, adapter := range r.adapters {
		if s := adapter.GetSkill(skillID); s != nil {
			return s
		}
	}
	return nil
}

// GetAllSkills 获取所有 Skills
func (r *MCPSkillRegistry) GetAllSkills() []skill.Skill {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var skills []skill.Skill
	for _, adapter := range r.adapters {
		skills = append(skills, adapter.GetAllSkills()...)
	}
	return skills
}

// ExecuteSkill 执行 Skill
func (r *MCPSkillRegistry) ExecuteSkill(skillID string, input any) (any, error) {
	s := r.GetSkill(skillID)
	if s == nil {
		return nil, fmt.Errorf("skill not found: %s", skillID)
	}

	if executor, ok := s.(skill.SkillExecutor); ok {
		return executor.Execute(input)
	}

	return nil, fmt.Errorf("skill is not executable: %s", skillID)
}

// RefreshSkills 刷新 Skills
func (r *MCPSkillRegistry) RefreshSkills(ctx context.Context) error {
	r.mu.RLock()
	adapters := make(map[string]*MCPSkillAdapter)
	for k, v := range r.adapters {
		adapters[k] = v
	}
	r.mu.RUnlock()

	for serverID, adapter := range adapters {
		_, err := adapter.DiscoverSkills(ctx)
		if err != nil {
			r.logger.Warn("failed to refresh skills",
				zap.String("server_id", serverID),
				zap.Error(err))
		}
	}

	return nil
}

// GetServerInfo 获取服务器信息
func (r *MCPSkillRegistry) GetServerInfo(serverID string) any {
	return r.manager.GetServerInfo(serverID)
}

// GetAllServerInfo 获取所有服务器信息
func (r *MCPSkillRegistry) GetAllServerInfo() []*MCPServerInfo {
	return r.manager.GetAllServerInfo()
}
