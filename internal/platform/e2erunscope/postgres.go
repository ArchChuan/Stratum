package e2erunscope

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

const databaseOperationTimeout = 15 * time.Second

type DatabaseAdmin interface {
	Exec(context.Context, string) error
	Exists(context.Context, string) (bool, error)
	Close(context.Context) error
}

type pgxDatabaseAdmin struct{ conn *pgx.Conn }

func NewPostgresAdmin(ctx context.Context, baseDSN string) (DatabaseAdmin, error) {
	maintenance, err := MaintenanceURL(baseDSN)
	if err != nil {
		return nil, err
	}
	connectCtx, cancel := context.WithTimeout(ctx, databaseOperationTimeout)
	defer cancel()
	conn, err := pgx.Connect(connectCtx, maintenance)
	if err != nil {
		return nil, errors.New("connect database admin failed")
	}
	return &pgxDatabaseAdmin{conn: conn}, nil
}

func (a *pgxDatabaseAdmin) Exec(ctx context.Context, query string) error {
	_, err := a.conn.Exec(ctx, query)
	return err
}

func (a *pgxDatabaseAdmin) Exists(ctx context.Context, name string) (bool, error) {
	var exists bool
	err := a.conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname=$1)`, name).Scan(&exists)
	return exists, err
}

func (a *pgxDatabaseAdmin) Close(ctx context.Context) error { return a.conn.Close(ctx) }

var protectedDatabaseNames = map[string]bool{"postgres": true, "template0": true, "template1": true}

func validateDatabaseName(name string) error {
	if !databasePattern.MatchString(name) || protectedDatabaseNames[name] {
		return errors.New("database lifecycle: unsafe database name")
	}
	return nil
}

func CreateDatabase(ctx context.Context, admin DatabaseAdmin, name string) error {
	ctx, cancel := context.WithTimeout(ctx, databaseOperationTimeout)
	defer cancel()
	if err := validateDatabaseName(name); err != nil {
		return err
	}
	exists, err := admin.Exists(ctx, name)
	if err != nil {
		return fmt.Errorf("check database: %w", err)
	}
	if exists {
		return errors.New("database already exists")
	}
	if err := admin.Exec(ctx, `CREATE DATABASE "`+name+`"`); err != nil {
		return fmt.Errorf("create database: %w", err)
	}
	return nil
}

func DropDatabase(ctx context.Context, admin DatabaseAdmin, name string) error {
	ctx, cancel := context.WithTimeout(ctx, databaseOperationTimeout)
	defer cancel()
	if err := validateDatabaseName(name); err != nil {
		return err
	}
	exists, err := admin.Exists(ctx, name)
	if err != nil {
		return fmt.Errorf("check database: %w", err)
	}
	if !exists {
		return nil
	}
	if err := admin.Exec(ctx, `DROP DATABASE "`+name+`" WITH (FORCE)`); err != nil {
		return fmt.Errorf("drop database: %w", err)
	}
	return nil
}

func ReapDatabases(
	ctx context.Context,
	registry Registry,
	admin DatabaseAdmin,
	now time.Time,
	processAlive func(int, string) bool,
) ([]string, error) {
	stale, err := registry.Stale(now, processAlive)
	if err != nil {
		return nil, fmt.Errorf("find stale runs: %w", err)
	}
	removed := make([]string, 0, len(stale))
	for _, scope := range stale {
		if err := DropDatabase(ctx, admin, scope.DatabaseName); err != nil {
			return removed, err
		}
		if _, err := registry.Release(scope.RunID); err != nil {
			return removed, fmt.Errorf("release stale run %s: %w", scope.RunID, err)
		}
		removed = append(removed, scope.DatabaseName)
	}
	return removed, nil
}
