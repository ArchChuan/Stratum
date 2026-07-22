package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

type ResourceKind string

const (
	ResourceKindSkill     ResourceKind = "skill"
	ResourceKindAgent     ResourceKind = "agent"
	ResourceKindMCP       ResourceKind = "mcp"
	ResourceKindKnowledge ResourceKind = "knowledge"
)

func (k ResourceKind) Validate() error {
	if k == "" {
		return errors.New("resource kind required")
	}
	switch k {
	case ResourceKindSkill, ResourceKindAgent, ResourceKindMCP, ResourceKindKnowledge:
		return nil
	default:
		return fmt.Errorf("unsupported resource kind: %s", k)
	}
}

type ResourceRef struct {
	Kind       ResourceKind `json:"kind"`
	ResourceID string       `json:"resource_id"`
	RevisionID string       `json:"revision_id"`
}

func (r ResourceRef) Validate() error {
	if err := r.Kind.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(r.ResourceID) == "" {
		return errors.New("resource id required")
	}
	if strings.TrimSpace(r.RevisionID) == "" {
		return errors.New("revision id required")
	}
	return nil
}

type RevisionSource string

const (
	RevisionSourceManual       RevisionSource = "manual"
	RevisionSourceOptimization RevisionSource = "optimization"
	RevisionSourceRollback     RevisionSource = "rollback"
)

func (s RevisionSource) validate() error {
	switch s {
	case RevisionSourceManual, RevisionSourceOptimization, RevisionSourceRollback:
		return nil
	case "":
		return errors.New("revision source required")
	default:
		return fmt.Errorf("unsupported revision source: %s", s)
	}
}

type RevisionStatus string

const (
	RevisionStatusDraft     RevisionStatus = "draft"
	RevisionStatusPublished RevisionStatus = "published"
)

func (s RevisionStatus) validate() error {
	switch s {
	case RevisionStatusDraft, RevisionStatusPublished:
		return nil
	case "":
		return errors.New("revision status required")
	default:
		return fmt.Errorf("unsupported revision status: %s", s)
	}
}

type ResourceRevision struct {
	ID               string         `json:"id"`
	ResourceKind     ResourceKind   `json:"resource_kind"`
	ResourceID       string         `json:"resource_id"`
	ParentRevisionID string         `json:"parent_revision_id,omitempty"`
	Source           RevisionSource `json:"source"`
	Status           RevisionStatus `json:"status"`
	ContentHash      string         `json:"content_hash"`
	PayloadRef       string         `json:"-"`
	PayloadHash      string         `json:"-"`
	SafeSummary      map[string]any `json:"safe_summary"`
	CreatedBy        string         `json:"created_by"`
	CreatedAt        time.Time      `json:"created_at"`
}

func (r ResourceRevision) Validate() error {
	if strings.TrimSpace(r.ID) == "" {
		return errors.New("revision id required")
	}
	if err := r.ResourceKind.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(r.ResourceID) == "" {
		return errors.New("resource id required")
	}
	if err := r.Source.validate(); err != nil {
		return err
	}
	if err := r.Status.validate(); err != nil {
		return err
	}
	if strings.TrimSpace(r.ContentHash) == "" {
		return errors.New("content hash required")
	}
	if strings.TrimSpace(r.PayloadRef) == "" {
		return errors.New("payload ref required")
	}
	if strings.TrimSpace(r.PayloadHash) == "" {
		return errors.New("payload hash required")
	}
	if key, ok := sensitiveSummaryKey(r.SafeSummary); ok {
		return fmt.Errorf("safe summary contains sensitive key: %s", key)
	}
	return nil
}

var sensitiveSafeSummaryKeys = map[string]struct{}{
	"password":      {},
	"token":         {},
	"api_key":       {},
	"apikey":        {},
	"authorization": {},
	"secret":        {},
	"access_token":  {},
	"refresh_token": {},
}

func sensitiveSummaryKey(value any) (string, bool) {
	switch typed := value.(type) {
	case map[string]any:
		for key, nested := range typed {
			normalized := strings.ReplaceAll(strings.ToLower(key), "-", "_")
			if _, sensitive := sensitiveSafeSummaryKeys[normalized]; sensitive {
				return key, true
			}
			if key, sensitive := sensitiveSummaryKey(nested); sensitive {
				return key, true
			}
		}
	case []any:
		for _, nested := range typed {
			if key, sensitive := sensitiveSummaryKey(nested); sensitive {
				return key, true
			}
		}
	}
	return "", false
}
