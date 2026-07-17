package domain

import (
	"errors"
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
	WorkContext     []string
	PersonalContext []string
	TopOfMind       []string
	Source          SnapshotSource
	ExpiresAt       time.Time
	UpdatedAt       time.Time
	Version         int64
	Status          SnapshotStatus
}

func (s *ActiveSnapshot) Validate() error {
	if s == nil {
		return errors.New("active snapshot is required")
	}
	if s.TenantID == "" || s.UserID == "" || s.AgentID == "" {
		return errors.New("active snapshot tenant, user, and agent scope are required")
	}
	if s.Status != SnapshotStatusActive && s.Status != SnapshotStatusInactive {
		return fmt.Errorf("invalid active snapshot status %q", s.Status)
	}
	if s.UpdatedAt.IsZero() || !s.ExpiresAt.After(s.UpdatedAt) {
		return errors.New("active snapshot expiry must be after updated_at")
	}
	if s.Source.Type == "" || s.Source.Reference == "" {
		return errors.New("active snapshot source type and reference are required")
	}
	if utf8.RuneCountInString(s.Source.Type) > 32 || utf8.RuneCountInString(s.Source.Reference) > constants.ActiveSnapshotSourceRefMaxRunes {
		return errors.New("active snapshot source reference exceeds limit")
	}
	total := 0
	for _, section := range [][]string{s.WorkContext, s.PersonalContext, s.TopOfMind} {
		if len(section) > constants.ActiveSnapshotSectionMaxItems {
			return errors.New("active snapshot section exceeds item limit")
		}
		for _, item := range section {
			n := utf8.RuneCountInString(item)
			if n > constants.ActiveSnapshotItemMaxRunes {
				return errors.New("active snapshot item exceeds limit")
			}
			total += n
		}
	}
	if total > constants.ActiveSnapshotTotalMaxRunes {
		return errors.New("active snapshot exceeds total limit")
	}
	return nil
}
