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

type dependencies struct {
	now          func() time.Time
	pid          func() int
	random       io.Reader
	lookupEnv    func(string) string
	openAdmin    func(string) (e2erunscope.DatabaseAdmin, error)
	processAlive func(int, string) bool
}

func defaultDependencies() dependencies {
	return dependencies{
		now: time.Now, pid: os.Getpid, lookupEnv: os.Getenv, openAdmin: openAdmin, processAlive: processAlive,
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	return runWithDependencies(args, stdout, stderr, defaultDependencies())
}

func runWithDependencies(args []string, stdout, stderr io.Writer, deps dependencies) error {
	if len(args) == 0 {
		return errors.New("command is required")
	}
	handlers := map[string]func([]string, io.Writer, io.Writer) error{
		"allocate": func(a []string, o, e io.Writer) error { return runAllocate(a, o, e, deps) },
		"release":  runRelease, "prepare-registry": runPrepareRegistry, "validate": runValidate,
		"database-url": func(a []string, o, e io.Writer) error { return runDatabaseURL(a, o, e, deps.lookupEnv) },
		"create-database": func(a []string, _ io.Writer, e io.Writer) error {
			return runDatabase(a, e, deps.openAdmin, e2erunscope.CreateDatabase)
		},
		"drop-database": func(a []string, _ io.Writer, e io.Writer) error {
			return runDatabase(a, e, deps.openAdmin, e2erunscope.DropDatabase)
		},
		"mark-infrastructure-owned": func(a []string, o, e io.Writer) error {
			return runMarkInfrastructureOwned(a, o, e, deps.now)
		},
		"confirm-infrastructure-stopped": runConfirmStopped,
		"reap":                           func(a []string, o, e io.Writer) error { return runReap(a, o, e, deps) },
	}
	handler, ok := handlers[args[0]]
	if !ok {
		return fmt.Errorf("unknown command %q", args[0])
	}
	return handler(args[1:], stdout, stderr)
}

func runValidate(args []string, _ io.Writer, stderr io.Writer) error {
	fs := newFlags("validate", stderr)
	scopePath := fs.String("scope", "", "scope")
	if err := fs.Parse(args); err != nil {
		return err
	}
	_, err := readScope(*scopePath)
	return err
}

func runPrepareRegistry(args []string, _ io.Writer, stderr io.Writer) error {
	fs := newFlags("prepare-registry", stderr)
	root := fs.String("registry", "", "registry")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return (e2erunscope.Registry{Root: *root}).Prepare()
}

func runDatabaseURL(args []string, stdout, stderr io.Writer, lookupEnv func(string) string) error {
	fs := newFlags("database-url", stderr)
	envName := fs.String("base-dsn-env", "", "DSN env")
	database := fs.String("database-name", "", "database name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	value, err := e2erunscope.DatabaseURL(lookupEnv(*envName), *database)
	if err != nil {
		return err
	}
	_, err = io.WriteString(stdout, value+"\n")
	return err
}

func runMarkInfrastructureOwned(args []string, _ io.Writer, stderr io.Writer, now func() time.Time) error {
	scopePath, root, err := parseScopeRegistryArgs("mark-infrastructure-owned", args, stderr)
	if err != nil {
		return err
	}
	scope, err := readScope(scopePath)
	if err != nil {
		return err
	}
	return (e2erunscope.Registry{Root: root}).MarkInfrastructureOwned(scope.RunID, now().UTC())
}

func runAllocate(args []string, stdout, stderr io.Writer, deps dependencies) error {
	fs := newFlags("allocate", stderr)
	repository := fs.String("repository", "", "repository")
	root := fs.String("registry", "", "registry")
	ownerPID := fs.Int("owner-pid", 0, "runner process ID")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !filepath.IsAbs(*repository) || !filepath.IsAbs(*root) {
		return errors.New("repository and registry must be absolute")
	}
	pid := deps.pid()
	if *ownerPID < 0 {
		return errors.New("owner PID must be positive")
	}
	if *ownerPID > 0 {
		pid = *ownerPID
	}
	scope, err := e2erunscope.NewScope(*repository, pid, deps.now().UTC(), deps.random)
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

func runDatabase(
	args []string,
	stderr io.Writer,
	open func(string) (e2erunscope.DatabaseAdmin, error),
	operation func(context.Context, e2erunscope.DatabaseAdmin, string) error,
) error {
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
	admin, err := open(*envName)
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

func runReap(args []string, stdout, stderr io.Writer, deps dependencies) error {
	fs := newFlags("reap", stderr)
	root := fs.String("registry", "", "registry")
	envName := fs.String("base-dsn-env", "", "DSN env")
	if err := fs.Parse(args); err != nil {
		return err
	}
	admin, err := deps.openAdmin(*envName)
	if err != nil {
		return err
	}
	ctx := context.Background()
	removed, opErr := e2erunscope.ReapDatabases(
		ctx, e2erunscope.Registry{Root: *root}, admin, deps.now().UTC(), deps.processAlive,
	)
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
