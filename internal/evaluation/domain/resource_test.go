package domain

import (
	"strings"
	"testing"
	"time"
)

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
	}
	for _, value := range unsafe {
		if !IsSensitiveSafeSummaryValue(value) {
			t.Errorf("unsafe value %q was not classified", value)
		}
		if result := SanitizeSafeSummary(map[string]any{"note": value}); len(result) != 0 {
			t.Errorf("unsafe value survived sanitization: %#v", result)
		}
	}
	for _, value := range []string{"token_count=10", "API key rotation policy", "authorization guide"} {
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
