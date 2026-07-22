package domain

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var ErrRevisionNotPublished = errors.New("resource revision is not published")

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
	"payload": {}, "raw_payload": {}, "prompt": {}, "raw_prompt": {}, "retrieved_content": {},
	"document_content": {}, "arguments": {}, "tool_arguments": {}, "raw_response": {},
	"tool_raw_response": {}, "encrypted_payload_ref": {}, "payload_ref": {}, "payload_hash": {},
	"content_hash":  {},
	"system_prompt": {}, "developer_prompt": {}, "api_token": {}, "bearer_token": {},
	"retrieved_chunks": {},
}

var summaryToken = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,63}$`)
var changeTypes = map[string]struct{}{"added": {}, "removed": {}, "modified": {}, "enabled": {}, "disabled": {}}
var sensitiveSafeSummaryAssignment = regexp.MustCompile(
	`(?i)(^|[\s;,])(?:api[_-]?key|access[_-]?token|client[_-]?secret)\s*[:=]\s*\S`,
)
var sensitiveSafeSummaryAuthorization = regexp.MustCompile(
	`(?i)(^|[\s;,])authorization\s*[:=]\s*(?:bearer|basic)\b`,
)

func validateSafeSummary(summary map[string]any) error {
	if len(summary) > 64 {
		return errors.New("safe summary has too many fields")
	}
	for key, value := range summary {
		normalized := NormalizeSafeSummaryKey(key)
		if IsSensitiveSafeSummaryKey(normalized) {
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
			if err := validateSafeSummaryValue(value, 0); err != nil {
				return fmt.Errorf("safe summary field %s invalid: %w", key, err)
			}
		}
	}
	return nil
}

func validateSafeSummaryValue(value any, depth int) error {
	if depth > 6 {
		return errors.New("maximum depth exceeded")
	}
	switch typed := value.(type) {
	case nil, bool, float64, int, int32, int64:
		return nil
	case string:
		if len(typed) > 2048 {
			return errors.New("string too long")
		}
		if IsSensitiveSafeSummaryValue(typed) {
			return errors.New("sensitive value")
		}
		return nil
	case []string:
		if len(typed) > 64 {
			return errors.New("too many items")
		}
		for _, item := range typed {
			if err := validateSafeSummaryValue(item, depth+1); err != nil {
				return err
			}
		}
		return nil
	case []any:
		if len(typed) > 64 {
			return errors.New("too many items")
		}
		for _, item := range typed {
			if err := validateSafeSummaryValue(item, depth+1); err != nil {
				return err
			}
		}
		return nil
	case map[string]string:
		converted := make(map[string]any, len(typed))
		for key, item := range typed {
			converted[key] = item
		}
		return validateSafeSummaryMap(converted, depth+1)
	case map[string]any:
		return validateSafeSummaryMap(typed, depth+1)
	default:
		return errors.New("value is not JSON-safe")
	}
}

func validateSafeSummaryMap(value map[string]any, depth int) error {
	if len(value) > 64 {
		return errors.New("too many fields")
	}
	for key, nested := range value {
		if IsSensitiveSafeSummaryKey(key) {
			return fmt.Errorf("sensitive key: %s", key)
		}
		if err := validateSafeSummaryValue(nested, depth); err != nil {
			return err
		}
	}
	return nil
}

func IsSensitiveSafeSummaryKey(key string) bool {
	_, sensitive := sensitiveSafeSummaryKeys[NormalizeSafeSummaryKey(key)]
	return sensitive
}

func IsSensitiveSafeSummaryValue(value string) bool {
	return sensitiveSafeSummaryAssignment.MatchString(value) || sensitiveSafeSummaryAuthorization.MatchString(value)
}

func NormalizeSafeSummaryKey(key string) string {
	key = strings.ReplaceAll(key, "-", "_")
	var normalized strings.Builder
	for index := 0; index < len(key); index++ {
		current := key[index]
		if current >= 'A' && current <= 'Z' {
			previousLowerOrDigit := index > 0 && ((key[index-1] >= 'a' && key[index-1] <= 'z') ||
				(key[index-1] >= '0' && key[index-1] <= '9'))
			nextLower := index+1 < len(key) && key[index+1] >= 'a' && key[index+1] <= 'z'
			previousUpper := index > 0 && key[index-1] >= 'A' && key[index-1] <= 'Z'
			if normalized.Len() > 0 && (previousLowerOrDigit || (previousUpper && nextLower)) {
				normalized.WriteByte('_')
			}
			normalized.WriteByte(current + ('a' - 'A'))
			continue
		}
		normalized.WriteByte(current)
	}
	return strings.ToLower(normalized.String())
}

func SanitizeSafeSummary(summary map[string]any) map[string]any {
	return sanitizeSafeSummaryMap(summary, 0)
}

func sanitizeSafeSummaryMap(summary map[string]any, depth int) map[string]any {
	if depth > 6 || len(summary) > 64 {
		return map[string]any{}
	}
	result := make(map[string]any, len(summary))
	for key, value := range summary {
		if len(key) > 64 || IsSensitiveSafeSummaryKey(key) {
			continue
		}
		if sanitized, ok := sanitizeSafeSummaryValue(value, depth); ok {
			result[key] = sanitized
		}
	}
	return result
}

func sanitizeSafeSummaryValue(value any, depth int) (any, bool) {
	if depth > 6 {
		return nil, false
	}
	switch typed := value.(type) {
	case nil, bool, float64, int, int32, int64:
		return typed, true
	case string:
		return typed, len(typed) <= 2048 && !IsSensitiveSafeSummaryValue(typed)
	case []any:
		if len(typed) > 64 {
			return nil, false
		}
		result := make([]any, 0, len(typed))
		for _, item := range typed {
			sanitized, ok := sanitizeSafeSummaryValue(item, depth+1)
			if !ok {
				return nil, false
			}
			result = append(result, sanitized)
		}
		return result, true
	case map[string]any:
		result := sanitizeSafeSummaryMap(typed, depth+1)
		if len(typed) > 0 && len(result) == 0 {
			return nil, false
		}
		return result, true
	default:
		return nil, false
	}
}
