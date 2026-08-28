package domain

import (
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/byteBuilderX/stratum/pkg/constants"
)

type SnapshotStatus string

const (
	SnapshotStatusActive   SnapshotStatus = "active"
	SnapshotStatusInactive SnapshotStatus = "inactive"
)

type SnapshotSource struct {
	Type      string `json:"type"`
	Reference string `json:"reference"`
}

type ActiveSnapshot struct {
	TenantID        string
	UserID          string
	AgentID         string
	// AgentName / ConversationName 仅管理页展示用（#24：ListUser JOIN
	// agents + 最近会话取名），不属于持久化状态，Upsert/Validate 不涉及。
	AgentName         string
	ConversationName  string
	WorkContext       []string
	PersonalContext   []string
	TopOfMind         []string
	Source            SnapshotSource
	ExpiresAt         time.Time
	UpdatedAt         time.Time
	Version           int64
	Status            SnapshotStatus
}

func (s *ActiveSnapshot) Validate() error {
	if s == nil {
		return fmt.Errorf("%w: active snapshot is required", ErrSnapshotInvalid)
	}
	if s.TenantID == "" || s.UserID == "" || s.AgentID == "" {
		return fmt.Errorf("%w: active snapshot tenant, user, and agent scope are required", ErrSnapshotInvalid)
	}
	if s.Status != SnapshotStatusActive && s.Status != SnapshotStatusInactive {
		return fmt.Errorf("%w: invalid active snapshot status %q", ErrSnapshotInvalid, s.Status)
	}
	if s.UpdatedAt.IsZero() || !s.ExpiresAt.After(s.UpdatedAt) {
		return fmt.Errorf("%w: active snapshot expiry must be after updated_at", ErrSnapshotInvalid)
	}
	if s.Source.Type == "" || s.Source.Reference == "" {
		return fmt.Errorf("%w: active snapshot source type and reference are required", ErrSnapshotInvalid)
	}
	if utf8.RuneCountInString(s.Source.Type) > 32 || utf8.RuneCountInString(s.Source.Reference) > constants.ActiveSnapshotSourceRefMaxRunes {
		return fmt.Errorf("%w: active snapshot source reference exceeds limit", ErrSnapshotInvalid)
	}
	total := 0
	for _, section := range [][]string{s.WorkContext, s.PersonalContext, s.TopOfMind} {
		if len(section) > constants.ActiveSnapshotSectionMaxItems {
			return fmt.Errorf("%w: active snapshot section exceeds item limit", ErrSnapshotInvalid)
		}
		for _, item := range section {
			n := utf8.RuneCountInString(item)
			if n > constants.ActiveSnapshotItemMaxRunes {
				return fmt.Errorf("%w: active snapshot item exceeds limit", ErrSnapshotInvalid)
			}
			total += n
		}
	}
	if total > constants.ActiveSnapshotTotalMaxRunes {
		return fmt.Errorf("%w: active snapshot exceeds total limit", ErrSnapshotInvalid)
	}
	return nil
}
