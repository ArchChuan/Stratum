package persistence

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PgSkillLookup implements port.SkillLookup using pgxpool.
type PgSkillLookup struct {
	db *pgxpool.Pool
}

func NewPgSkillLookup(db *pgxpool.Pool) *PgSkillLookup {
	return &PgSkillLookup{db: db}
}

func (s *PgSkillLookup) LookupSkill(ctx context.Context, tenantID, skillID string) (string, string, error) {
	schema := fmt.Sprintf(`"tenant_%s"`, tenantID)
	var name, description string
	err := s.db.QueryRow(ctx,
		fmt.Sprintf(`SELECT name, description FROM %s.skills WHERE id=$1`, schema),
		skillID,
	).Scan(&name, &description)
	if err != nil {
		// not found is a no-op; caller falls back to skillID as name
		return "", "", nil
	}
	return name, description, nil
}
