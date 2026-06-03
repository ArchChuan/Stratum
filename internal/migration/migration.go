// Package migration provides implementation for migration.
package migration

import (
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"go.uber.org/zap"
)

func RunPublicSchema(postgresURL string, sqlDir string, logger *zap.Logger) error {
	m, err := migrate.New("file://"+sqlDir, postgresURL)
	if err != nil {
		return fmt.Errorf("migration: init: %w", err)
	}
	defer m.Close()

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("migration: up: %w", err)
	}

	logger.Info("public schema migration complete")
	return nil
}
