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
	// ResourceKindMechanism 被测对象是机制面模型档案（model_profiles 族档位）：
	// 基准集 × 档案矩阵评测（机制基线设计 §5），adapter 用档案声明的模型/模板执行 case。
	ResourceKindMechanism ResourceKind = "mechanism"
)

func (k ResourceKind) Validate() error {
	if k == "" {
		return errors.New("resource kind required")
	}
	switch k {
	case ResourceKindSkill, ResourceKindAgent, ResourceKindMCP, ResourceKindKnowledge, ResourceKindMechanism:
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

func (r ResourceRevision) CanEvaluateOffline() bool {
	return r.Status == RevisionStatusPublished ||
		(r.Status == RevisionStatusDraft && r.Source == RevisionSourceOptimization)
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

const (
	maxSafeSummaryDepth     = 6    // 容器嵌套最大深度（与 sanitize 路径一致）
	maxSafeSummaryItems     = 64   // slice / map 元素上限
	maxSafeSummaryKeyLen    = 64   // 键名长度上限（sanitize 路径）
	maxSafeSummaryStringLen = 2048 // 单个 string 值上限
)

// sensitiveSafeSummaryKeys 是敏感 token 种子集：键名 normalize 后，任一 token
// 以串首/串尾、分隔符(_.-)或数字为边界出现在键名中即视为敏感，由此覆盖
// x_api_key、oauth_token、secret1、APIKey3 等变体；tokens/tokenizer/secretary
// 及 token_count/prompt_version 等元数据键因边界规则不命中。
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
	`(?i)(^|[^A-Za-z0-9_-])["']?(?:api[_-]?key|access[_-]?token|client[_-]?secret)["']?\s*[:=]\s*["']?\S`,
)
var sensitiveSafeSummaryAuthorization = regexp.MustCompile(
	`(?i)(^|[^A-Za-z0-9_-])["']?authorization["']?\s*[:=]\s*["']?(?:bearer|basic)\b`,
)

func validateSafeSummary(summary map[string]any) error {
	if len(summary) > maxSafeSummaryItems {
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
	if depth > maxSafeSummaryDepth {
		return errors.New("maximum depth exceeded")
	}
	switch typed := value.(type) {
	case nil, bool, float64, int, int32, int64:
		return nil
	case string:
		return validateSafeSummaryString(typed)
	case []string:
		return validateSafeSummaryStringSlice(typed, depth)
	case []any:
		return validateSafeSummaryAnySlice(typed, depth)
	case map[string]string:
		return validateSafeSummaryMap(convertSafeSummaryStringMap(typed), depth+1)
	case map[string]any:
		return validateSafeSummaryMap(typed, depth+1)
	default:
		return errors.New("value is not JSON-safe")
	}
}

func validateSafeSummaryString(value string) error {
	if len(value) > maxSafeSummaryStringLen {
		return errors.New("string too long")
	}
	if IsSensitiveSafeSummaryValue(value) {
		return errors.New("sensitive value")
	}
	return nil
}

func validateSafeSummaryStringSlice(values []string, depth int) error {
	if len(values) > maxSafeSummaryItems {
		return errors.New("too many items")
	}
	for _, item := range values {
		if err := validateSafeSummaryValue(item, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func validateSafeSummaryAnySlice(values []any, depth int) error {
	if len(values) > maxSafeSummaryItems {
		return errors.New("too many items")
	}
	for _, item := range values {
		if err := validateSafeSummaryValue(item, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func convertSafeSummaryStringMap(typed map[string]string) map[string]any {
	converted := make(map[string]any, len(typed))
	for key, item := range typed {
		converted[key] = item
	}
	return converted
}

func validateSafeSummaryMap(value map[string]any, depth int) error {
	if len(value) > maxSafeSummaryItems {
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
	return isSensitiveNormalizedKey(NormalizeSafeSummaryKey(key))
}

// isSensitiveNormalizedKey 报告 normalize 后的键名是否含敏感 token：任一种子
// token 在键名中出现，且左侧边界为串首/分隔符/数字、右侧为串尾/数字。
// 由此 x_api_key（api_key 前缀 x_）、oauth_token、auth_token2、secret1、
// APIKey3 均命中；tokens/tokenizer/secretary 不命中（token 后跟字母），
// token_count、prompt_version 等元数据键（token 后跟分隔符）也不命中。
func isSensitiveNormalizedKey(normalized string) bool {
	for token := range sensitiveSafeSummaryKeys {
		if hasSensitiveToken(normalized, token) {
			return true
		}
	}
	return false
}

// hasSensitiveToken 在 normalized 中扫描 token 的全部出现位置，任一位置
// 两侧都是敏感边界即命中（如 secret_secret）。
func hasSensitiveToken(normalized, token string) bool {
	for offset := 0; ; {
		index := strings.Index(normalized[offset:], token)
		if index < 0 {
			return false
		}
		index += offset
		if isSensitiveTokenBoundary(normalized, index-1, true) &&
			isSensitiveTokenBoundary(normalized, index+len(token), false) {
			return true
		}
		offset = index + 1
	}
}

// isSensitiveTokenBoundary 报告 token 一侧是否为敏感边界：串外恒为边界。
// 左侧（left=true）还接受分隔符(_.-)或数字，覆盖 x_api_key、oauth_token
// 等前缀组合；右侧只接受数字，分隔符不是边界——token_count、prompt_version
// 等元数据键的 token 后跟分隔符，故不命中。字母两侧都不构成边界，
// tokens/tokenizer/secretary 不被命中。
func isSensitiveTokenBoundary(normalized string, position int, left bool) bool {
	if position < 0 || position >= len(normalized) {
		return true
	}
	switch current := normalized[position]; {
	case current == '_' || current == '.' || current == '-':
		return left
	case current >= '0' && current <= '9':
		return true
	}
	return false
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
	if depth > maxSafeSummaryDepth || len(summary) > maxSafeSummaryItems {
		return map[string]any{}
	}
	result := make(map[string]any, len(summary))
	for key, value := range summary {
		if len(key) > maxSafeSummaryKeyLen || IsSensitiveSafeSummaryKey(key) {
			continue
		}
		if sanitized, ok := sanitizeSafeSummaryValue(value, depth); ok {
			result[key] = sanitized
		}
	}
	return result
}

func sanitizeSafeSummaryValue(value any, depth int) (any, bool) {
	if depth > maxSafeSummaryDepth {
		return nil, false
	}
	switch typed := value.(type) {
	case nil, bool, float64, int, int32, int64:
		return typed, true
	case string:
		return typed, len(typed) <= maxSafeSummaryStringLen && !IsSensitiveSafeSummaryValue(typed)
	case []any:
		if len(typed) > maxSafeSummaryItems {
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
