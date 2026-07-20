package persistence

import (
	"context"
	"strings"
	"testing"
)

func TestSkillLookupRejectsInvalidTenantBeforeUsingPool(t *testing.T) {
	_, _, err := NewPgSkillLookup(nil).LookupSkill(context.Background(), `bad"tenant`, "skill-1")
	if err == nil || !strings.Contains(err.Error(), "postgres: invalid tenant_id") {
		t.Fatalf("expected shared tenant validation error, got %v", err)
	}
}
