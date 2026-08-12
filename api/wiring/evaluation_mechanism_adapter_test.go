package wiring

import (
	"context"
	"errors"
	"testing"

	evaldomain "github.com/byteBuilderX/stratum/internal/evaluation/domain"
	llmgatewaydomain "github.com/byteBuilderX/stratum/internal/llmgateway/domain"
	mechanismdomain "github.com/byteBuilderX/stratum/internal/mechanism/domain"
	"github.com/byteBuilderX/stratum/pkg/constants"
	"github.com/byteBuilderX/stratum/pkg/reqctx"
)

// stubProfileReader 是 mechanismProfileReader 的内存替身。
type stubProfileReader struct {
	profile mechanismdomain.Profile
	missing bool
	err     error
}

func (s *stubProfileReader) GetByFamilyKey(_ context.Context, familyKey string) (mechanismdomain.Profile, error) {
	if s.err != nil {
		return mechanismdomain.Profile{}, s.err
	}
	if s.missing || s.profile.FamilyKey != familyKey {
		return mechanismdomain.Profile{}, errors.New("profile not found")
	}
	return s.profile, nil
}

// stubCompleter 是 llmgatewaydomain.LLMCompleter 的内存替身（仅 Complete
// 被机制 adapter 使用，CompleteStream 恒报错）。tenant 捕获 ctx 租户，
// 供租户注入断言使用。
type stubCompleter struct {
	response *llmgatewaydomain.CompletionResponse
	err      error
	request  *llmgatewaydomain.CompletionRequest
	tenant   string
}

func (s *stubCompleter) Complete(ctx context.Context, req *llmgatewaydomain.CompletionRequest) (*llmgatewaydomain.CompletionResponse, error) {
	s.request = req
	s.tenant = reqctx.TenantIDFromContext(ctx)
	if s.err != nil {
		return nil, s.err
	}
	return s.response, nil
}

func (s *stubCompleter) CompleteStream(context.Context, *llmgatewaydomain.CompletionRequest, func(string)) (*llmgatewaydomain.CompletionResponse, error) {
	return nil, errors.New("stream not supported")
}

func matrixProfile(fingerprint, model string) mechanismdomain.Profile {
	return mechanismdomain.Profile{
		FamilyKey: "qwen", DisplayName: "Qwen", Status: mechanismdomain.ProfileStatusActive,
		Fingerprint: fingerprint, Version: 1, CreatedBy: "api",
		Baseline: mechanismdomain.Baseline{
			Models: mechanismdomain.BaselineModels{EnrichModel: model},
			Prompts: mechanismdomain.BaselinePrompts{
				MemoryExtraction: "抽取模板：%s", MemorySummary: "总结模板：%s", Compaction: "压缩指令",
			},
		},
	}
}

func TestMechanismEvaluationAdapterExecutesWithProfileModelAndTemplate(t *testing.T) {
	completer := &stubCompleter{response: &llmgatewaydomain.CompletionResponse{
		Content: "{\"地点\":\"杭州\"}",
		Usage:   llmgatewaydomain.TokenUsage{TotalTokens: 42},
	}}
	adapter := &mechanismEvaluationAdapter{
		profiles:  &stubProfileReader{profile: matrixProfile("fp-1", "qwen-max")},
		completer: completer,
	}

	result, err := adapter.ExecuteRevision(context.Background(), "t1", "runner", evaldomain.ResourceRef{
		Kind: evaldomain.ResourceKindMechanism, ResourceID: "qwen", RevisionID: "fp-1",
	}, evaldomain.EvalCase{Input: map[string]string{"template": "memory_extraction", "input": "用户说：下周发布新版本。"}})
	if err != nil {
		t.Fatalf("ExecuteRevision: %v", err)
	}
	if completer.request == nil {
		t.Fatal("completer not called")
	}
	if completer.request.Model != "qwen-max" || completer.request.Temperature != 0 || completer.request.MaxTokens != constants.MatrixMaxTokens {
		t.Fatalf("unexpected completion params: %+v", completer.request)
	}
	if len(completer.request.Messages) != 2 || completer.request.Messages[0].Role != "system" ||
		completer.request.Messages[0].Content != "抽取模板：%s" || completer.request.Messages[1].Content != "用户说：下周发布新版本。" {
		t.Fatalf("unexpected messages: %+v", completer.request.Messages)
	}
	if result.Output != "{\"地点\":\"杭州\"}" || result.Tokens != 42 || result.TraceID == "" || result.DurationMs < 0 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestMechanismEvaluationAdapterInjectsTenantForGateway(t *testing.T) {
	completer := &stubCompleter{response: &llmgatewaydomain.CompletionResponse{Content: "ok"}}
	adapter := &mechanismEvaluationAdapter{
		profiles:  &stubProfileReader{profile: matrixProfile("fp-1", "qwen-max")},
		completer: completer,
	}
	_, err := adapter.ExecuteRevision(context.Background(), "tenant_default", "runner", evaldomain.ResourceRef{
		Kind: evaldomain.ResourceKindMechanism, ResourceID: "qwen", RevisionID: "fp-1",
	}, evaldomain.EvalCase{Input: map[string]string{"template": "memory_extraction", "input": "x"}})
	if err != nil {
		t.Fatalf("ExecuteRevision: %v", err)
	}
	// 网关 Complete 从 ctx 读租户解析模型提供方；未注入会在冷缓存空租户硬失败。
	if completer.tenant != "tenant_default" {
		t.Fatalf("expected tenant injected into completer ctx, got %q", completer.tenant)
	}
}

func TestMechanismEvaluationAdapterRejectsEmptyTenant(t *testing.T) {
	adapter := &mechanismEvaluationAdapter{
		profiles:  &stubProfileReader{profile: matrixProfile("fp-1", "qwen-max")},
		completer: &stubCompleter{response: &llmgatewaydomain.CompletionResponse{Content: "ok"}},
	}
	_, err := adapter.ExecuteRevision(context.Background(), "", "runner", evaldomain.ResourceRef{
		Kind: evaldomain.ResourceKindMechanism, ResourceID: "qwen", RevisionID: "fp-1",
	}, evaldomain.EvalCase{Input: map[string]string{"template": "memory_extraction", "input": "x"}})
	if err == nil {
		t.Fatal("expected fail-closed error for empty tenant, got nil")
	}
}

func TestMechanismEvaluationAdapterFailsClosed(t *testing.T) {
	ref := evaldomain.ResourceRef{Kind: evaldomain.ResourceKindMechanism, ResourceID: "qwen", RevisionID: "fp-1"}
	cases := []struct {
		name     string
		adapter  *mechanismEvaluationAdapter
		caseData evaldomain.EvalCase
		wantErr  error
	}{
		{
			name:     "case input missing template",
			adapter:  &mechanismEvaluationAdapter{profiles: &stubProfileReader{profile: matrixProfile("fp-1", "qwen-max")}},
			caseData: evaldomain.EvalCase{Input: map[string]string{"input": "x"}},
		},
		{
			name:    "fingerprint mismatch invalidates revision",
			adapter: &mechanismEvaluationAdapter{profiles: &stubProfileReader{profile: matrixProfile("fp-2", "qwen-max")}},
			caseData: evaldomain.EvalCase{Input: map[string]string{
				"template": "memory_extraction", "input": "x",
			}},
			wantErr: evaldomain.ErrRevisionNotPublished,
		},
		{
			name:     "empty enrich model refuses silent fallback",
			adapter:  &mechanismEvaluationAdapter{profiles: &stubProfileReader{profile: matrixProfile("fp-1", "")}},
			caseData: evaldomain.EvalCase{Input: map[string]string{"template": "memory_extraction", "input": "x"}},
		},
		{
			name:     "unknown template key",
			adapter:  &mechanismEvaluationAdapter{profiles: &stubProfileReader{profile: matrixProfile("fp-1", "qwen-max")}},
			caseData: evaldomain.EvalCase{Input: map[string]string{"template": "ghost", "input": "x"}},
		},
		{
			name:     "no completer configured",
			adapter:  &mechanismEvaluationAdapter{profiles: &stubProfileReader{profile: matrixProfile("fp-1", "qwen-max")}},
			caseData: evaldomain.EvalCase{Input: map[string]string{"template": "memory_extraction", "input": "x"}},
		},
		{
			name:    "profile missing",
			adapter: &mechanismEvaluationAdapter{profiles: &stubProfileReader{missing: true}},
			caseData: evaldomain.EvalCase{Input: map[string]string{
				"template": "memory_extraction", "input": "x",
			}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.adapter.ExecuteRevision(context.Background(), "t1", "runner", ref, tc.caseData)
			if err == nil {
				t.Fatal("expected fail-closed error, got nil")
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("expected %v, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestMechanismEvaluationAdapterPropagatesCompleterError(t *testing.T) {
	want := errors.New("upstream down")
	adapter := &mechanismEvaluationAdapter{
		profiles:  &stubProfileReader{profile: matrixProfile("fp-1", "qwen-max")},
		completer: &stubCompleter{err: want},
	}
	_, err := adapter.ExecuteRevision(context.Background(), "t1", "runner", evaldomain.ResourceRef{
		Kind: evaldomain.ResourceKindMechanism, ResourceID: "qwen", RevisionID: "fp-1",
	}, evaldomain.EvalCase{Input: map[string]string{"template": "memory_extraction", "input": "x"}})
	if !errors.Is(err, want) {
		t.Fatalf("expected upstream error propagation, got %v", err)
	}
}

func TestMechanismEvaluationAdapterResolvesVirtualPublishedRevision(t *testing.T) {
	profile := matrixProfile("fp-1", "qwen-max")
	adapter := &mechanismEvaluationAdapter{profiles: &stubProfileReader{profile: profile}}
	revision, err := adapter.ResolveRevision(context.Background(), "t1", evaldomain.ResourceRef{
		Kind: evaldomain.ResourceKindMechanism, ResourceID: "qwen", RevisionID: "fp-1",
	})
	if err != nil {
		t.Fatalf("ResolveRevision: %v", err)
	}
	if revision.ID != "fp-1" || revision.ResourceKind != evaldomain.ResourceKindMechanism ||
		revision.Status != evaldomain.RevisionStatusPublished || revision.ContentHash != "fp-1" ||
		revision.PayloadRef != "profile:qwen" {
		t.Fatalf("unexpected virtual revision: %+v", revision)
	}
	if !revision.CanEvaluateOffline() {
		t.Fatal("published virtual revision must be evaluable offline")
	}
	if err := revision.Validate(); err != nil {
		t.Fatalf("virtual revision must pass validation: %v", err)
	}
}

func TestMechanismEvaluationAdapterSafeSummaryPassesSensitiveKeyCheck(t *testing.T) {
	adapter := &mechanismEvaluationAdapter{profiles: &stubProfileReader{profile: matrixProfile("fp-1", "qwen-max")}}
	summary, err := adapter.SafeSummary(context.Background(), "t1", evaldomain.ResourceRef{
		Kind: evaldomain.ResourceKindMechanism, ResourceID: "qwen", RevisionID: "fp-1",
	})
	if err != nil {
		t.Fatalf("SafeSummary: %v", err)
	}
	for key, value := range summary {
		if evaldomain.IsSensitiveSafeSummaryKey(key) {
			t.Fatalf("safe summary contains sensitive key %q", key)
		}
		if text, ok := value.(string); ok && evaldomain.IsSensitiveSafeSummaryValue(text) {
			t.Fatalf("safe summary field %s carries sensitive value", key)
		}
	}
	if summary["family"] != "qwen" || summary["enrich_model"] != "qwen-max" {
		t.Fatalf("unexpected summary: %+v", summary)
	}
}

func TestMechanismEvaluationAdapterTemplateByKey(t *testing.T) {
	prompts := mechanismdomain.BaselinePrompts{
		MemoryExtraction: "a", MemorySummary: "b", MemoryEnrichment: "c",
		MemorySummarize: "d", MemorySupersede: "e", Compaction: "f",
	}
	for key, want := range map[string]string{
		"memory_extraction": "a", "memory_summary": "b", "memory_enrichment": "c",
		"memory_summarize": "d", "memory_supersede": "e", "compaction": "f",
	} {
		if got := mechanismTemplateByKey(prompts, key); got != want {
			t.Fatalf("template %s: expected %q, got %q", key, want, got)
		}
	}
	if got := mechanismTemplateByKey(prompts, "ghost"); got != "" {
		t.Fatalf("unknown key must return empty, got %q", got)
	}
}
