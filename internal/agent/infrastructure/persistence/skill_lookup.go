package persistence

import (
	"context"
	"errors"
	"fmt"

	pgstore "github.com/byteBuilderX/stratum/pkg/storage/postgres"
	"github.com/jackc/pgx/v5"
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
	var name, description string
	err := pgstore.Wrap(s.db).ExecTenant(ctx, tenantID, func(ctx context.Context, tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT name, description FROM skills WHERE id=$1`, skillID).Scan(&name, &description)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", nil
		}
		return "", "", fmt.Errorf("lookup skill %s: %w", skillID, err)
	}
	return name, description, nil
}
