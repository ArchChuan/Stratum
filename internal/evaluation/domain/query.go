package domain

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var (
	ErrInvalidCenterQuery     = errors.New("invalid evaluation center query")
	ErrCenterResourceNotFound = errors.New("evaluation center resource not found")
)

type CenterCursor struct {
	CreatedAt time.Time `json:"created_at"`
	ID        string    `json:"id"`
}

func EncodeCenterCursor(createdAt time.Time, id string) string {
	b, _ := json.Marshal(CenterCursor{CreatedAt: createdAt.UTC(), ID: id})
	return base64.RawURLEncoding.EncodeToString(b)
}

func DecodeCenterCursor(value string) (CenterCursor, error) {
	var cursor CenterCursor
	b, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(b, &cursor) != nil || cursor.CreatedAt.IsZero() || strings.TrimSpace(cursor.ID) == "" {
		return cursor, ErrInvalidCenterQuery
	}
	return cursor, nil
}

type CenterOverview struct {
	Resources   int `json:"resources"`
	Suites      int `json:"suites"`
	Runs        int `json:"runs"`
	Candidates  int `json:"candidates"`
	Experiments int `json:"experiments"`
}

type ResourceSummary struct {
	ID               string         `json:"id"`
	ResourceID       string         `json:"resource_id"`
	Status           string         `json:"status"`
	StableRevisionID string         `json:"stable_revision_id,omitempty"`
	LatestRunStatus  string         `json:"latest_run_status,omitempty"`
	ResourceKind     ResourceKind   `json:"resource_kind"`
	SafeSummary      map[string]any `json:"safe_summary"`
	CreatedAt        time.Time      `json:"created_at"`
}

type SuiteSummary struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
}
type RunSummary struct {
	ID           string       `json:"id"`
	ResourceID   string       `json:"resource_id"`
	RevisionID   string       `json:"revision_id"`
	Status       string       `json:"status"`
	ResourceKind ResourceKind `json:"resource_kind"`
	Passed       bool         `json:"passed"`
	TotalCases   int          `json:"total_cases"`
	PassedCases  int          `json:"passed_cases"`
	CreatedAt    time.Time    `json:"created_at"`
}
type CandidateSummary struct {
	ID               string         `json:"id"`
	ResourceID       string         `json:"resource_id"`
	RevisionID       string         `json:"revision_id"`
	ParentRevisionID string         `json:"parent_revision_id"`
	Source           string         `json:"source"`
	Status           string         `json:"status"`
	ResourceKind     ResourceKind   `json:"resource_kind"`
	Rank             *int           `json:"rank,omitempty"`
	StateVersion     int64          `json:"state_version"`
	SafeDiff         map[string]any `json:"safe_diff"`
	CreatedAt        time.Time      `json:"created_at"`
}
type ExperimentSummary struct {
	ID               string       `json:"id"`
	ResourceID       string       `json:"resource_id"`
	StableRevisionID string       `json:"stable_revision_id"`
	CanaryRevisionID string       `json:"canary_revision_id"`
	Status           string       `json:"status"`
	Recommendation   string       `json:"recommendation"`
	ResourceKind     ResourceKind `json:"resource_kind"`
	StagePercent     int          `json:"stage_percent"`
	SafetyStopped    bool         `json:"safety_stopped"`
	StateVersion     int64        `json:"state_version"`
	CreatedAt        time.Time    `json:"created_at"`
}

type TimelineEvent struct {
	ID           string       `json:"id"`
	Kind         string       `json:"kind"`
	Status       string       `json:"status"`
	Summary      string       `json:"summary"`
	ResourceID   string       `json:"resource_id"`
	ResourceKind ResourceKind `json:"resource_kind"`
	CreatedAt    time.Time    `json:"created_at"`
}

type ResourcePage struct {
	Items      []ResourceSummary `json:"items"`
	NextCursor string            `json:"next_cursor,omitempty"`
}
type SuitePage struct {
	Items      []SuiteSummary `json:"items"`
	NextCursor string         `json:"next_cursor,omitempty"`
}
type RunPage struct {
	Items      []RunSummary `json:"items"`
	NextCursor string       `json:"next_cursor,omitempty"`
}
type CandidatePage struct {
	Items      []CandidateSummary `json:"items"`
	NextCursor string             `json:"next_cursor,omitempty"`
}
type ExperimentPage struct {
	Items      []ExperimentSummary `json:"items"`
	NextCursor string              `json:"next_cursor,omitempty"`
}
type TimelinePage struct {
	Items      []TimelineEvent `json:"items"`
	NextCursor string          `json:"next_cursor,omitempty"`
}
