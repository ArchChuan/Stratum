package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgTenantSettings implements port.TenantSettings using pgxpool.
type PgTenantSettings struct {
	db *pgxpool.Pool
}

func NewPgTenantSettings(db *pgxpool.Pool) *PgTenantSettings {
	return &PgTenantSettings{db: db}
}

func (t *PgTenantSettings) GetEmbedModel(ctx context.Context, tenantID string) (string, error) {
	var settingsJSON []byte
	err := t.db.QueryRow(ctx,
		"SELECT settings FROM public.tenants WHERE id=$1 AND deleted_at IS NULL",
		tenantID,
	).Scan(&settingsJSON)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("get tenant settings %s: %w", tenantID, err)
	}
	if len(settingsJSON) == 0 {
		return "", nil
	}
	var ts map[string]interface{}
	if err := json.Unmarshal(settingsJSON, &ts); err != nil {
		return "", fmt.Errorf("parse tenant settings %s: %w", tenantID, err)
	}
	model, _ := ts["embed_model"].(string)
	return model, nil
}
