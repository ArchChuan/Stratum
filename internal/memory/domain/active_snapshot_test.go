package domain

import (
	"strings"
	"testing"
	"time"

	"github.com/byteBuilderX/stratum/pkg/constants"
)

func TestActiveSnapshotValidateBoundsStructuredContent(t *testing.T) {
	now := time.Now().UTC()
	valid := &ActiveSnapshot{
		TenantID: "tenant", UserID: "user", AgentID: "agent",
		WorkContext: []string{"shipping phase one"}, PersonalContext: []string{"prefers concise answers"},
		TopOfMind: []string{"migration safety"}, Source: SnapshotSource{Type: "message", Reference: "msg-1"},
		ExpiresAt: now.Add(time.Hour), UpdatedAt: now, Status: SnapshotStatusActive,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid snapshot rejected: %v", err)
	}

	tooLarge := *valid
	tooLarge.TopOfMind = []string{strings.Repeat("x", constants.ActiveSnapshotItemMaxRunes+1)}
	if err := tooLarge.Validate(); err == nil {
		t.Fatal("expected oversized item to be rejected")
	}

	tooMany := *valid
	tooMany.WorkContext = make([]string, constants.ActiveSnapshotSectionMaxItems+1)
	if err := tooMany.Validate(); err == nil {
		t.Fatal("expected oversized section to be rejected")
	}
}

func TestActiveSnapshotValidateRequiresScopeTTLAndMinimalSource(t *testing.T) {
	now := time.Now().UTC()
	base := ActiveSnapshot{TenantID: "tenant", UserID: "user", AgentID: "agent", Source: SnapshotSource{Type: "message", Reference: "msg-1"}, ExpiresAt: now.Add(time.Hour), UpdatedAt: now, Status: SnapshotStatusActive}

	for name, mutate := range map[string]func(*ActiveSnapshot){
		"user":             func(s *ActiveSnapshot) { s.UserID = "" },
		"agent":            func(s *ActiveSnapshot) { s.AgentID = "" },
		"ttl":              func(s *ActiveSnapshot) { s.ExpiresAt = s.UpdatedAt },
		"source type":      func(s *ActiveSnapshot) { s.Source.Type = "" },
		"source reference": func(s *ActiveSnapshot) { s.Source.Reference = "" },
		"source": func(s *ActiveSnapshot) {
			s.Source.Reference = strings.Repeat("x", constants.ActiveSnapshotSourceRefMaxRunes+1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			s := base
			mutate(&s)
			if err := s.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
