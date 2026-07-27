// Package migration provides implementation for migration.
package migration

import (
	"fmt"
	"math"

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
	defer m.Close() //nolint:errcheck

	version, dirty, err := m.Version()
	if err != nil && err != migrate.ErrNoChange {
		// ErrNoChange means no migration has been applied yet — that's fine
		if err.Error() != "no migration" {
			return fmt.Errorf("migration: version check: %w", err)
		}
	}
	if dirty {
		logger.Warn("dirty migration detected, forcing version to retry",
			zap.Uint("version", version))
		if version == 0 || version > math.MaxInt32 {
			return fmt.Errorf("migration: force clean: invalid dirty version %d", version)
		}
		if err := m.Force(int(version) - 1); err != nil {
			return fmt.Errorf("migration: force clean: %w", err)
		}
	}

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("migration: up: %w", err)
	}

	logger.Info("public schema migration complete")
	return nil
}
