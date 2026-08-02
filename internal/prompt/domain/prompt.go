package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// PromptStatus represents the lifecycle stage of a prompt template.
type PromptStatus string

const (
	PromptDraft     PromptStatus = "draft"
	PromptPublished PromptStatus = "published"
	PromptArchived  PromptStatus = "archived"
)

// PromptTemplate is a versioned, content-addressed prompt.
// Once published it is immutable — new versions are created as new rows.
type PromptTemplate struct {
	Key         string       `json:"key"`       // system_prompt, memory_extraction, ...
	TenantID    *string      `json:"tenant_id"` // nil = global
	Version     int          `json:"version"`
	Content     string       `json:"content"`
	Status      PromptStatus `json:"status"`
	ContentHash string       `json:"content_hash"` // SHA-256 hex
	CreatedBy   string       `json:"created_by"`   // user:<id>|evolution:<cand_id>|system
	CreatedAt   time.Time    `json:"created_at"`
}

// PromptBinding wires a prompt key to specific versions for a scope
// (tenant or agent), with optional A/B traffic split.
type PromptBinding struct {
	Key             string `json:"key"`
	Scope           string `json:"scope"`             // tenant:<id>|agent:<id>
	StableVersionID string `json:"stable_version_id"` // always-served version
	CanaryVersionID string `json:"canary_version_id"` // experimental version
	TrafficPercent  int    `json:"traffic_percent"`   // 0-100, % routed to canary
}

// ComputeHash returns the SHA-256 hex digest of the content.
func ComputeHash(content string) string {
	h := sha256.Sum256([]byte(content))
	return hex.EncodeToString(h[:])
}
