package application

import (
	"context"
	"testing"

	"github.com/byteBuilderX/stratum/internal/prompt/domain"
	"github.com/stretchr/testify/require"
)

func newRegistry(prompts *fakePromptRepo, bindings *fakeBindingRepo) *RegistryService {
	if prompts == nil {
		prompts = &fakePromptRepo{byHash: map[string]*domain.PromptTemplate{}}
	}
	if bindings == nil {
		bindings = &fakeBindingRepo{}
	}
	return NewRegistryService(prompts, bindings)
}

func TestRegistryService_CreateTemplate_firstVersion(t *testing.T) {
	prompts := &fakePromptRepo{byKey: map[string][]domain.PromptTemplate{}}
	svc := newRegistry(prompts, nil)

	tmpl, err := svc.CreateTemplate(context.Background(), "system_prompt", nil, "hello", "user:1")
	require.NoError(t, err)
	require.Equal(t, 1, tmpl.Version)
	require.Equal(t, domain.PromptDraft, tmpl.Status)
	require.Equal(t, domain.ComputeHash("hello"), tmpl.ContentHash)
}

func TestRegistryService_CreateTemplate_incrementsVersion(t *testing.T) {
	prompts := &fakePromptRepo{byKey: map[string][]domain.PromptTemplate{
		"k1": {{Key: "k1", Version: 3}, {Key: "k1", Version: 7}},
	}}
	svc := newRegistry(prompts, nil)

	tmpl, err := svc.CreateTemplate(context.Background(), "k1", nil, "v8", "system")
	require.NoError(t, err)
	require.Equal(t, 8, tmpl.Version)
}

func TestRegistryService_CreateTemplate_getFails(t *testing.T) {
	svc := newRegistry(&fakePromptRepo{err: context.DeadlineExceeded}, nil)
	_, err := svc.CreateTemplate(context.Background(), "k1", nil, "x", "user:1")
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestRegistryService_CreateTemplate_insertFails(t *testing.T) {
	prompts := &fakePromptRepo{byKey: map[string][]domain.PromptTemplate{}, err: context.DeadlineExceeded}
	svc := newRegistry(prompts, nil)

	// err is returned for GetByKey too, so override: insert failure only path.
	prompts.err = nil
	prompts.insertErr = context.DeadlineExceeded
	_, err := svc.CreateTemplate(context.Background(), "k1", nil, "x", "user:1")
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestRegistryService_PublishVersion_successArchivesOld(t *testing.T) {
	prompts := &fakePromptRepo{
		byVer: map[string]*domain.PromptTemplate{
			"k1": {Key: "k1", Version: 2, Status: domain.PromptDraft},
		},
		byKey: map[string][]domain.PromptTemplate{
			"k1": {
				{Key: "k1", Version: 2, Status: domain.PromptDraft},
				{Key: "k1", Version: 1, Status: domain.PromptPublished},
			},
		},
		archived: make(map[int]bool),
	}
	svc := newRegistry(prompts, nil)

	require.NoError(t, svc.PublishVersion(context.Background(), "k1", 2, nil))
	require.True(t, prompts.archived[1], "previously published version must be archived")
	require.Equal(t, domain.PromptPublished, prompts.publishedStatus)
}

func TestRegistryService_PublishVersion_notFound(t *testing.T) {
	svc := newRegistry(&fakePromptRepo{byVer: map[string]*domain.PromptTemplate{}}, nil)
	err := svc.PublishVersion(context.Background(), "k1", 9, nil)
	require.ErrorContains(t, err, "version 9 not found")
}

func TestRegistryService_PublishVersion_getFails(t *testing.T) {
	svc := newRegistry(&fakePromptRepo{err: context.DeadlineExceeded}, nil)
	err := svc.PublishVersion(context.Background(), "k1", 1, nil)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestRegistryService_PublishVersion_rejectsNonDraft(t *testing.T) {
	svc := newRegistry(&fakePromptRepo{
		byVer: map[string]*domain.PromptTemplate{
			"k1": {Key: "k1", Version: 1, Status: domain.PromptPublished},
		},
	}, nil)
	err := svc.PublishVersion(context.Background(), "k1", 1, nil)
	require.ErrorContains(t, err, "only draft can be published")
}

func TestRegistryService_GetVersions(t *testing.T) {
	prompts := &fakePromptRepo{byKey: map[string][]domain.PromptTemplate{
		"k1": {{Key: "k1", Version: 1}},
	}}
	svc := newRegistry(prompts, nil)

	versions, err := svc.GetVersions(context.Background(), "k1", nil)
	require.NoError(t, err)
	require.Len(t, versions, 1)
}

func TestRegistryService_GetVersions_fails(t *testing.T) {
	svc := newRegistry(&fakePromptRepo{err: context.DeadlineExceeded}, nil)
	_, err := svc.GetVersions(context.Background(), "k1", nil)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestRegistryService_Rollback_success(t *testing.T) {
	prompts := &fakePromptRepo{
		byVer: map[string]*domain.PromptTemplate{
			"k1": {Key: "k1", Version: 3, Content: "old-content", Status: domain.PromptPublished},
		},
		byKey: map[string][]domain.PromptTemplate{
			"k1": {{Key: "k1", Version: 3}},
		},
	}
	svc := newRegistry(prompts, nil)

	tmpl, err := svc.Rollback(context.Background(), "k1", 3, nil, "user:2")
	require.NoError(t, err)
	require.Equal(t, "old-content", tmpl.Content)
	require.Equal(t, 4, tmpl.Version, "rollback creates a new version preserving the audit trail")
}

func TestRegistryService_Rollback_sourceMissing(t *testing.T) {
	svc := newRegistry(&fakePromptRepo{byVer: map[string]*domain.PromptTemplate{}}, nil)
	_, err := svc.Rollback(context.Background(), "k1", 3, nil, "user:2")
	require.ErrorContains(t, err, "source version 3 not found")
}

func TestRegistryService_Rollback_getFails(t *testing.T) {
	svc := newRegistry(&fakePromptRepo{err: context.DeadlineExceeded}, nil)
	_, err := svc.Rollback(context.Background(), "k1", 3, nil, "user:2")
	require.ErrorContains(t, err, "source version 3 not found")
}

func TestRegistryService_GetEffectivePrompt_agentBindingWins(t *testing.T) {
	svc := newRegistry(&fakePromptRepo{
		byHash: map[string]*domain.PromptTemplate{
			"sv-a": {Key: "k1", Content: "agent-prompt"},
		},
	}, &fakeBindingRepo{
		binding: &domain.PromptBinding{Key: "k1", Scope: "agent:a1", StableVersionID: "sv-a"},
	})

	text, err := svc.GetEffectivePrompt(context.Background(), "k1", "t1", "a1", "req-1")
	require.NoError(t, err)
	require.Equal(t, "agent-prompt", text)
}

func TestRegistryService_GetEffectivePrompt_tenantFallback(t *testing.T) {
	svc := newRegistry(&fakePromptRepo{
		byHash: map[string]*domain.PromptTemplate{
			"sv-t": {Key: "k1", Content: "tenant-prompt"},
		},
	}, &fakeBindingRepo{
		binding: &domain.PromptBinding{Key: "k1", Scope: "tenant:t1", StableVersionID: "sv-t"},
	})

	text, err := svc.GetEffectivePrompt(context.Background(), "k1", "t1", "", "req-1")
	require.NoError(t, err)
	require.Equal(t, "tenant-prompt", text)
}

func TestRegistryService_GetEffectivePrompt_agentBindingMissingFallsThrough(t *testing.T) {
	svc := newRegistry(&fakePromptRepo{
		pub: map[string]*domain.PromptTemplate{
			"k1": {Key: "k1", Content: "global-prompt"},
		},
	}, &fakeBindingRepo{}) // no bindings at all

	text, err := svc.GetEffectivePrompt(context.Background(), "k1", "t1", "a1", "req-1")
	require.NoError(t, err)
	require.Equal(t, "global-prompt", text)
}

func TestRegistryService_GetEffectivePrompt_globalMissing(t *testing.T) {
	svc := newRegistry(&fakePromptRepo{}, &fakeBindingRepo{})
	_, err := svc.GetEffectivePrompt(context.Background(), "k1", "t1", "a1", "req-1")
	require.ErrorContains(t, err, `no published version for key "k1"`)
}

func TestRegistryService_GetEffectivePrompt_canaryRouting(t *testing.T) {
	// TrafficPercent 100 forces canary; both versions must exist.
	svc := newRegistry(&fakePromptRepo{
		byHash: map[string]*domain.PromptTemplate{
			"sv-stable": {Key: "k1", Content: "stable"},
			"sv-canary": {Key: "k1", Content: "canary"},
		},
	}, &fakeBindingRepo{
		binding: &domain.PromptBinding{
			Key: "k1", Scope: "agent:a1",
			StableVersionID: "sv-stable", CanaryVersionID: "sv-canary", TrafficPercent: 100,
		},
	})

	text, err := svc.GetEffectivePrompt(context.Background(), "k1", "t1", "a1", "req-1")
	require.NoError(t, err)
	require.Equal(t, "canary", text)
}

func TestRegistryService_GetEffectivePrompt_agentBindingBrokenFallsThrough(t *testing.T) {
	// Agent binding exists but its content hash is missing → skip to tenant/global.
	svc := newRegistry(&fakePromptRepo{
		pub: map[string]*domain.PromptTemplate{
			"k1": {Key: "k1", Content: "global-prompt"},
		},
	}, &fakeBindingRepo{
		binding: &domain.PromptBinding{Key: "k1", Scope: "agent:a1", StableVersionID: "missing"},
	})

	text, err := svc.GetEffectivePrompt(context.Background(), "k1", "t1", "a1", "req-1")
	require.NoError(t, err)
	require.Equal(t, "global-prompt", text)
}
