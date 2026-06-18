package port

import "context"

// SkillLookup resolves a skill's name and description from persistent storage.
// Used by handlers to annotate ToolDefinitions without importing a DB pool.
type SkillLookup interface {
	// LookupSkill returns the name and description for the given skill ID in the
	// given tenant schema. Returns ("", "", nil) when the skill is not found.
	LookupSkill(ctx context.Context, tenantID, skillID string) (name, description string, err error)
}
