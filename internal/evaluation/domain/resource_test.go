package domain

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestResourceRevisionOfflineEvaluationEligibility(t *testing.T) {
	for _, tc := range []struct {
		name     string
		revision ResourceRevision
		want     bool
	}{
		{name: "published baseline", revision: ResourceRevision{Status: RevisionStatusPublished, Source: RevisionSourceManual}, want: true},
		{name: "optimization candidate", revision: ResourceRevision{Status: RevisionStatusDraft, Source: RevisionSourceOptimization}, want: true},
		{name: "manual draft", revision: ResourceRevision{Status: RevisionStatusDraft, Source: RevisionSourceManual}, want: false},
		{name: "rollback draft", revision: ResourceRevision{Status: RevisionStatusDraft, Source: RevisionSourceRollback}, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.revision.CanEvaluateOffline(); got != tc.want {
				t.Fatalf("CanEvaluateOffline()=%v want %v", got, tc.want)
			}
		})
	}
}

func TestResourceKindValidate(t *testing.T) {
	tests := []struct {
		name    string
		kind    ResourceKind
		wantErr bool
	}{
		{name: "skill", kind: ResourceKindSkill},
		{name: "agent", kind: ResourceKindAgent},
		{name: "mcp", kind: ResourceKindMCP},
		{name: "knowledge", kind: ResourceKindKnowledge},
		{name: "unknown workflow", kind: "workflow", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.kind.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestResourceRefValidateRequiresKindAndIDs(t *testing.T) {
	tests := []struct {
		name    string
		ref     ResourceRef
		wantErr string
	}{
		{name: "valid", ref: ResourceRef{Kind: ResourceKindSkill, ResourceID: "skill-1", RevisionID: "revision-1"}},
		{name: "kind", ref: ResourceRef{ResourceID: "skill-1", RevisionID: "revision-1"}, wantErr: "resource kind required"},
		{name: "resource id", ref: ResourceRef{Kind: ResourceKindSkill, RevisionID: "revision-1"}, wantErr: "resource id required"},
		{name: "revision id", ref: ResourceRef{Kind: ResourceKindSkill, ResourceID: "skill-1"}, wantErr: "revision id required"},
		{name: "unknown kind", ref: ResourceRef{Kind: "workflow", ResourceID: "workflow-1", RevisionID: "revision-1"}, wantErr: "unsupported resource kind"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.ref.Validate()
			if tt.wantErr == "" && err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("Validate() error = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestResourceRevisionValidateRequiresMetadata(t *testing.T) {
	valid := validResourceRevision()
	tests := []struct {
		name   string
		mutate func(*ResourceRevision)
	}{
		{name: "id", mutate: func(revision *ResourceRevision) { revision.ID = "" }},
		{name: "resource kind", mutate: func(revision *ResourceRevision) { revision.ResourceKind = "" }},
		{name: "resource id", mutate: func(revision *ResourceRevision) { revision.ResourceID = "" }},
		{name: "source", mutate: func(revision *ResourceRevision) { revision.Source = "" }},
		{name: "status", mutate: func(revision *ResourceRevision) { revision.Status = "" }},
		{name: "content hash", mutate: func(revision *ResourceRevision) { revision.ContentHash = "" }},
		{name: "payload ref", mutate: func(revision *ResourceRevision) { revision.PayloadRef = "" }},
		{name: "payload hash", mutate: func(revision *ResourceRevision) { revision.PayloadHash = "" }},
	}

	if err := valid.Validate(); err != nil {
		t.Fatalf("valid revision rejected: %v", err)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			revision := valid
			tt.mutate(&revision)
			if err := revision.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestResourceRevisionRejectsSensitiveSafeSummaryKeys(t *testing.T) {
	keys := []string{
		"password", "TOKEN", "api-key", "apikey", "Authorization", "secret", "access-token", "refresh_token",
	}

	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			revision := validResourceRevision()
			revision.SafeSummary = map[string]any{
				"nested": []any{map[string]any{key: "redacted"}},
			}
			if err := revision.Validate(); err == nil {
				t.Fatalf("expected sensitive key %q to be rejected", key)
			}
		})
	}
}

func TestResourceRevisionAllowsBenignSafeSummaryValues(t *testing.T) {
	revision := validResourceRevision()
	revision.SafeSummary = map[string]any{
		"resource_name":  "classifier",
		"changed_fields": []string{"instructions", "temperature"},
		"change_types":   []string{"modified"},
		"version_label":  "candidate-2",
	}
	if err := revision.Validate(); err != nil {
		t.Fatalf("benign value rejected: %v", err)
	}
}

func TestResourceRevisionAllowsAdapterDefinedJSONSafeSummary(t *testing.T) {
	revision := validResourceRevision()
	revision.SafeSummary = map[string]any{
		"label":        "客服技能",
		"capabilities": map[string]any{"tools": float64(3), "streaming": true},
	}
	if err := revision.Validate(); err != nil {
		t.Fatalf("adapter-defined safe summary rejected: %v", err)
	}
}

func TestResourceRevisionRejectsSensitiveKeysInTypedNestedMaps(t *testing.T) {
	revision := validResourceRevision()
	revision.SafeSummary = map[string]any{
		"nested": map[string]string{"token": "redacted"},
	}
	if err := revision.Validate(); err == nil {
		t.Fatal("expected sensitive key in typed nested map to be rejected")
	}
}

func TestSensitiveSafeSummaryKeyNormalizationVariants(t *testing.T) {
	for _, key := range []string{"cookie", "Session", "connection_string", "connection-string", "connectionString",
		"CERT", "privateKey", "apiKey", "refreshToken"} {
		if !IsSensitiveSafeSummaryKey(key) {
			t.Errorf("key %q was not classified as sensitive", key)
		}
	}
}

func TestSensitiveSafeSummaryAliasesAndSafeMetadata(t *testing.T) {
	for _, key := range []string{"system_prompt", "systemPrompt", "developer-prompt", "API_TOKEN", "bearerToken",
		"retrieved_chunks", "rawResponse", "toolArguments", "documentContent"} {
		if !IsSensitiveSafeSummaryKey(key) {
			t.Errorf("unsafe alias %q was not classified", key)
		}
	}
	for _, key := range []string{"prompt_version", "promptVersion", "token_count", "prompt_hash", "model_token_limit"} {
		if IsSensitiveSafeSummaryKey(key) {
			t.Errorf("safe metadata %q was classified as sensitive", key)
		}
	}
}

func TestSensitiveSafeSummaryKeyDigitSuffixAndEmbeddedVariants(t *testing.T) {
	for _, key := range []string{"X-Api-Key", "x-api-key", "oauth_token", "auth_token2", "secret1", "APIKey3"} {
		if !IsSensitiveSafeSummaryKey(key) {
			t.Errorf("sensitive variant %q was not classified", key)
		}
	}
	for _, key := range []string{
		"tokens", "tokenizer", "secretary", "resource_name", "token_count", "prompt_version", "model_token_limit",
	} {
		if IsSensitiveSafeSummaryKey(key) {
			t.Errorf("safe key %q was classified as sensitive", key)
		}
	}
}

func TestResourceRevisionRejectsSensitiveKeyVariants(t *testing.T) {
	for _, key := range []string{"X-Api-Key", "x-api-key", "oauth_token", "auth_token2", "secret1", "APIKey3"} {
		t.Run(key, func(t *testing.T) {
			revision := validResourceRevision()
			revision.SafeSummary = map[string]any{key: "redacted"}
			if err := revision.Validate(); err == nil {
				t.Fatalf("expected sensitive key %q to be rejected", key)
			}
		})
	}
}

func TestResourceRevisionAllowsTokenLikeSafeSummaryKeys(t *testing.T) {
	revision := validResourceRevision()
	revision.SafeSummary = map[string]any{
		"resource_name": "classifier",
		"tokens":        float64(5),
		"tokenizer":     "x",
		"secretary":     "y",
	}
	if err := revision.Validate(); err != nil {
		t.Fatalf("token-like safe keys rejected: %v", err)
	}
}

func TestSanitizeSafeSummaryStripsSensitiveKeyVariants(t *testing.T) {
	result := SanitizeSafeSummary(map[string]any{
		"X-Api-Key": "sk-1", "secret1": "s-2", "token2": "s-3", "label": "safe",
	})
	if len(result) != 1 || result["label"] != "safe" {
		t.Fatalf("sanitized summary = %#v", result)
	}
}

func TestSanitizeSafeSummaryOmitsUnsafeAndMalformedBranches(t *testing.T) {
	deep := map[string]any{"safe": "value"}
	for range 8 {
		deep = map[string]any{"nested": deep}
	}
	result := SanitizeSafeSummary(map[string]any{
		"label": "safe", "systemPrompt": "raw", "auth": map[string]any{"credentials": "secret"},
		"deep": deep, "prompt_version": "v2",
	})
	if result["label"] != "safe" || result["prompt_version"] != "v2" || len(result) != 2 {
		t.Fatalf("sanitized summary = %#v", result)
	}
}

func TestSensitiveSafeSummaryValueMarkers(t *testing.T) {
	unsafe := []string{
		"api_key=secret", "API_KEY = secret", "access_token: secret", "client_secret = secret",
		"Authorization: Bearer secret", "authorization = basic abc123",
		"https://example.test?api_key=secret", "note(api_key=secret)", `{"api_key":"secret"}`,
		"prefix?ACCESS_TOKEN=secret", `{"Authorization":"Bearer secret"}`,
	}
	for _, value := range unsafe {
		if !IsSensitiveSafeSummaryValue(value) {
			t.Errorf("unsafe value %q was not classified", value)
		}
		if result := SanitizeSafeSummary(map[string]any{"note": value}); len(result) != 0 {
			t.Errorf("unsafe value survived sanitization: %#v", result)
		}
	}
	for _, value := range []string{"token_count=10", "API key rotation policy", "authorization guide",
		"my_api_key_count=10", "my-api_key=metadata", "api_key_rotation_policy", "prompt_version=v2"} {
		if IsSensitiveSafeSummaryValue(value) {
			t.Errorf("safe value %q was classified", value)
		}
	}
}

func TestResourceRevisionRejectsFreeTextSummaryEvenWhenSecretIsOnlyInValue(t *testing.T) {
	revision := validResourceRevision()
	revision.SafeSummary = map[string]any{"description": "client_secret=synthetic-value"}
	if err := revision.Validate(); err == nil {
		t.Fatal("expected free-text safe summary field to be rejected")
	}
}

// TestValidateSafeSummaryValue locks the per-type validation rules after the
// validateSafeSummaryValue decomposition: same accept/reject and error text as
// the original single function.
func TestValidateSafeSummaryValue(t *testing.T) {
	bigString := strings.Repeat("a", maxSafeSummaryStringLen)
	tooLong := strings.Repeat("a", maxSafeSummaryStringLen+1)
	deep := map[string]any{"safe": "value"}
	for range maxSafeSummaryDepth {
		deep = map[string]any{"nested": deep}
	}
	overFieldCap := map[string]any{}
	for i := 0; i <= maxSafeSummaryItems; i++ {
		overFieldCap[fmt.Sprintf("k%d", i)] = i
	}

	tests := []struct {
		name    string
		value   any
		depth   int
		wantErr string
	}{
		{name: "nil scalar", value: nil},
		{name: "bool scalar", value: true},
		{name: "float scalar", value: 1.5},
		{name: "int scalar", value: 42},
		{name: "int32 scalar", value: int32(42)},
		{name: "int64 scalar", value: int64(42)},
		{name: "plain string", value: "hello"},
		{name: "string at length cap", value: bigString},
		{name: "string over length cap", value: tooLong, wantErr: "string too long"},
		{name: "string with secret marker", value: "api_key=secret", wantErr: "sensitive value"},
		{name: "string slice", value: []string{"a", "b"}},
		{name: "string slice over item cap", value: make([]string, maxSafeSummaryItems+1), wantErr: "too many items"},
		{name: "string slice with secret item", value: []string{"safe", "access_token: secret"}, wantErr: "sensitive value"},
		{name: "any slice", value: []any{float64(1), "two"}},
		{name: "any slice over item cap", value: make([]any, maxSafeSummaryItems+1), wantErr: "too many items"},
		{name: "any slice with nested secret key", value: []any{map[string]any{"token": "x"}}, wantErr: "sensitive key"},
		{name: "string map", value: map[string]string{"label": "客服技能"}},
		{name: "string map with sensitive key", value: map[string]string{"password": "x"}, wantErr: "sensitive key"},
		{name: "any map", value: map[string]any{"capabilities": map[string]any{"tools": float64(3)}}},
		{name: "any map with sensitive key", value: map[string]any{"api_key": "x"}, wantErr: "sensitive key"},
		{name: "map over field cap", value: overFieldCap, wantErr: "too many fields"},
		{name: "maximum depth exceeded", value: deep, wantErr: "maximum depth exceeded"},
		{name: "depth limit respected", value: map[string]any{"nested": map[string]any{"nested": map[string]any{"safe": "v"}}}},
		{name: "depth check precedes type check", value: "x", depth: maxSafeSummaryDepth + 1, wantErr: "maximum depth exceeded"},
		{name: "non-JSON value", value: struct{}{}, wantErr: "not JSON-safe"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateSafeSummaryValue(tc.value, tc.depth)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("validateSafeSummaryValue() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("validateSafeSummaryValue() error = %v, want containing %q", err, tc.wantErr)
			}
		})
	}
}

func validResourceRevision() ResourceRevision {
	return ResourceRevision{
		ID:           "revision-1",
		ResourceKind: ResourceKindSkill,
		ResourceID:   "skill-1",
		Source:       RevisionSourceManual,
		Status:       RevisionStatusDraft,
		ContentHash:  "content-hash",
		PayloadRef:   "payloads/revision-1",
		PayloadHash:  "payload-hash",
		SafeSummary:  map[string]any{"resource_name": "classifier"},
		CreatedBy:    "user-1",
		CreatedAt:    time.Now(),
	}
}
