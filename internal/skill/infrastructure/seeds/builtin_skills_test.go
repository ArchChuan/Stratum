package seeds

import (
	"testing"
)

// TestBuiltinSkillsContentHash verifies that each built-in skill has a
// deterministic, non-empty content hash.
func TestBuiltinSkillsContentHash(t *testing.T) {
	skills := BuiltinSkills()
	if len(skills) != 2 {
		t.Fatalf("expected 2 built-in skills, got %d", len(skills))
	}
	for _, sk := range skills {
		t.Run(sk.ID, func(t *testing.T) {
			if sk.Revision.ContentHash == "" {
				t.Fatal("content hash is empty")
			}
			if len(sk.Revision.ContentHash) != 64 {
				t.Fatalf("expected 64-char SHA-256 hex, got %d chars", len(sk.Revision.ContentHash))
			}
			// Verify determinism: same input → same hash
			h2, err := sk.Revision.ComputeContentHash()
			if err != nil {
				t.Fatalf("re-compute content hash: %v", err)
			}
			if h2 != sk.Revision.ContentHash {
				t.Fatalf("content hash mismatch: stored=%s recomputed=%s", sk.Revision.ContentHash, h2)
			}
		})
	}
}

// TestBuiltinSkillsContractNames verifies activation contract names are valid.
func TestBuiltinSkillsContractNames(t *testing.T) {
	for _, sk := range BuiltinSkills() {
		t.Run(sk.ID, func(t *testing.T) {
			if err := sk.Revision.ActivationContract.Validate(); err != nil {
				t.Fatalf("activation contract validation: %v", err)
			}
		})
	}
}

// TestBuiltinSkillsPublishable verifies each seed revision passes publish
// validation.
func TestBuiltinSkillsPublishable(t *testing.T) {
	for _, sk := range BuiltinSkills() {
		t.Run(sk.ID, func(t *testing.T) {
			if err := sk.Revision.ValidatePublishable(0); err != nil {
				t.Fatalf("publishable: %v", err)
			}
		})
	}
}

// TestBuiltinSkillsSQLGolden prints the generated SQL for review.
// Run with: go test -v -run TestBuiltinSkillsSQLGolden
func TestBuiltinSkillsSQLGolden(t *testing.T) {
	sql := SkillSQL()
	if sql == "" {
		t.Fatal("SkillSQL returned empty string")
	}
	// Golden: the SQL must contain key identifiers.
	for _, want := range []string{
		"builtin:platform-guide",
		"builtin:tenant-diagnostic",
		"rev-builtin-platform-guide-v1",
		"rev-builtin-tenant-diagnostic-v1",
		"stratum-platform-assistant",
		"ON CONFLICT (id) DO NOTHING",
		"ON CONFLICT (agent_id, skill_id) DO NOTHING",
	} {
		if !contains(sql, want) {
			t.Errorf("expected SQL to contain %q", want)
		}
	}
	t.Logf("\n%s", sql)
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && search(s, substr)
}

func search(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
