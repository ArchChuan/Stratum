package domain

import (
	"errors"
	"fmt"
	"regexp"
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
	if err := validateSafeSummary(r.SafeSummary); err != nil {
		return err
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
	"client_secret": {}, "private_key": {}, "credential": {}, "credentials": {},
	"cookie": {}, "session": {}, "key": {}, "cert": {}, "connection_string": {},
}

var summaryToken = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,63}$`)
var changeTypes = map[string]struct{}{"added": {}, "removed": {}, "modified": {}, "enabled": {}, "disabled": {}}

func validateSafeSummary(summary map[string]any) error {
	if len(summary) > 4 {
		return errors.New("safe summary has too many fields")
	}
	for key, value := range summary {
		normalized := strings.ReplaceAll(strings.ToLower(key), "-", "_")
		if _, sensitive := sensitiveSafeSummaryKeys[normalized]; sensitive {
			return fmt.Errorf("safe summary contains sensitive key: %s", key)
		}
		switch normalized {
		case "resource_name":
			text, ok := value.(string)
			if !ok || len(text) == 0 || len(text) > 100 {
				return fmt.Errorf("safe summary resource_name invalid")
			}
		case "version_label":
			text, ok := value.(string)
			if !ok || !summaryToken.MatchString(text) {
				return fmt.Errorf("safe summary version_label invalid")
			}
		case "changed_fields":
			values, ok := value.([]string)
			if !ok || len(values) > 32 {
				return fmt.Errorf("safe summary changed_fields invalid")
			}
			for _, item := range values {
				if !summaryToken.MatchString(item) {
					return fmt.Errorf("safe summary changed_fields invalid")
				}
			}
		case "change_types":
			values, ok := value.([]string)
			if !ok || len(values) > 32 {
				return fmt.Errorf("safe summary change_types invalid")
			}
			for _, item := range values {
				if _, ok := changeTypes[item]; !ok {
					return fmt.Errorf("safe summary change_types invalid")
				}
			}
		default:
			return fmt.Errorf("safe summary field not allowed: %s", key)
		}
	}
	return nil
}
