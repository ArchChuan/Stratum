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
		"description": "rotate the access_token and password documentation",
	}
	if err := revision.Validate(); err != nil {
		t.Fatalf("benign value rejected: %v", err)
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
		SafeSummary:  map[string]any{"name": "classifier"},
		CreatedBy:    "user-1",
		CreatedAt:    time.Now(),
	}
}
