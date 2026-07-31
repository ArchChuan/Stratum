package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/byteBuilderX/stratum/internal/platform/e2erunscope"
	"golang.org/x/sys/unix"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("command is required")
	}
	handlers := map[string]func([]string, io.Writer, io.Writer) error{
		"allocate": runAllocate, "release": runRelease, "create-database": runCreateDatabase,
		"drop-database": runDropDatabase, "confirm-infrastructure-stopped": runConfirmStopped, "reap": runReap,
	}
	handler, ok := handlers[args[0]]
	if !ok {
		return fmt.Errorf("unknown command %q", args[0])
	}
	return handler(args[1:], stdout, stderr)
}

func runAllocate(args []string, stdout, stderr io.Writer) error {
	fs := newFlags("allocate", stderr)
	repository := fs.String("repository", "", "repository")
	root := fs.String("registry", "", "registry")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !filepath.IsAbs(*repository) || !filepath.IsAbs(*root) {
		return errors.New("repository and registry must be absolute")
	}
	scope, err := e2erunscope.NewScope(*repository, os.Getpid(), time.Now().UTC(), nil)
	if err != nil {
		return err
	}
	if err := (e2erunscope.Registry{Root: *root}).Register(scope); err != nil {
		return err
	}
	return writeJSON(stdout, scope)
}

func runRelease(args []string, stdout, stderr io.Writer) error {
	scopePath, root, err := parseScopeRegistryArgs("release", args, stderr)
	if err != nil {
		return err
	}
	scope, err := readScope(scopePath)
	if err != nil {
		return err
	}
	result, err := (e2erunscope.Registry{Root: root}).Release(scope.RunID)
	if err != nil {
		return err
	}
	return writeJSON(stdout, result)
}

func runCreateDatabase(args []string, _ io.Writer, stderr io.Writer) error {
	return runDatabase(args, stderr, e2erunscope.CreateDatabase)
}
func runDropDatabase(args []string, _ io.Writer, stderr io.Writer) error {
	return runDatabase(args, stderr, e2erunscope.DropDatabase)
}

func runDatabase(args []string, stderr io.Writer, operation func(context.Context, e2erunscope.DatabaseAdmin, string) error) error {
	fs := newFlags("database", stderr)
	scopePath := fs.String("scope", "", "scope")
	envName := fs.String("base-dsn-env", "", "DSN env")
	if err := fs.Parse(args); err != nil {
		return err
	}
	scope, err := readScope(*scopePath)
	if err != nil {
		return err
	}
	admin, err := openAdmin(*envName)
	if err != nil {
		return err
	}
	ctx := context.Background()
	return errors.Join(operation(ctx, admin, scope.DatabaseName), admin.Close(ctx))
}

func runConfirmStopped(args []string, _ io.Writer, stderr io.Writer) error {
	fs := newFlags("confirm", stderr)
	runID := fs.String("ownership-run-id", "", "run ID")
	root := fs.String("registry", "", "registry")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return (e2erunscope.Registry{Root: *root}).ConfirmInfrastructureStopped(*runID)
}

func runReap(args []string, stdout, stderr io.Writer) error {
	fs := newFlags("reap", stderr)
	root := fs.String("registry", "", "registry")
	envName := fs.String("base-dsn-env", "", "DSN env")
	if err := fs.Parse(args); err != nil {
		return err
	}
	admin, err := openAdmin(*envName)
	if err != nil {
		return err
	}
	ctx := context.Background()
	removed, opErr := e2erunscope.ReapDatabases(ctx, e2erunscope.Registry{Root: *root}, admin, time.Now().UTC(), processAlive)
	if err := errors.Join(opErr, admin.Close(ctx)); err != nil {
		return err
	}
	return writeJSON(stdout, removed)
}

func newFlags(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

func parseScopeRegistryArgs(name string, args []string, stderr io.Writer) (string, string, error) {
	fs := newFlags(name, stderr)
	scopePath := fs.String("scope", "", "scope")
	root := fs.String("registry", "", "registry")
	err := fs.Parse(args)
	return *scopePath, *root, err
}

func openAdmin(envName string) (e2erunscope.DatabaseAdmin, error) {
	dsn := os.Getenv(envName)
	if dsn == "" {
		return nil, errors.New("database DSN environment variable is empty")
	}
	return e2erunscope.NewPostgresAdmin(context.Background(), dsn)
}

func processAlive(pid int, repository string) bool {
	if pid <= 0 || unix.Kill(pid, 0) != nil {
		return false
	}
	cwd, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid))
	return err != nil || filepath.Clean(cwd) == filepath.Clean(repository)
}

func readScope(path string) (e2erunscope.Scope, error) {
	if !filepath.IsAbs(path) {
		return e2erunscope.Scope{}, errors.New("scope path must be absolute")
	}
	file, err := os.Open(filepath.Clean(path)) // #nosec G304 -- absolute path is supplied by the trusted runner.
	if err != nil {
		return e2erunscope.Scope{}, err
	}
	defer file.Close()
	var scope e2erunscope.Scope
	if err := json.NewDecoder(io.LimitReader(file, 1<<20)).Decode(&scope); err != nil {
		return scope, err
	}
	return scope, e2erunscope.Validate(scope)
}

func writeJSON(w io.Writer, value any) error { return json.NewEncoder(w).Encode(value) }
