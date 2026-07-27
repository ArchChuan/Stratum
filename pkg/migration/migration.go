// Package migration provides implementation for migration.
package migration

import (
	"errors"
	"fmt"
	"net/url"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"go.uber.org/zap"
)

func RunPublicSchema(postgresURL string, sqlDir string, logger *zap.Logger) error {
	databaseURL, err := driverURL(postgresURL)
	if err != nil {
		return fmt.Errorf("migration: database URL: %w", err)
	}
	m, err := migrate.New("file://"+sqlDir, databaseURL)
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

func driverURL(postgresURL string) (string, error) {
	parsed, err := url.Parse(postgresURL)
	if err != nil {
		return "", errors.New("invalid URL")
	}
	switch parsed.Scheme {
	case "postgres", "postgresql":
		parsed.Scheme = "pgx5"
	case "pgx5":
	default:
		return "", errors.New("unsupported database scheme")
	}
	return parsed.String(), nil
}
