package application

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	auditdomain "github.com/byteBuilderX/stratum/internal/audit/domain"
	"github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	"github.com/byteBuilderX/stratum/internal/llmgateway/domain/port"
)

func TestOptionalIntJSONStates(t *testing.T) {
	tests := []struct {
		name  string
		input string
		set   bool
		value *int
	}{
		{name: "omitted preserves", input: `{}`, set: false},
		{name: "null clears", input: `{"operatorMaxTokens":null}`, set: true},
		{name: "value sets", input: `{"operatorMaxTokens":2048}`, set: true, value: intPtr(2048)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var input UpdateModelPolicyInput
			if err := json.Unmarshal([]byte(tc.input), &input); err != nil {
				t.Fatal(err)
			}
			if input.OperatorMaxTokens.Set != tc.set {
				t.Fatalf("Set = %v, want %v", input.OperatorMaxTokens.Set, tc.set)
			}
			if tc.value == nil && input.OperatorMaxTokens.Value != nil {
				t.Fatalf("Value = %v, want nil", *input.OperatorMaxTokens.Value)
			}
			if tc.value != nil && (input.OperatorMaxTokens.Value == nil || *input.OperatorMaxTokens.Value != *tc.value) {
				t.Fatalf("Value = %v, want %v", input.OperatorMaxTokens.Value, *tc.value)
			}
		})
	}
}

func intPtr(value int) *int { return &value }

type modelMgmtRepo struct {
	model   domain.Model
	models  []domain.Model
	err     error
	listErr error
	updated *domain.Model
	created *domain.Model
}

func (r *modelMgmtRepo) Create(context.Context, *domain.Model) error { return r.err }
func (r *modelMgmtRepo) Get(_ context.Context, id string) (*domain.Model, error) {
	for i := range r.models {
		if r.models[i].ID == id {
			m := r.models[i]
			return &m, r.err
		}
	}
	model := r.model
	return &model, r.err
}
func (r *modelMgmtRepo) List(context.Context, port.ModelFilter) ([]domain.Model, error) {
	if r.listErr != nil {
		return nil, r.listErr
	}
	return r.models, r.err
}
func (r *modelMgmtRepo) Update(context.Context, *domain.Model, string, *auditdomain.ResourceChangeAuditEvent) error {
	return r.err
}
func (r *modelMgmtRepo) UpsertDiscovered(
	context.Context, string, []domain.Model,
) ([]domain.Model, error) {
	return nil, r.err
}
func (r *modelMgmtRepo) Delete(context.Context, string) error       { return r.err }
func (r *modelMgmtRepo) Toggle(context.Context, string, bool) error { return r.err }
func (r *modelMgmtRepo) UpdatePolicy(_ context.Context, m *domain.Model, _ string, _ *auditdomain.ResourceChangeAuditEvent) error {
	r.updated = m
	return r.err
}
func (r *modelMgmtRepo) UpdatePlatform(_ context.Context, m *domain.Model, _ string, _ *auditdomain.ResourceChangeAuditEvent) error {
	r.updated = m
	return r.err
}
func (r *modelMgmtRepo) CreatePlatform(_ context.Context, m *domain.Model, _ string, _ *auditdomain.ResourceChangeAuditEvent) error {
	r.created = m
	return r.err
}

type recordingInvalidator struct {
	calls int
}

func (i *recordingInvalidator) Invalidate() {
	i.calls++
}

// invalidatorFunc adapts a plain func to ModelCacheInvalidator for concise test setup.
type invalidatorFunc func()

func (f invalidatorFunc) Invalidate() { f() }

func TestModelMgmtServiceInvalidatesRegistryAfterSuccessfulMutation(t *testing.T) {
	invalidator := &recordingInvalidator{}
	svc := NewModelMgmtService(&modelMgmtRepo{}, invalidator)

	if err := svc.Toggle(context.Background(), "tenant-1", "model-1", false); err != nil {
		t.Fatalf("Toggle: %v", err)
	}
	if invalidator.calls != 1 {
		t.Fatalf("invalidations = %d, want 1", invalidator.calls)
	}
}

func TestModelMgmtServiceDoesNotInvalidateAfterFailedMutation(t *testing.T) {
	invalidator := &recordingInvalidator{}
	svc := NewModelMgmtService(&modelMgmtRepo{err: errors.New("write failed")}, invalidator)

	if err := svc.Toggle(context.Background(), "tenant-1", "model-1", false); err == nil {
		t.Fatal("Toggle must return the repository error")
	}
	if invalidator.calls != 0 {
		t.Fatalf("invalidations = %d, want none", invalidator.calls)
	}
}

// TestModelMgmtServiceUpdatePolicyFallbackCandidates 覆盖显式降级候选写入校验
// （fail-closed）：上限、非自身、去重、目录存在性（enabled+chat）、目录查询
// 失败传播，以及成功路径的合并、nil 保留与空数组清空语义。
func TestModelMgmtServiceUpdatePolicyFallbackCandidates(t *testing.T) {
	catalog := func() *modelMgmtRepo {
		return &modelMgmtRepo{models: []domain.Model{
			{ID: "m-primary", Name: "primary", Enabled: true, Capabilities: []domain.ModelCapability{domain.CapChat}},
			{ID: "m-ca", Name: "cand-a", Enabled: true, Capabilities: []domain.ModelCapability{domain.CapChat}},
			{ID: "m-cb", Name: "cand-b", Enabled: true, Capabilities: []domain.ModelCapability{domain.CapChat}},
			{ID: "m-dis", Name: "disabled-x", Enabled: false, Capabilities: []domain.ModelCapability{domain.CapChat}},
			{ID: "m-emb", Name: "embed-x", Enabled: true, Capabilities: []domain.ModelCapability{domain.CapEmbedding}},
		}}
	}
	cands := func(names ...string) *[]string {
		v := append([]string(nil), names...)
		return &v
	}

	rejectCases := []struct {
		name  string
		cands *[]string
		want  string
	}{
		{"over max", cands("cand-a", "cand-b", "cand-a", "cand-a"), "exceed max"},
		{"self reference", cands("primary"), "must not be the model itself"},
		{"duplicate", cands("cand-a", "cand-a"), "duplicate fallback candidate"},
		{"unknown model", cands("ghost"), "not an enabled chat model"},
		{"disabled model", cands("disabled-x"), "not an enabled chat model"},
		{"embedding-only model", cands("embed-x"), "not an enabled chat model"},
	}
	for _, tc := range rejectCases {
		t.Run(tc.name, func(t *testing.T) {
			repo := catalog()
			svc := NewModelMgmtService(repo, invalidatorFunc(func() {
				t.Fatal("must not invalidate on rejected candidates")
			}))
			_, err := svc.UpdatePolicy(context.Background(), "t1", "actor", UpdateModelPolicyInput{
				ID: "m-primary", FallbackCandidates: tc.cands,
			})
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.want)
			}
			// fail-closed 语义的另一半：客户端输入错误必须命中领域 sentinel，
			// 由错误中间件映射 4xx，而不是裸 error 落入 5xx。
			if !errors.Is(err, domain.ErrInvalidFallbackCandidates) {
				t.Fatalf("error %q must wrap domain.ErrInvalidFallbackCandidates for 4xx mapping", err.Error())
			}
			if repo.updated != nil {
				t.Fatalf("repo must not be touched on rejected candidates, updated = %+v", repo.updated)
			}
		})
	}

	t.Run("accepts valid candidates and persists merged", func(t *testing.T) {
		repo := catalog()
		invalidated := false
		svc := NewModelMgmtService(repo, invalidatorFunc(func() { invalidated = true }))
		_, err := svc.UpdatePolicy(context.Background(), "t1", "actor", UpdateModelPolicyInput{
			ID: "m-primary", FallbackCandidates: cands("cand-b", "cand-a"),
		})
		if err != nil {
			t.Fatal(err)
		}
		if repo.updated == nil || !slices.Equal(repo.updated.FallbackCandidates, []string{"cand-b", "cand-a"}) {
			t.Fatalf("persisted candidates = %v, want [cand-b cand-a]", repo.updated.FallbackCandidates)
		}
		if !invalidated {
			t.Fatal("expected registry invalidation")
		}
	})

	t.Run("nil preserves existing candidates", func(t *testing.T) {
		repo := catalog()
		repo.models[0].FallbackCandidates = []string{"cand-a"}
		svc := NewModelMgmtService(repo)
		if _, err := svc.UpdatePolicy(context.Background(), "t1", "actor", UpdateModelPolicyInput{ID: "m-primary"}); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(repo.updated.FallbackCandidates, []string{"cand-a"}) {
			t.Fatalf("preserved candidates = %v, want [cand-a]", repo.updated.FallbackCandidates)
		}
	})

	t.Run("empty array clears candidates", func(t *testing.T) {
		repo := catalog()
		repo.models[0].FallbackCandidates = []string{"cand-a"}
		svc := NewModelMgmtService(repo)
		if _, err := svc.UpdatePolicy(context.Background(), "t1", "actor", UpdateModelPolicyInput{
			ID: "m-primary", FallbackCandidates: &[]string{},
		}); err != nil {
			t.Fatal(err)
		}
		if len(repo.updated.FallbackCandidates) != 0 {
			t.Fatalf("cleared candidates = %v, want empty", repo.updated.FallbackCandidates)
		}
	})

	t.Run("catalog query failure propagates fail-closed", func(t *testing.T) {
		repo := catalog()
		repo.listErr = errors.New("db down")
		svc := NewModelMgmtService(repo)
		_, err := svc.UpdatePolicy(context.Background(), "t1", "actor", UpdateModelPolicyInput{
			ID: "m-primary", FallbackCandidates: cands("cand-a"),
		})
		if err == nil {
			t.Fatal("expected catalog query error to propagate")
		}
		if !strings.Contains(err.Error(), "list chat models") {
			t.Fatalf("error %q does not mention catalog query", err.Error())
		}
	})
}

// providerRepoStub 实现 port.ProviderRepository 的只读面（Create 校验只用 Get）。
type providerRepoStub struct {
	provider *domain.Provider
	err      error
}

func (p *providerRepoStub) Create(context.Context, *domain.Provider) error { return nil }
func (p *providerRepoStub) Get(_ context.Context, _ string) (*domain.Provider, error) {
	if p.err != nil {
		return nil, p.err
	}
	if p.provider == nil {
		return nil, nil
	}
	return p.provider, nil
}
func (p *providerRepoStub) GetMeta(context.Context, string) (*domain.Provider, error) {
	return nil, nil
}
func (p *providerRepoStub) List(context.Context) ([]domain.Provider, error) {
	return nil, nil
}
func (p *providerRepoStub) Update(context.Context, *domain.Provider, string, *auditdomain.ResourceChangeAuditEvent) error {
	return nil
}
func (p *providerRepoStub) Delete(context.Context, string) error { return nil }

func TestModelMgmtService_Create(t *testing.T) {
	t.Run("rejects empty name", func(t *testing.T) {
		svc := NewModelMgmtService(&modelMgmtRepo{}).WithProviderRepo(&providerRepoStub{})
		_, err := svc.Create(context.Background(), "actor", "t1", CreateModelInput{
			ProviderID: "p-1", Capabilities: []domain.ModelCapability{domain.CapChat},
		})
		require.ErrorIs(t, err, domain.ErrInvalidModelInput)
	})

	t.Run("rejects no capabilities", func(t *testing.T) {
		svc := NewModelMgmtService(&modelMgmtRepo{}).WithProviderRepo(&providerRepoStub{})
		_, err := svc.Create(context.Background(), "actor", "t1", CreateModelInput{
			ProviderID: "p-1", Name: "gpt-x",
		})
		require.ErrorIs(t, err, domain.ErrInvalidModelInput)
	})

	t.Run("rejects missing providerId", func(t *testing.T) {
		svc := NewModelMgmtService(&modelMgmtRepo{}).WithProviderRepo(&providerRepoStub{})
		_, err := svc.Create(context.Background(), "actor", "t1", CreateModelInput{
			Name: "gpt-x", Capabilities: []domain.ModelCapability{domain.CapChat},
		})
		require.ErrorIs(t, err, domain.ErrInvalidModelInput)
	})

	t.Run("propagates provider lookup failure", func(t *testing.T) {
		svc := NewModelMgmtService(&modelMgmtRepo{}).WithProviderRepo(&providerRepoStub{
			err: errors.New("get provider: failed"),
		})
		_, err := svc.Create(context.Background(), "actor", "t1", CreateModelInput{
			ProviderID: "ghost", Name: "gpt-x", Capabilities: []domain.ModelCapability{domain.CapChat},
		})
		if !strings.Contains(err.Error(), "provider") {
			t.Fatalf("error %q does not mention provider", err.Error())
		}
	})

	t.Run("inserts manual model with defaults and audit", func(t *testing.T) {
		repo := &modelMgmtRepo{}
		invalidated := false
		svc := NewModelMgmtService(repo, invalidatorFunc(func() { invalidated = true })).
			WithProviderRepo(&providerRepoStub{provider: &domain.Provider{ID: "p-1"}})
		m, err := svc.Create(context.Background(), "actor", "t1", CreateModelInput{
			ProviderID: "p-1", Name: "gpt-x", Capabilities: []domain.ModelCapability{domain.CapChat, domain.CapReasoning},
			ContextWindow: 128000, MaxTokens: 4096,
		})
		require.NoError(t, err)
		require.Equal(t, "gpt-x", m.Name)
		require.False(t, m.ProviderManaged)
		require.True(t, m.Enabled)
		require.Equal(t, domain.CapabilitySourceManualUnknown, m.ContextWindowSource)
		require.Equal(t, 128000, m.ContextWindow)
		if !invalidated {
			t.Fatal("expected registry invalidation")
		}
	})
}
