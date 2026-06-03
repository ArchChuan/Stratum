// Package skillgateway provides skill gateway and routing.
package skillgateway

import (
	"context"
	"fmt"
	"sync"
)

// SkillProvider 插件化 skill 提供者接口
type SkillProvider interface {
	// SkillIDs 返回该 provider 管理的所有 skill ID
	SkillIDs() []string
	// Execute 执行指定 skill
	Execute(ctx context.Context, skillID string, input any) (any, error)
	// Has 检查是否管理该 skill
	Has(skillID string) bool
	// SkillType 返回该 provider 的 skill 类型（用于 metrics label）
	SkillType() string
}

// ProviderRegistry 管理所有 SkillProvider，按 skill_id 路由
type ProviderRegistry struct {
	providers map[string]SkillProvider // skill_id -> provider
	mu        sync.RWMutex
}

func newProviderRegistry() *ProviderRegistry {
	return &ProviderRegistry{
		providers: make(map[string]SkillProvider),
	}
}

// Register 注册 provider 管理的所有 skill
func (r *ProviderRegistry) Register(provider SkillProvider) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, id := range provider.SkillIDs() {
		if _, exists := r.providers[id]; exists {
			return &SkillError{
				Code:    ErrSkillAlreadyExists,
				Message: fmt.Sprintf("skill already registered: %s", id),
			}
		}
	}
	for _, id := range provider.SkillIDs() {
		r.providers[id] = provider
	}
	return nil
}

// Resolve 查找 skill_id 对应的 provider
func (r *ProviderRegistry) Resolve(skillID string) (SkillProvider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[skillID]
	return p, ok
}

// TypeOf 返回 skill 的类型字符串，用于 metrics label
func (r *ProviderRegistry) TypeOf(skillID string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if p, ok := r.providers[skillID]; ok {
		return p.SkillType()
	}
	return "unknown"
}
