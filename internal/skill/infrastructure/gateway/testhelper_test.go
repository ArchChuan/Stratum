package gateway

import (
	"context"

	"go.uber.org/zap"
)

// mockProvider 用于测试的 mock SkillProvider
type mockProvider struct {
	skillID   string
	skillType string
	output    any
	err       error
	callCount int
}

func newMockProvider(id, typ string, output any, err error) *mockProvider {
	return &mockProvider{skillID: id, skillType: typ, output: output, err: err}
}

func (m *mockProvider) SkillIDs() []string { return []string{m.skillID} }
func (m *mockProvider) Has(id string) bool { return id == m.skillID }
func (m *mockProvider) SkillType() string  { return m.skillType }
func (m *mockProvider) Execute(_ context.Context, _ string, _ any) (any, error) {
	m.callCount++
	return m.output, m.err
}

func newTestEngine(provider SkillProvider) *atomicEngine {
	reg := newProviderRegistry()
	_ = reg.Register(provider)
	cb := newCircuitBreakerManager(*defaultCBConfig(), nil, zap.NewNop())
	return newAtomicEngine(reg, cb, nil, newAuditor(zap.NewNop()), zap.NewNop())
}

func testCtx() context.Context {
	return context.Background()
}
