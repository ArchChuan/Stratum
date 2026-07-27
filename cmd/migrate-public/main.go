package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"go.uber.org/zap"

	"github.com/byteBuilderX/stratum/pkg/migration"
	"github.com/byteBuilderX/stratum/pkg/observability"
)

type migrateFunc func(databaseURL, sqlDir string, logger *zap.Logger) error

func main() {
	logger, err := observability.NewLogger(os.Getenv("APP_ENV"))
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "create logger: %v\n", err)
		os.Exit(1)
	}
	exitCode := 0
	if err := run(os.Args[1:], os.Getenv, logger, migration.RunPublicSchema); err != nil {
		logger.Error("public schema migration failed", zap.Error(err))
		exitCode = 1
	}
	_ = logger.Sync()
	os.Exit(exitCode)
}

func run(args []string, getenv func(string) string, logger *zap.Logger, migrate migrateFunc) error {
	flags := flag.NewFlagSet("migrate-public", flag.ContinueOnError)
	sqlDir := flags.String("sql-dir", "pkg/migration/sql", "public schema migration directory")
	if err := flags.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	databaseURL := getenv("POSTGRES_URL")
	if databaseURL == "" {
		return errors.New("POSTGRES_URL is required")
	}
	if err := migrate(databaseURL, *sqlDir, logger); err != nil {
		return fmt.Errorf("run public schema migration: %w", err)
	}
	return nil
}
