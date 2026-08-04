package application

import (
	"context"
	"testing"

	"github.com/byteBuilderX/stratum/internal/prompt/domain"
	"github.com/stretchr/testify/require"
)

type fakePromptRepo struct {
	byHash          map[string]*domain.PromptTemplate
	byKey           map[string][]domain.PromptTemplate
	byVer           map[string]*domain.PromptTemplate
	pub             map[string]*domain.PromptTemplate
	err             error
	insertErr       error
	archived        map[int]bool
	publishedStatus domain.PromptStatus
}

func (f *fakePromptRepo) Insert(context.Context, domain.PromptTemplate) error {
	if f.insertErr != nil {
		return f.insertErr
	}
	return f.err
}
func (f *fakePromptRepo) GetByKey(_ context.Context, key string, _ *string) ([]domain.PromptTemplate, error) {
	return f.byKey[key], f.err
}
func (f *fakePromptRepo) GetVersion(_ context.Context, key string, ver int, _ *string) (*domain.PromptTemplate, error) {
	if f.byVer != nil {
		if tmpl := f.byVer[key]; tmpl != nil && tmpl.Version == ver {
			return tmpl, nil
		}
	}
	return nil, f.err
}
func (f *fakePromptRepo) GetLatestPublished(_ context.Context, key string, _ *string) (*domain.PromptTemplate, error) {
	return f.pub[key], f.err
}
func (f *fakePromptRepo) UpdateStatus(_ context.Context, _ string, ver int, _ *string, status domain.PromptStatus) error {
	if f.err != nil {
		return f.err
	}
	if status == domain.PromptArchived {
		f.archived[ver] = true
	}
	if status == domain.PromptPublished {
		f.publishedStatus = status
	}
	return nil
}
func (f *fakePromptRepo) GetByHash(_ context.Context, hash string) (*domain.PromptTemplate, error) {
	return f.byHash[hash], f.err
}

type fakeBindingRepo struct {
	binding  *domain.PromptBinding
	bindings []domain.PromptBinding
	err      error
	upserted *domain.PromptBinding
	deleted  bool
}

func (f *fakeBindingRepo) UpsertBinding(_ context.Context, b domain.PromptBinding) error {
	f.upserted = &b
	return f.err
}
func (f *fakeBindingRepo) GetBinding(context.Context, string, string) (*domain.PromptBinding, error) {
	return f.binding, f.err
}
func (f *fakeBindingRepo) ListBindings(context.Context, string) ([]domain.PromptBinding, error) {
	return f.bindings, f.err
}
func (f *fakeBindingRepo) DeleteBinding(context.Context, string, string) error {
	f.deleted = true
	return f.err
}

func TestABService_BindExperiment_outOfRangePercent(t *testing.T) {
	svc := NewABService(&fakeBindingRepo{}, &fakePromptRepo{})
	for _, p := range []int{-1, 101} {
		err := svc.BindExperiment(context.Background(), "k1", "tenant:t1", "sv1", "", p)
		require.ErrorContains(t, err, "traffic percent must be 0-100")
	}
}

func TestABService_BindExperiment_stableNotFound(t *testing.T) {
	svc := NewABService(&fakeBindingRepo{}, &fakePromptRepo{byHash: map[string]*domain.PromptTemplate{}})
	err := svc.BindExperiment(context.Background(), "k1", "tenant:t1", "sv1", "", 10)
	require.ErrorContains(t, err, `version "sv1" not found`)
}

func TestABService_BindExperiment_promptLookupFails(t *testing.T) {
	svc := NewABService(&fakeBindingRepo{}, &fakePromptRepo{err: context.DeadlineExceeded})
	err := svc.BindExperiment(context.Background(), "k1", "tenant:t1", "sv1", "", 10)
	require.ErrorContains(t, err, `version "sv1" not found`)
}

func TestABService_BindExperiment_canaryNotFound(t *testing.T) {
	svc := NewABService(&fakeBindingRepo{}, &fakePromptRepo{
		byHash: map[string]*domain.PromptTemplate{"sv1": {Key: "k1"}},
	})
	err := svc.BindExperiment(context.Background(), "k1", "tenant:t1", "sv1", "cv1", 10)
	require.ErrorContains(t, err, `version "cv1" not found`)
}

func TestABService_BindExperiment_success(t *testing.T) {
	bindings := &fakeBindingRepo{}
	svc := NewABService(bindings, &fakePromptRepo{
		byHash: map[string]*domain.PromptTemplate{
			"sv1": {Key: "k1"}, "cv1": {Key: "k1"},
		},
	})
	require.NoError(t, svc.BindExperiment(context.Background(), "k1", "tenant:t1", "sv1", "cv1", 20))
	require.NotNil(t, bindings.upserted)
	require.Equal(t, 20, bindings.upserted.TrafficPercent)
	require.Equal(t, "tenant:t1", bindings.upserted.Scope)
}

func TestABService_BindExperiment_skipEmptyVersions(t *testing.T) {
	bindings := &fakeBindingRepo{}
	// Both version IDs empty → no lookup, straight to upsert.
	svc := NewABService(bindings, &fakePromptRepo{err: context.DeadlineExceeded})
	require.NoError(t, svc.BindExperiment(context.Background(), "k1", "tenant:t1", "", "", 0))
	require.NotNil(t, bindings.upserted)
}

func TestABService_ClearExperiment(t *testing.T) {
	bindings := &fakeBindingRepo{}
	svc := NewABService(bindings, &fakePromptRepo{})
	require.NoError(t, svc.ClearExperiment(context.Background(), "k1", "agent:a1"))
	require.True(t, bindings.deleted)
}

func TestABService_ClearExperiment_fails(t *testing.T) {
	svc := NewABService(&fakeBindingRepo{err: context.DeadlineExceeded}, &fakePromptRepo{})
	require.ErrorIs(t, svc.ClearExperiment(context.Background(), "k1", "agent:a1"), context.DeadlineExceeded)
}

func TestResolveAB_emptyRequest(t *testing.T) {
	require.False(t, resolveAB("", 50))
}

func TestResolveAB_zeroPercent(t *testing.T) {
	require.False(t, resolveAB("req-1", 0))
	require.False(t, resolveAB("req-1", -5))
}

func TestResolveAB_fullPercent(t *testing.T) {
	require.True(t, resolveAB("req-1", 100))
	require.True(t, resolveAB("req-1", 500))
}

func TestResolveAB_deterministic(t *testing.T) {
	require.Equal(t, resolveAB("req-42", 30), resolveAB("req-42", 30))
}

func TestResolveAB_distributionBounds(t *testing.T) {
	// Hash space spans all buckets; 50% traffic must admit at least one of two
	// known IDs and reject at least one other — proves the modulo gate is not a
	// constant function.
	seen := 0
	for i := 0; i < 100; i++ {
		if resolveAB("req-dist-"+string(rune(i)), 50) {
			seen++
		}
	}
	require.Greater(t, seen, 0)
	require.Less(t, seen, 100)
}
