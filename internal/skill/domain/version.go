package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

type VersionStatus string

const (
	VersionStatusDraft      VersionStatus = "draft"
	VersionStatusCandidate  VersionStatus = "candidate"
	VersionStatusPublished  VersionStatus = "published"
	VersionStatusDeprecated VersionStatus = "deprecated"
)

// SkillRevision is a versioned snapshot of a skill's editable surface. The
// revision carries its own name/description/instructions so each version is
// self-contained; the skills product row only mirrors the active values for
// list display.
type SkillRevision struct {
	ID                 string
	SkillID            string
	ParentRevisionID   string
	RevisionNo         int
	Status             VersionStatus
	Source             string
	ContentHash        string
	Name               string
	Description        string
	Instructions       string
	GenerationMetadata map[string]any
	PublishChecks      map[string]any
	// CreatedBy traces who authored this revision (draft author, not ownership).
	CreatedBy string
}

func (v SkillRevision) ComputeContentHash() (string, error) {
	payload := struct {
		Name         string `json:"name"`
		Description  string `json:"description"`
		Instructions string `json:"instructions"`
	}{
		Name:         v.Name,
		Description:  v.Description,
		Instructions: v.Instructions,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal skill version content: %w", err)
	}
	hash := sha256.Sum256(encoded)
	return hex.EncodeToString(hash[:]), nil
}

func (v SkillRevision) ValidatePublishable() error {
	if strings.TrimSpace(v.Name) == "" {
		return fmt.Errorf("skill name required: %w", ErrSkillNotPublishable)
	}
	if strings.TrimSpace(v.Description) == "" {
		return fmt.Errorf("skill description required: %w", ErrSkillNotPublishable)
	}
	if strings.TrimSpace(v.Instructions) == "" {
		return fmt.Errorf("instructions required: %w", ErrSkillNotPublishable)
	}
	return nil
}
