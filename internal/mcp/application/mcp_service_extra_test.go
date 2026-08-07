package application

import (
	"context"
	"errors"
	"testing"

	"github.com/byteBuilderX/stratum/internal/mcp/domain"
	"go.uber.org/zap"
)

// queryManagerFake 嵌入 lifecycleManagerFake，补充只读查询脚本化。
type queryManagerFake struct {
	*lifecycleManagerFake
	infos     []*domain.ServerInfo
	infoErr   error
	tools     []*domain.Tool
	toolsErr  error
	resources []*domain.Resource
	resErr    error
	quota     domain.Quota
}

func (f *queryManagerFake) GetAllServerInfo(context.Context) []*domain.ServerInfo { return f.infos }
func (f *queryManagerFake) GetServerInfo(_ context.Context, id string) *domain.ServerInfo {
	if f.infoErr != nil {
		return nil
	}
	for _, info := range f.infos {
		if info.ID == id {
			return info
		}
	}
	return nil
}
func (f *queryManagerFake) ListTools(context.Context, string) ([]*domain.Tool, error) {
	return f.tools, f.toolsErr
}
func (f *queryManagerFake) ListResources(context.Context, string) ([]*domain.Resource, error) {
	return f.resources, f.resErr
}
func (f *queryManagerFake) Quota(context.Context) domain.Quota { return f.quota }

// toolPolicyStub 记录策略调用。
type toolPolicyStub struct {
	policy    domain.ToolPolicy
	found     bool
	getErr    error
	upserted  []domain.ToolPolicy
	upsertErr error
}

func (s *toolPolicyStub) Get(_ context.Context, _, _ string) (domain.ToolPolicy, bool, error) {
	return s.policy, s.found, s.getErr
}
func (s *toolPolicyStub) List(context.Context) ([]domain.ToolPolicy, error) {
	return nil, nil
}
func (s *toolPolicyStub) Upsert(_ context.Context, p domain.ToolPolicy) error {
	s.upserted = append(s.upserted, p)
	return s.upsertErr
}

func TestMCPServiceListAndGetServers(t *testing.T) {
	// 列表转发 + 单查命中/未命中。
	mgr := &queryManagerFake{
		lifecycleManagerFake: &lifecycleManagerFake{},
		infos: []*domain.ServerInfo{
			{ID: "s1", Status: "connected"},
			{ID: "s2", Status: "disconnected"},
		},
	}
	svc := NewMCPService(&lifecycleRegistryFake{}, mgr, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})

	got := svc.ListServers(context.Background())
	if len(got) != 2 {
		t.Fatalf("ListServers = %d, want 2", len(got))
	}
	info, err := svc.GetServer(context.Background(), "s1")
	if err != nil || info == nil || info.ID != "s1" {
		t.Fatalf("GetServer = %v, %v", info, err)
	}
	if _, err := svc.GetServer(context.Background(), "ghost"); !errors.Is(err, domain.ErrServerNotFound) {
		t.Fatalf("missing server err = %v", err)
	}
}

func TestMCPServiceListToolsAndResources(t *testing.T) {
	mgr := &queryManagerFake{
		lifecycleManagerFake: &lifecycleManagerFake{},
		tools:                []*domain.Tool{{Name: "t1"}},
		resources:            []*domain.Resource{{URI: "r1"}},
	}
	svc := NewMCPService(&lifecycleRegistryFake{}, mgr, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})

	tools, err := svc.ListTools(context.Background(), "s1")
	if err != nil || len(tools) != 1 || tools[0].Name != "t1" {
		t.Fatalf("ListTools = %v, %v", tools, err)
	}
	resources, err := svc.ListResources(context.Background(), "s1")
	if err != nil || len(resources) != 1 || resources[0].URI != "r1" {
		t.Fatalf("ListResources = %v, %v", resources, err)
	}
}

func TestMCPServiceQuotaAndStatusBreakdown(t *testing.T) {
	// 极端情况：unknown 状态不计入任何桶。
	mgr := &queryManagerFake{
		lifecycleManagerFake: &lifecycleManagerFake{},
		quota:                domain.Quota{TenantID: "t1", Used: 3, Limit: 5, Healthy: 2, Dead: 1},
		infos: []*domain.ServerInfo{
			{Status: "connected"}, {Status: "connected"},
			{Status: "disconnected"}, {Status: "error"}, {Status: "weird"},
		},
	}
	svc := NewMCPService(&lifecycleRegistryFake{}, mgr, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})

	q := svc.GetQuota(context.Background())
	if q.TenantID != "t1" || q.Used != 3 || q.Limit != 5 || q.Healthy != 2 || q.Dead != 1 {
		t.Fatalf("quota = %+v", q)
	}
	b := svc.ServerStatus(context.Background())
	if b.Total != 5 || b.Connected != 2 || b.Disconnected != 1 || b.Error != 1 {
		t.Fatalf("breakdown = %+v", b)
	}
}

func TestMCPServiceGetServerConfigAndNameConflict(t *testing.T) {
	mgr := &queryManagerFake{lifecycleManagerFake: &lifecycleManagerFake{}}
	svc := NewMCPService(&lifecycleRegistryFake{}, mgr, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})
	if _, err := svc.GetServerConfig(context.Background(), "s1"); err != nil {
		t.Fatalf("GetServerConfig = %v", err)
	}
	if !IsNameConflict(domain.ErrNameConflict) || IsNameConflict(errors.New("other")) || IsNameConflict(nil) {
		t.Fatal("IsNameConflict must match only the canonical sentinel")
	}
}

func TestMCPServiceGetToolRisk(t *testing.T) {
	svc := NewMCPService(&lifecycleRegistryFake{}, &lifecycleManagerFake{}, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})

	// 未配置 repo → Unclassified。
	level, err := svc.GetToolRisk(context.Background(), "s1", "tool")
	if err != nil || level != domain.ToolRiskUnclassified {
		t.Fatalf("no repo risk = %q, %v", level, err)
	}

	// 命中策略。
	svc.SetToolPolicyRepo(&toolPolicyStub{
		policy: domain.ToolPolicy{ServerID: "s1", ToolName: "tool", RiskLevel: domain.ToolRiskDestructive},
		found:  true,
	})
	level, err = svc.GetToolRisk(context.Background(), "s1", "tool")
	if err != nil || level != domain.ToolRiskDestructive {
		t.Fatalf("risk = %q, %v", level, err)
	}

	// 未命中 → Unclassified。
	svc.SetToolPolicyRepo(&toolPolicyStub{})
	level, err = svc.GetToolRisk(context.Background(), "s1", "tool")
	if err != nil || level != domain.ToolRiskUnclassified {
		t.Fatalf("miss risk = %q, %v", level, err)
	}

	// 极端情况：repo 错误传播。
	svc.SetToolPolicyRepo(&toolPolicyStub{getErr: errors.New("db down")})
	if _, err := svc.GetToolRisk(context.Background(), "s1", "tool"); err == nil {
		t.Fatal("repo error must propagate")
	}
}

func TestMCPServiceListToolPoliciesNoRepo(t *testing.T) {
	// 极端情况：nil repo 返回空列表而非 nil。
	svc := NewMCPService(&lifecycleRegistryFake{}, &lifecycleManagerFake{}, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})
	policies, err := svc.ListToolPolicies(context.Background())
	if err != nil || len(policies) != 0 || policies == nil {
		t.Fatalf("ListToolPolicies = %v, %v", policies, err)
	}
}

func TestMCPServiceSetToolPolicyValidation(t *testing.T) {
	svc := NewMCPService(&lifecycleRegistryFake{}, &lifecycleManagerFake{}, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})

	// 非法风险等级。
	if err := svc.SetToolPolicy(context.Background(), domain.ToolPolicy{RiskLevel: "nope"}); err == nil {
		t.Fatal("invalid risk level must error")
	}
	// 缺少 server/tool。
	if err := svc.SetToolPolicy(context.Background(), domain.ToolPolicy{RiskLevel: domain.ToolRiskRead}); err == nil {
		t.Fatal("missing server/tool must error")
	}
	// 未配置 repo。
	if err := svc.SetToolPolicy(context.Background(), domain.ToolPolicy{
		ServerID: "s1", ToolName: "t", RiskLevel: domain.ToolRiskRead,
	}); err == nil {
		t.Fatal("nil repo must error")
	}

	// 成功 upsert。
	repo := &toolPolicyStub{}
	svc.SetToolPolicyRepo(repo)
	if err := svc.SetToolPolicy(context.Background(), domain.ToolPolicy{
		ServerID: "s1", ToolName: "t", RiskLevel: domain.ToolRiskWriteReversible,
	}); err != nil {
		t.Fatalf("upsert = %v", err)
	}
	if len(repo.upserted) != 1 || repo.upserted[0].ToolName != "t" {
		t.Fatalf("upserted = %+v", repo.upserted)
	}
}

func TestMCPServiceReconnectServer(t *testing.T) {
	// 成功 + 注册失败仅告警（不阻断）。
	mgr := &queryManagerFake{lifecycleManagerFake: &lifecycleManagerFake{}}
	svc := NewMCPService(&lifecycleRegistryFake{}, mgr, zap.NewNop())
	svc.SetTenantRoleResolver(stubTenantRole{role: "owner"})
	if err := svc.ReconnectServer(context.Background(), "s1"); err != nil {
		t.Fatalf("reconnect = %v", err)
	}
}

// stubTenantRole resolves every actor as a fixed role so ownership tests
// control authorization via the fake, not tenant membership.
type stubTenantRole struct{ role string }

func (s stubTenantRole) ResolveTenantRole(_ context.Context, _, _ string) (string, error) {
	return s.role, nil
}
