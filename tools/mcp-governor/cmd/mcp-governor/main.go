package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/byteBuilderX/stratum/tools/mcp-governor/internal/clientconfig"
	"github.com/byteBuilderX/stratum/tools/mcp-governor/internal/config"
	"github.com/byteBuilderX/stratum/tools/mcp-governor/internal/identity"
	"github.com/byteBuilderX/stratum/tools/mcp-governor/internal/jsonstrict"
	"github.com/byteBuilderX/stratum/tools/mcp-governor/internal/observe"
	"github.com/byteBuilderX/stratum/tools/mcp-governor/internal/process"
	"github.com/byteBuilderX/stratum/tools/mcp-governor/internal/proxy"
	"github.com/byteBuilderX/stratum/tools/mcp-governor/internal/report"
)

type scanner interface {
	ListPIDs() ([]int, error)
	ReadIdentity(int) (process.Identity, error)
	ReadProcess(int) (process.Process, []string, error)
}

var (
	currentTime    = time.Now
	userHomeDir    = os.UserHomeDir
	newScanner     = func(root string) scanner { return process.NewProcFS(root) }
	createTempFile = os.CreateTemp
	syncFile       = func(file *os.File) error { return file.Sync() }
	renameFile     = os.Rename
	syncDirectory  = syncDirectoryOS
	proxyStdin     = io.Reader(os.Stdin)
)

type options struct {
	configPath string
	procRoot   string
	outputPath string
	outputSet  bool
}

type proxyOptions struct {
	configPath string
	client     string
	service    string
	session    string
	repository string
	command    string
	args       []string
}

type reportOptions struct {
	configPath              string
	outputPath              string
	start                   time.Time
	end                     time.Time
	outputSet, allowPartial bool
	calendarWindow          bool
}

type renderOptions struct {
	configPath, client, governorPath, outputPath string
}

const observationWriterCloseBudget = 2 * time.Second

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	os.Exit(runContext(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	return runContext(context.Background(), args, stdout, stderr)
}

func runContext(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "expected command")
		printUsage(stderr)
		return 2
	}
	switch args[0] {
	case "snapshot":
		opts, err := parseArgs(args)
		if err != nil {
			fmt.Fprintln(stderr, err)
			fmt.Fprintln(stderr, "usage: mcp-governor snapshot --config PATH [--proc-root /proc] [--output PATH|-]")
			return 2
		}
		if err := snapshot(opts, stdout); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "proxy":
		opts, err := parseProxyArgs(args)
		if err != nil {
			fmt.Fprintln(stderr, err)
			fmt.Fprintln(stderr, proxyUsage)
			return 2
		}
		if err := runProxyContext(ctx, opts, stdout, stderr); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "report":
		opts, err := parseReportArgs(args)
		if err != nil {
			fmt.Fprintln(stderr, err)
			fmt.Fprintln(stderr, "usage: mcp-governor report --config PATH --from RFC3339 --to RFC3339 [--output PATH|-] [--allow-partial]")
			return 2
		}
		if err := runReport(opts, stdout); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "report-latest":
		configPath, err := parseReportLatestArgs(args)
		if err != nil {
			fmt.Fprintln(stderr, err)
			fmt.Fprintln(stderr, "usage: mcp-governor report-latest --config PATH")
			return 2
		}
		if err := runReportLatest(configPath, stdout); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "render-config":
		opts, err := parseRenderArgs(args)
		if err != nil {
			fmt.Fprintln(stderr, err)
			fmt.Fprintln(stderr, renderUsage)
			return 2
		}
		if err := runRenderConfig(opts, stdout); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		printUsage(stderr)
		return 2
	}
}

const proxyUsage = "usage: mcp-governor proxy --config PATH --client CLIENT --service SERVICE " +
	"[--session PID:START_TICKS] [--repository PATH] -- COMMAND [ARG...]"

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: mcp-governor snapshot|proxy|report|report-latest|render-config ...")
}

const renderUsage = "usage: mcp-governor render-config --config PATH --client codex|claude|vscode|lingma " +
	"--governor PATH --output PATH|-"

func parseRenderArgs(args []string) (renderOptions, error) {
	var opts renderOptions
	if len(args) == 0 || args[0] != "render-config" {
		return opts, errors.New("expected render-config command")
	}
	seen := make(map[string]bool)
	for i := 1; i < len(args); i++ {
		name := args[i]
		if name != "--config" && name != "--client" && name != "--governor" && name != "--output" {
			return opts, fmt.Errorf("unexpected argument")
		}
		if seen[name] {
			return opts, fmt.Errorf("duplicate flag %s", name)
		}
		seen[name] = true
		if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
			return opts, fmt.Errorf("flag %s requires a value", name)
		}
		i++
		switch name {
		case "--config":
			opts.configPath = args[i]
		case "--client":
			opts.client = args[i]
		case "--governor":
			opts.governorPath = args[i]
		case "--output":
			opts.outputPath = args[i]
		}
	}
	if opts.configPath == "" || opts.client == "" || opts.governorPath == "" || opts.outputPath == "" {
		return opts, errors.New("--config, --client, --governor, and --output are required")
	}
	if !knownClient(opts.client) {
		return opts, errors.New("--client must be codex, claude, vscode, or lingma")
	}
	return opts, nil
}

func parseReportArgs(args []string) (reportOptions, error) {
	var opts reportOptions
	if len(args) == 0 || args[0] != "report" {
		return opts, errors.New("expected report command")
	}
	seen := make(map[string]bool)
	for i := 1; i < len(args); i++ {
		name := args[i]
		if name == "--allow-partial" {
			if seen[name] {
				return opts, fmt.Errorf("duplicate flag %s", name)
			}
			seen[name], opts.allowPartial = true, true
			continue
		}
		if name != "--config" && name != "--from" && name != "--to" && name != "--output" {
			return opts, fmt.Errorf("unexpected argument %q", name)
		}
		if seen[name] {
			return opts, fmt.Errorf("duplicate flag %s", name)
		}
		seen[name] = true
		if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
			return opts, fmt.Errorf("flag %s requires a value", name)
		}
		i++
		switch name {
		case "--config":
			opts.configPath = args[i]
		case "--output":
			opts.outputPath, opts.outputSet = args[i], true
		case "--from":
			value, err := time.Parse(time.RFC3339, args[i])
			if err != nil {
				return opts, fmt.Errorf("parse --from: %w", err)
			}
			opts.start = value
		case "--to":
			value, err := time.Parse(time.RFC3339, args[i])
			if err != nil {
				return opts, fmt.Errorf("parse --to: %w", err)
			}
			opts.end = value
		}
	}
	if opts.configPath == "" || opts.start.IsZero() || opts.end.IsZero() {
		return opts, errors.New("--config, --from, and --to are required")
	}
	if !opts.start.Before(opts.end) {
		return opts, errors.New("--from must be before --to")
	}
	if opts.outputSet && opts.outputPath == "" {
		return opts, errors.New("--output must not be empty")
	}
	return opts, nil
}

func parseReportLatestArgs(args []string) (string, error) {
	if len(args) != 3 || args[0] != "report-latest" || args[1] != "--config" ||
		args[2] == "" || strings.HasPrefix(args[2], "--") {
		return "", errors.New("--config is required")
	}
	return args[2], nil
}

func runReportLatest(configPath string, stdout io.Writer) error {
	now := currentTime()
	location := now.Location()
	localNow := now.In(location)
	end := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, location)
	start := end.AddDate(0, 0, -7)
	return runReport(reportOptions{configPath: configPath, start: start, end: end, calendarWindow: true}, stdout)
}

func parseArgs(args []string) (options, error) {
	opts := options{procRoot: "/proc"}
	if len(args) == 0 || args[0] != "snapshot" {
		return opts, errors.New("expected snapshot command")
	}
	seen := make(map[string]bool)
	for i := 1; i < len(args); i++ {
		name := args[i]
		if name != "--config" && name != "--proc-root" && name != "--output" {
			return opts, fmt.Errorf("unexpected argument %q", name)
		}
		if seen[name] {
			return opts, fmt.Errorf("duplicate flag %s", name)
		}
		seen[name] = true
		if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
			return opts, fmt.Errorf("flag %s requires a value", name)
		}
		i++
		switch name {
		case "--config":
			opts.configPath = args[i]
		case "--proc-root":
			opts.procRoot = args[i]
		case "--output":
			opts.outputPath, opts.outputSet = args[i], true
		}
	}
	if opts.configPath == "" {
		return opts, errors.New("--config is required")
	}
	if opts.procRoot == "" || (opts.outputSet && opts.outputPath == "") {
		return opts, errors.New("path flag values must not be empty")
	}
	return opts, nil
}

func parseProxyArgs(args []string) (proxyOptions, error) {
	var opts proxyOptions
	separator := -1
	for i := 1; i < len(args); i++ {
		if args[i] == "--" {
			separator = i
			break
		}
	}
	if separator < 0 {
		return opts, errors.New("proxy command separator -- is required")
	}
	seen := make(map[string]bool)
	for i := 1; i < separator; i++ {
		name := args[i]
		if name != "--config" && name != "--client" && name != "--service" && name != "--session" && name != "--repository" {
			return opts, fmt.Errorf("unexpected argument %q", name)
		}
		if seen[name] {
			return opts, fmt.Errorf("duplicate flag %s", name)
		}
		seen[name] = true
		if i+1 >= separator || strings.HasPrefix(args[i+1], "--") {
			return opts, fmt.Errorf("flag %s requires a value", name)
		}
		i++
		switch name {
		case "--config":
			opts.configPath = args[i]
		case "--client":
			opts.client = args[i]
		case "--service":
			opts.service = args[i]
		case "--session":
			opts.session = args[i]
		case "--repository":
			opts.repository = args[i]
		}
	}
	if opts.configPath == "" || opts.client == "" || opts.service == "" {
		return opts, errors.New("--config, --client, and --service are required")
	}
	if seen["--session"] {
		if err := validateSessionIdentity(opts.session); err != nil {
			return opts, err
		}
	}
	if seen["--repository"] && strings.TrimSpace(opts.repository) == "" {
		return opts, errors.New("repository must not be empty")
	}
	if separator+1 >= len(args) || strings.TrimSpace(args[separator+1]) == "" {
		return opts, errors.New("proxy command must not be empty")
	}
	opts.command = args[separator+1]
	opts.args = append([]string(nil), args[separator+2:]...)
	return opts, nil
}

func validateSessionIdentity(value string) error {
	pid, ticks, ok := strings.Cut(value, ":")
	parsedPID, pidErr := strconv.Atoi(pid)
	parsedTicks, ticksErr := strconv.ParseUint(ticks, 10, 64)
	if !ok || pidErr != nil || ticksErr != nil || parsedPID <= 0 || parsedTicks == 0 {
		return errors.New("session must have positive PID:START_TICKS identity")
	}
	return nil
}

func runProxy(opts proxyOptions, stdout, stderr io.Writer) (resultErr error) {
	return runProxyContext(context.Background(), opts, stdout, stderr)
}

func runProxyContext(ctx context.Context, opts proxyOptions, stdout, stderr io.Writer) (resultErr error) {
	resolvePath := newPathResolver()
	configPath, err := resolvePath(opts.configPath)
	if err != nil {
		return err
	}
	file, err := os.Open(configPath)
	if err != nil {
		return fmt.Errorf("open config: %w", err)
	}
	cfg, decodeErr := config.Decode(file)
	closeErr := file.Close()
	if decodeErr != nil {
		return decodeErr
	}
	if closeErr != nil {
		return fmt.Errorf("close config: %w", closeErr)
	}
	if cfg.Version != 2 {
		return errors.New("proxy requires config version 2")
	}
	if !knownClient(opts.client) {
		return fmt.Errorf("unknown client %q", opts.client)
	}
	service, ok := findService(cfg.Services, opts.service)
	if !ok {
		return fmt.Errorf("unknown service %q", opts.service)
	}
	if service.Transport != config.TransportStdio {
		return fmt.Errorf("service %q does not use stdio transport", opts.service)
	}
	if !serviceEnabled(service, config.Client(opts.client)) {
		return fmt.Errorf("service %q is not enabled for client %q", opts.service, opts.client)
	}
	if service.Scope == config.ScopeRepository && opts.repository == "" {
		return fmt.Errorf("service %q requires repository identity", opts.service)
	}
	if opts.command != service.Command || !slices.Equal(opts.args, service.Args) {
		return fmt.Errorf("service %q catalog command mismatch", opts.service)
	}
	classifier, err := process.NewClassifier([]process.Rule{{
		Name: service.Name, AllArgsContain: service.AllArgsContain,
	}})
	if err != nil {
		return fmt.Errorf("build service classifier: %w", err)
	}
	commandArgs := append([]string{opts.command}, opts.args...)
	if classifier.Classify(process.Process{Command: opts.command, Args: commandArgs}) != service.Name {
		return fmt.Errorf("command does not match service %q catalog classification", opts.service)
	}

	sessionIdentity := opts.session
	if sessionIdentity == "" {
		parent, err := newScanner("/proc").ReadIdentity(os.Getppid())
		if err != nil {
			return fmt.Errorf("resolve parent session identity: %w", err)
		}
		sessionIdentity = fmt.Sprintf("%d:%d", parent.PID, parent.StartTicks)
	}
	hasher, err := loadProxyHasher(resolvePath, cfg.Observation.SaltPath)
	if err != nil {
		return err
	}
	sessionHash := hasher.Hash("session", sessionIdentity)
	repositoryHash := ""
	if opts.repository != "" {
		canonical, err := canonicalRepository(opts.repository)
		if err != nil {
			return errors.New("canonicalize repository identity: repository path unavailable")
		}
		repositoryHash = hasher.Hash("repository", canonical)
	}
	eventsDir, err := resolvePath(cfg.Observation.EventsDir)
	if err != nil {
		return err
	}
	writer, err := observe.NewWriterWithOptions(eventsDir, opts.client, sessionHash,
		observe.WriterOptions{MaxSegmentBytes: cfg.Observation.MaxEventSegmentBytes, RotateDaily: true})
	if err != nil {
		return fmt.Errorf("create observation writer: %w", err)
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), observationWriterCloseBudget)
		resultErr = errors.Join(resultErr, writer.CloseContext(closeCtx))
		cancel()
	}()
	tracker, err := observe.NewTracker(currentTime, observe.Metadata{
		Client: opts.client, Service: service.Name, SessionHash: sessionHash, RepositoryHash: repositoryHash,
	})
	if err != nil {
		return fmt.Errorf("create observation tracker: %w", err)
	}
	dir := service.Cwd
	if dir != "" {
		dir, err = resolvePath(dir)
		if err != nil {
			return err
		}
	}
	return proxy.Run(ctx, proxy.Options{
		Command: opts.command, Args: opts.args, Env: os.Environ(), Dir: dir,
		Stdin: proxyStdin, Stdout: stdout, Stderr: stderr, Tracker: tracker, Events: writer,
	})
}

func knownClient(client string) bool {
	switch config.Client(client) {
	case config.ClientCodex, config.ClientClaude, config.ClientVSCode, config.ClientLingma:
		return true
	default:
		return false
	}
}

func findService(services []config.ServiceRule, name string) (config.ServiceRule, bool) {
	for _, service := range services {
		if service.Name == name {
			return service, true
		}
	}
	return config.ServiceRule{}, false
}

func serviceEnabled(service config.ServiceRule, client config.Client) bool {
	for _, allowed := range service.Clients {
		if allowed == client {
			return true
		}
	}
	return false
}

func loadProxyHasher(resolvePath func(string) (string, error), path string) (*identity.Hasher, error) {
	resolved, err := resolvePath(path)
	if err != nil {
		return nil, err
	}
	hasher, err := identity.LoadSalt(resolved)
	if err != nil {
		return nil, err
	}
	return hasher, nil
}

func canonicalRepository(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	return filepath.Clean(canonical), nil
}

func snapshot(opts options, stdout io.Writer) error {
	resolvePath := newPathResolver()
	var err error
	opts.configPath, err = resolvePath(opts.configPath)
	if err != nil {
		return err
	}
	file, err := os.Open(opts.configPath)
	if err != nil {
		return fmt.Errorf("open config: %w", err)
	}
	cfg, decodeErr := config.Decode(file)
	closeErr := file.Close()
	if decodeErr != nil {
		return decodeErr
	}
	if closeErr != nil {
		return fmt.Errorf("close config: %w", closeErr)
	}
	cfg.RegistryPath, err = resolvePath(cfg.RegistryPath)
	if err != nil {
		return err
	}
	if opts.outputSet {
		opts.outputPath, err = resolvePath(opts.outputPath)
		if err != nil {
			return err
		}
	} else {
		opts.outputPath, err = resolvePath(cfg.OutputPath)
		if err != nil {
			return err
		}
	}

	rules := make([]process.Rule, len(cfg.Services))
	for i, service := range cfg.Services {
		rules[i] = process.Rule{Name: service.Name, AllArgsContain: service.AllArgsContain}
	}
	classifier, err := process.NewClassifier(rules)
	if err != nil {
		return fmt.Errorf("build classifier: %w", err)
	}
	registrations, err := readRegistry(cfg.RegistryPath)
	if err != nil {
		return err
	}
	serviceNames := make(map[string]bool, len(cfg.Services))
	for _, service := range cfg.Services {
		serviceNames[service.Name] = true
	}
	for i, registration := range registrations {
		if !serviceNames[registration.Service] {
			return fmt.Errorf("registry registrations[%d] service %q is not configured", i, registration.Service)
		}
	}

	proc := newScanner(opts.procRoot)
	pids, err := proc.ListPIDs()
	if err != nil {
		return err
	}
	var processes []process.Process
	live := make(map[process.Identity]bool)
	var warnings []string
	for _, pid := range pids {
		item, itemWarnings, err := proc.ReadProcess(pid)
		if err != nil {
			var gone *process.ProcessGoneError
			if errors.As(err, &gone) {
				continue
			}
			warnings = append(warnings, fmt.Sprintf("process %d unavailable: %v", pid, err))
			continue
		}
		live[item.Identity] = true
		item.Service = classifier.Classify(item)
		for _, warning := range itemWarnings {
			if item.Service != "" {
				warning = fmt.Sprintf("service %s: %s", item.Service, warning)
			}
			warnings = append(warnings, warning)
		}
		processes = append(processes, item)
	}

	checkedClients := make(map[process.Identity]bool, len(registrations))
	for _, registration := range registrations {
		client := registration.Client
		if checkedClients[client] {
			continue
		}
		checkedClients[client] = true
		identity, err := proc.ReadIdentity(client.PID)
		if err == nil {
			live[identity] = true
			continue
		}
		var gone *process.ProcessGoneError
		if errors.As(err, &gone) || errors.Is(err, fs.ErrNotExist) {
			continue
		}
		live[client] = true
		warnings = append(warnings, fmt.Sprintf(
			"registered client process %d identity indeterminate (%s); treating expected identity as live",
			client.PID, identityErrorCategory(err),
		))
	}
	result := process.BuildSnapshot(currentTime(), processes, registrations, live, warnings)
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("encode snapshot: %w", err)
	}
	data = append(data, '\n')
	if opts.outputPath == "-" {
		if _, err := stdout.Write(data); err != nil {
			return fmt.Errorf("write snapshot: %w", err)
		}
		return nil
	}
	if cfg.Version == 2 {
		return publishSnapshotData(opts.outputPath, data, result, cfg.Observation.RawRetentionDays)
	}
	return writeAtomic(opts.outputPath, data)
}

func identityErrorCategory(err error) string {
	if errors.Is(err, fs.ErrPermission) {
		return "permission"
	}
	if strings.Contains(err.Error(), "parse stat") {
		return "malformed"
	}
	return "unavailable"
}

func readRegistry(path string) ([]process.Registration, error) {
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open registry: %w", err)
	}
	registry, decodeErr := process.DecodeRegistry(file)
	closeErr := file.Close()
	if decodeErr != nil {
		return nil, decodeErr
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close registry: %w", closeErr)
	}
	return registry.Registrations, nil
}

func newPathResolver() func(string) (string, error) {
	var home string
	var resolved bool
	return func(path string) (string, error) {
		if !strings.HasPrefix(path, "%h/") {
			return path, nil
		}
		if !resolved {
			var err error
			home, err = userHomeDir()
			if err != nil {
				return "", fmt.Errorf("resolve home directory: %w", err)
			}
			resolved = true
		}
		return filepath.Join(home, strings.TrimPrefix(path, "%h/")), nil
	}
}

func runReport(opts reportOptions, stdout io.Writer) error {
	if !opts.allowPartial && !opts.calendarWindow && opts.end.Sub(opts.start) < 7*24*time.Hour {
		return fmt.Errorf("report window must cover at least seven complete 24-hour days")
	}
	resolvePath := newPathResolver()
	configPath, err := resolvePath(opts.configPath)
	if err != nil {
		return err
	}
	file, err := os.Open(configPath)
	if err != nil {
		return fmt.Errorf("open config: %w", err)
	}
	cfg, decodeErr := config.Decode(file)
	closeErr := file.Close()
	if decodeErr != nil {
		return decodeErr
	}
	if closeErr != nil {
		return fmt.Errorf("close config: %w", closeErr)
	}
	if cfg.Version != 2 {
		return fmt.Errorf("report requires config version 2")
	}
	eventsDir, err := resolvePath(cfg.Observation.EventsDir)
	if err != nil {
		return err
	}
	snapshotPath, err := resolvePath(cfg.OutputPath)
	if err != nil {
		return err
	}
	accumulator, err := report.NewAccumulatorWithBudget(opts.start, opts.end, report.Budget{
		MaxEventBytes:                  cfg.Observation.ReportMaxEventBytes,
		MaxRecords:                     cfg.Observation.ReportMaxRecords,
		MaxToolCardinality:             cfg.Observation.ReportMaxToolCardinality,
		MaxSessionCardinality:          cfg.Observation.ReportMaxSessionCardinality,
		MaxDayCardinality:              cfg.Observation.ReportMaxDayCardinality,
		MaxServiceCardinality:          cfg.Observation.ReportMaxServiceCardinality,
		MaxSnapshotIdentityCardinality: cfg.Observation.ReportMaxSnapshotIdentityCardinality,
		MaxDistributionCardinality:     cfg.Observation.ReportMaxDistributionValues,
		MaxWorkUnits:                   cfg.Observation.ReportMaxWorkUnits,
	})
	if err != nil {
		return err
	}
	files, err := readEventFiles(eventsDir, accumulator)
	if err != nil {
		return err
	}
	maxHistorySamples, err := snapshotHistoryLimit(cfg.Observation.RawRetentionDays)
	if err != nil {
		return err
	}
	snapshots, err := readSnapshots(snapshotPath, maxHistorySamples)
	if err != nil {
		return err
	}
	for i, snapshot := range snapshots {
		if err := accumulator.AddSnapshot(snapshot); err != nil {
			return fmt.Errorf("snapshot %d: %w", i, err)
		}
	}
	aggregate := accumulator.Report()
	data, err := json.MarshalIndent(aggregate, "", "  ")
	if err != nil {
		return fmt.Errorf("encode report: %w", err)
	}
	data = append(data, '\n')
	if opts.outputSet && opts.outputPath == "-" {
		if _, err := stdout.Write(data); err != nil {
			return fmt.Errorf("write report: %w", err)
		}
		if !aggregate.Completeness.Complete {
			return fmt.Errorf("report incomplete (%s)",
				strings.Join(aggregate.Completeness.OverflowReasons, ","))
		}
		return nil
	} else {
		output := opts.outputPath
		if !opts.outputSet {
			reportsDir, err := resolvePath(cfg.Observation.ReportsDir)
			if err != nil {
				return err
			}
			output = filepath.Join(reportsDir, "report-"+safeReportTime(opts.start)+"-"+safeReportTime(opts.end)+".json")
		} else if output, err = resolvePath(output); err != nil {
			return err
		}
		if !aggregate.Completeness.Complete {
			if _, statErr := os.Stat(output); statErr == nil {
				return fmt.Errorf("report incomplete (%s); previous report preserved",
					strings.Join(aggregate.Completeness.OverflowReasons, ","))
			} else if !errors.Is(statErr, os.ErrNotExist) {
				return fmt.Errorf("inspect prior report: %w", statErr)
			}
			if err := writeAtomic(output, data); err != nil {
				return err
			}
			return fmt.Errorf("report incomplete (%s)",
				strings.Join(aggregate.Completeness.OverflowReasons, ","))
		}
		if err := writeAtomic(output, data); err != nil {
			return err
		}
	}
	boundary := opts.end.AddDate(0, 0, -cfg.Observation.RawRetentionDays)
	if err := pruneEventFiles(files, boundary); err != nil {
		return err
	}
	if err := pruneObservationStatuses(eventsDir, boundary); err != nil {
		return err
	}
	return nil
}

func safeReportTime(value time.Time) string { return value.UTC().Format("20060102T150405Z") }

type eventFile struct {
	path   string
	device uint64
	inode  uint64
	size   int64
	count  int
	minAt  time.Time
	maxAt  time.Time
}

func readEventFiles(root string, accumulator *report.Accumulator) ([]eventFile, error) {
	var files []eventFile
	statusFiles := 0
	const maxStatusFiles = 10_000
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, os.ErrNotExist) && path == root {
				return nil
			}
			return walkErr
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || filepath.Ext(path) != ".jsonl" {
			if info.Mode().Perm() != 0o600 || !strings.HasSuffix(entry.Name(), ".status.json") {
				return nil
			}
			if statusFiles >= maxStatusFiles {
				accumulator.MarkDegraded("observation_status_cardinality", 1)
				return nil
			}
			statusFiles++
			status, err := decodeObservationStatus(path)
			if err != nil {
				return err
			}
			if err := accumulator.MergeObservationStatus(status); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			return nil
		}
		// Once a hard report input/work budget is exhausted, do not spend more
		// CPU or memory decoding additional files. Files not returned here are
		// intentionally not eligible for this run's retention prune.
		if accumulator.StopReading() {
			return nil
		}
		file, err := decodeEventJSONL(path, accumulator)
		if err != nil {
			return err
		}
		files = append(files, file)
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read events: %w", err)
	}
	return files, nil
}

func decodeObservationStatus(path string) (observe.DegradedStatus, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return observe.DegradedStatus{}, fmt.Errorf("open observation status: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	var stat syscall.Stat_t
	if err := syscall.Fstat(fd, &stat); err != nil {
		return observe.DegradedStatus{}, fmt.Errorf("inspect observation status: %w", err)
	}
	if stat.Mode&syscall.S_IFMT != syscall.S_IFREG || stat.Mode&0o777 != 0o600 {
		return observe.DegradedStatus{}, fmt.Errorf("observation status must be a private regular file")
	}
	if err := syscall.Flock(fd, syscall.LOCK_SH); err != nil {
		return observe.DegradedStatus{}, fmt.Errorf("lock observation status: %w", err)
	}
	defer syscall.Flock(fd, syscall.LOCK_UN)
	const maxStatusBytes = 64 << 10
	data, err := io.ReadAll(io.LimitReader(file, maxStatusBytes+1))
	if err != nil {
		return observe.DegradedStatus{}, fmt.Errorf("read observation status: %w", err)
	}
	if len(data) > maxStatusBytes {
		return observe.DegradedStatus{}, fmt.Errorf("observation status exceeds %d bytes", maxStatusBytes)
	}
	var status observe.DegradedStatus
	if err := decodeStrictJSON(data, &status); err != nil {
		return observe.DegradedStatus{}, fmt.Errorf("decode observation status: %w", err)
	}
	return status, nil
}

func decodeEventJSONL(path string, accumulator *report.Accumulator) (eventFile, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return eventFile{}, fmt.Errorf("open event file: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	if err := syscall.Flock(fd, syscall.LOCK_SH); err != nil {
		return eventFile{}, fmt.Errorf("lock event file for report: %w", err)
	}
	defer func() { _ = syscall.Flock(fd, syscall.LOCK_UN) }()
	var stat syscall.Stat_t
	if err := syscall.Fstat(fd, &stat); err != nil {
		_ = syscall.Flock(fd, syscall.LOCK_UN)
		return eventFile{}, err
	}
	if stat.Mode&syscall.S_IFMT != syscall.S_IFREG || stat.Mode&0o777 != 0o600 {
		_ = syscall.Flock(fd, syscall.LOCK_UN)
		return eventFile{}, fmt.Errorf("event file must be a private regular file")
	}
	result := eventFile{path: path, device: uint64(stat.Dev), inode: stat.Ino, size: stat.Size}
	// Decode directly from the locked descriptor with a bounded record buffer.
	// This avoids a second unbounded allocation proportional to a long-lived
	// session file. The lock is released after scanning; prune revalidates the
	// inode/size before unlinking.
	scanner := bufio.NewScanner(file)
	buffer := make([]byte, 64*1024)
	scanner.Buffer(buffer, 4*1024*1024)
	for line := 1; scanner.Scan(); line++ {
		data := bytes.TrimSpace(scanner.Bytes())
		if len(data) == 0 {
			return eventFile{}, fmt.Errorf("%s:%d: empty JSONL record", path, line)
		}
		var event observe.Event
		if err := decodeStrictJSON(data, &event); err != nil {
			return eventFile{}, fmt.Errorf("%s:%d: %w", path, line, err)
		}
		if err := accumulator.AddEventBytes(event, int64(len(data))); err != nil {
			_ = syscall.Flock(fd, syscall.LOCK_UN)
			return eventFile{}, fmt.Errorf("%s:%d: validate event: %w", path, line, err)
		}
		result.count++
		if result.minAt.IsZero() || event.At.Before(result.minAt) {
			result.minAt = event.At
		}
		if result.maxAt.IsZero() || event.At.After(result.maxAt) {
			result.maxAt = event.At
		}
		if accumulator.StopReading() {
			break
		}
	}
	_ = syscall.Flock(fd, syscall.LOCK_UN)
	if err := scanner.Err(); err != nil {
		return eventFile{}, fmt.Errorf("scan event file: %w", err)
	}
	return result, nil
}

func decodeStrictJSON(data []byte, target any) error {
	if err := jsonstrict.ValidateNoDuplicateKeys(data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return fmt.Errorf("trailing JSON value")
	}
	return nil
}

func readSnapshots(path string, maxHistorySamples int) ([]process.Snapshot, error) {
	historyPath := path + ".history.jsonl"
	if _, err := os.Stat(historyPath); err == nil {
		return readSnapshotHistoryLocked(historyPath, maxHistorySamples)
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect snapshot history: %w", err)
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read snapshot: %w", err)
	}
	var snapshot process.Snapshot
	if err := decodeStrictJSON(data, &snapshot); err != nil {
		return nil, fmt.Errorf("decode snapshot: %w", err)
	}
	if snapshot.Version != 1 || snapshot.Mode != "observe" || snapshot.CapturedAt.IsZero() {
		return nil, fmt.Errorf("validate snapshot: version 1, observe mode, and captured_at are required")
	}
	return []process.Snapshot{snapshot}, nil
}

func readSnapshotHistoryLocked(historyPath string, maxHistorySamples int) ([]process.Snapshot, error) {
	lockPath := historyPath + ".lock"
	lockFD, err := syscall.Open(lockPath, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if errors.Is(err, os.ErrNotExist) {
		return decodeSnapshotHistoryLimited(historyPath, maxHistorySamples)
	}
	if err != nil {
		return nil, fmt.Errorf("open snapshot history lock: %w", err)
	}
	lock := os.NewFile(uintptr(lockFD), lockPath)
	defer lock.Close()
	if err := syscall.Flock(lockFD, syscall.LOCK_SH); err != nil {
		return nil, fmt.Errorf("lock snapshot history: %w", err)
	}
	defer func() { _ = syscall.Flock(lockFD, syscall.LOCK_UN) }()
	return decodeSnapshotHistoryLimited(historyPath, maxHistorySamples)
}

func decodeSnapshotHistory(path string) ([]process.Snapshot, error) {
	return decodeSnapshotHistoryLimited(path, int(^uint(0)>>1))
}

func decodeSnapshotHistoryLimited(path string, limit int) ([]process.Snapshot, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("snapshot history limit must be positive")
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("open snapshot history: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	defer file.Close()
	var stat syscall.Stat_t
	if err := syscall.Fstat(fd, &stat); err != nil {
		return nil, err
	}
	if stat.Mode&syscall.S_IFMT != syscall.S_IFREG || stat.Mode&0o777 != 0o600 {
		return nil, fmt.Errorf("snapshot history must be a private regular file")
	}
	snapshots := make([]process.Snapshot, 0, min(limit, 1024))
	count := 0
	var previous time.Time
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for line := 1; scanner.Scan(); line++ {
		var snapshot process.Snapshot
		if err := decodeStrictJSON(scanner.Bytes(), &snapshot); err != nil {
			return nil, fmt.Errorf("snapshot history line %d: %w", line, err)
		}
		if !previous.IsZero() && snapshot.CapturedAt.Before(previous) {
			return nil, fmt.Errorf("snapshot history is not deterministically sorted")
		}
		previous = snapshot.CapturedAt
		if count < limit {
			snapshots = append(snapshots, snapshot)
		} else {
			snapshots[count%limit] = snapshot
		}
		count++
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan snapshot history: %w", err)
	}
	if count <= limit {
		return snapshots, nil
	}
	start := count % limit
	ordered := make([]process.Snapshot, 0, limit)
	ordered = append(ordered, snapshots[start:]...)
	ordered = append(ordered, snapshots[:start]...)
	return ordered, nil
}

func publishSnapshotData(outputPath string, _ []byte, snapshot process.Snapshot, retentionDays int) error {
	historyPath := outputPath + ".history.jsonl"
	lockFD, err := syscall.Open(historyPath+".lock", syscall.O_CREAT|syscall.O_RDWR|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return fmt.Errorf("open snapshot history lock: %w", err)
	}
	lock := os.NewFile(uintptr(lockFD), historyPath+".lock")
	defer lock.Close()
	if err := syscall.Flock(lockFD, syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock snapshot history: %w", err)
	}
	defer func() { _ = syscall.Flock(lockFD, syscall.LOCK_UN) }()
	var lockStat syscall.Stat_t
	if err := syscall.Fstat(lockFD, &lockStat); err != nil {
		return err
	}
	if lockStat.Mode&syscall.S_IFMT != syscall.S_IFREG || lockStat.Mode&0o777 != 0o600 {
		return fmt.Errorf("snapshot history lock must be a private regular file")
	}
	maxSamples, err := snapshotHistoryLimit(retentionDays)
	if err != nil {
		return err
	}
	var existing []process.Snapshot
	if _, statErr := os.Stat(historyPath); statErr == nil {
		decoded, err := decodeSnapshotHistoryLimited(historyPath, maxSamples)
		if err != nil {
			return err
		}
		existing = decoded
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("inspect snapshot history: %w", statErr)
	}
	merged := make([]process.Snapshot, 0, min(len(existing)+1, maxSamples+1))
	merged = append(merged, existing...)
	merged = append(merged, snapshot)
	for _, item := range merged {
		validator, _ := report.NewAccumulator(item.CapturedAt.Add(-time.Nanosecond), item.CapturedAt.Add(time.Nanosecond))
		if err := validator.AddSnapshot(item); err != nil {
			return fmt.Errorf("validate snapshot history: %w", err)
		}
	}
	sort.Slice(merged, func(i, j int) bool {
		if !merged[i].CapturedAt.Equal(merged[j].CapturedAt) {
			return merged[i].CapturedAt.Before(merged[j].CapturedAt)
		}
		left, _ := json.Marshal(merged[i])
		right, _ := json.Marshal(merged[j])
		return bytes.Compare(left, right) < 0
	})
	deduplicated := merged[:0]
	var previous []byte
	for _, item := range merged {
		canonical, err := json.Marshal(item)
		if err != nil {
			return fmt.Errorf("canonicalize snapshot history: %w", err)
		}
		if previous != nil && bytes.Equal(previous, canonical) {
			continue
		}
		deduplicated = append(deduplicated, item)
		previous = canonical
	}
	newestAt := deduplicated[len(deduplicated)-1].CapturedAt
	boundary := newestAt.AddDate(0, 0, -retentionDays)
	kept := deduplicated[:0]
	for _, item := range deduplicated {
		if !item.CapturedAt.Before(boundary) {
			kept = append(kept, item)
		}
	}
	if len(kept) > maxSamples {
		kept = kept[len(kept)-maxSamples:]
	}
	var data bytes.Buffer
	encoder := json.NewEncoder(&data)
	for _, item := range kept {
		if err := encoder.Encode(item); err != nil {
			return fmt.Errorf("encode snapshot history: %w", err)
		}
	}
	if err := writeAtomic(historyPath, data.Bytes()); err != nil {
		return fmt.Errorf("write snapshot history: %w", err)
	}
	currentData, err := json.MarshalIndent(kept[len(kept)-1], "", "  ")
	if err != nil {
		return fmt.Errorf("encode current snapshot: %w", err)
	}
	currentData = append(currentData, '\n')
	if err := writeAtomic(outputPath, currentData); err != nil {
		return fmt.Errorf("write current snapshot: %w", err)
	}
	return nil
}

func snapshotHistoryLimit(retentionDays int) (int, error) {
	const samplesPerDay = 24 * 60
	if retentionDays <= 0 || retentionDays > int(^uint(0)>>1)/samplesPerDay {
		return 0, fmt.Errorf("invalid snapshot history retention")
	}
	return retentionDays * samplesPerDay, nil
}

func pruneEventFiles(files []eventFile, boundary time.Time) error {
	for _, file := range files {
		if file.count == 0 || !file.maxAt.Before(boundary) {
			continue
		}
		if err := pruneEventFile(file, boundary); err != nil {
			return err
		}
	}
	return nil
}

func pruneEventFile(candidate eventFile, boundary time.Time) error {
	dirFD, err := syscall.Open(filepath.Dir(candidate.path), syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return fmt.Errorf("open event directory for prune: %w", err)
	}
	defer syscall.Close(dirFD)
	name := filepath.Base(candidate.path)
	lockName := eventLifecycleLockName(name)
	lifecycleFD, err := syscall.Openat(dirFD, lockName,
		syscall.O_CREAT|syscall.O_RDWR|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return fmt.Errorf("open event lifecycle lock for prune: %w", err)
	}
	lifecycle := os.NewFile(uintptr(lifecycleFD), lockName)
	defer lifecycle.Close()
	var lifecycleStat syscall.Stat_t
	if err := syscall.Fstat(lifecycleFD, &lifecycleStat); err != nil {
		return fmt.Errorf("inspect event lifecycle lock: %w", err)
	}
	if lifecycleStat.Mode&syscall.S_IFMT != syscall.S_IFREG || lifecycleStat.Mode&0o777 != 0o600 {
		return fmt.Errorf("event lifecycle lock must be a private regular file")
	}
	if err := syscall.Flock(lifecycleFD, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil
		}
		return fmt.Errorf("lock event lifecycle for prune: %w", err)
	}
	defer func() { _ = syscall.Flock(lifecycleFD, syscall.LOCK_UN) }()
	fd, err := syscall.Openat(dirFD, name, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return fmt.Errorf("open event file for prune: %w", err)
	}
	file := os.NewFile(uintptr(fd), name)
	defer file.Close()
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return fmt.Errorf("lock event file for prune: %w", err)
	}
	defer syscall.Flock(fd, syscall.LOCK_UN)
	var stat syscall.Stat_t
	if err := syscall.Fstat(fd, &stat); err != nil {
		return err
	}
	if stat.Mode&syscall.S_IFMT != syscall.S_IFREG || stat.Mode&0o777 != 0o600 || uint64(stat.Dev) != candidate.device ||
		stat.Ino != candidate.inode || stat.Size != candidate.size {
		return nil
	}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var event observe.Event
		if err := decodeStrictJSON(scanner.Bytes(), &event); err != nil {
			return fmt.Errorf("revalidate event file: %w", err)
		}
		if err := event.Validate(); err != nil {
			return fmt.Errorf("revalidate event file: %w", err)
		}
		if !event.At.Before(boundary) {
			return nil
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("revalidate event file: %w", err)
	}
	if err := syscall.Unlinkat(dirFD, name); err != nil {
		return fmt.Errorf("prune expired event file: %w", err)
	}
	// The lifecycle lock is per segment. Remove it only after the event file
	// was unlinked while holding its EX lock; active writers therefore cannot
	// lose their lock sidecar, and a failed sidecar cleanup remains visible.
	if err := syscall.Unlinkat(dirFD, lockName); err != nil && !errors.Is(err, syscall.ENOENT) {
		return fmt.Errorf("prune expired event lifecycle lock: %w", err)
	}
	return nil
}

func pruneObservationStatuses(root string, boundary time.Time) error {
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if errors.Is(walkErr, os.ErrNotExist) && path == root {
				return nil
			}
			return walkErr
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(entry.Name(), ".status.json") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			return nil
		}
		return pruneObservationStatus(path, boundary)
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("prune observation statuses: %w", err)
	}
	return nil
}

func pruneObservationStatus(path string, boundary time.Time) error {
	dirFD, err := syscall.Open(filepath.Dir(path), syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return fmt.Errorf("open observation status directory: %w", err)
	}
	defer syscall.Close(dirFD)
	name := filepath.Base(path)
	fd, err := syscall.Openat(dirFD, name, syscall.O_RDWR|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		if errors.Is(err, syscall.ENOENT) {
			return nil
		}
		return fmt.Errorf("open observation status for prune: %w", err)
	}
	file := os.NewFile(uintptr(fd), name)
	defer file.Close()
	if err := syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil
		}
		return fmt.Errorf("lock observation status for prune: %w", err)
	}
	defer syscall.Flock(fd, syscall.LOCK_UN)
	var stat syscall.Stat_t
	if err := syscall.Fstat(fd, &stat); err != nil {
		return fmt.Errorf("inspect observation status for prune: %w", err)
	}
	if stat.Mode&syscall.S_IFMT != syscall.S_IFREG || stat.Mode&0o777 != 0o600 {
		return fmt.Errorf("observation status must be a private regular file")
	}
	const maxStatusBytes = 64 << 10
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("seek observation status for prune: %w", err)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxStatusBytes+1))
	if err != nil {
		return fmt.Errorf("read observation status for prune: %w", err)
	}
	if len(data) > maxStatusBytes {
		return fmt.Errorf("observation status exceeds %d bytes", maxStatusBytes)
	}
	var status observe.DegradedStatus
	if err := decodeStrictJSON(data, &status); err != nil {
		return fmt.Errorf("decode observation status for prune: %w", err)
	}
	if status.LastEventAt.IsZero() || !status.LastEventAt.Before(boundary) {
		return nil
	}
	if err := syscall.Unlinkat(dirFD, name); err != nil && !errors.Is(err, syscall.ENOENT) {
		return fmt.Errorf("prune observation status: %w", err)
	}
	return nil
}

// eventLifecycleLockName maps each event segment to its own lifecycle lock.
// Writers hold a shared lock only for the current segment; this allows
// pruning closed expired segments while preserving the active segment.
func eventLifecycleLockName(name string) string {
	base := strings.TrimSuffix(name, ".jsonl")
	return base + ".lock"
}

func runRenderConfig(opts renderOptions, stdout io.Writer) error {
	governorInfo, err := os.Stat(opts.governorPath)
	if err != nil || !governorInfo.Mode().IsRegular() || governorInfo.Mode().Perm()&0o111 == 0 {
		return errors.New("governor must be an existing executable regular file")
	}
	file, err := os.Open(opts.configPath)
	if err != nil {
		return errors.New("open render catalog")
	}
	cfg, decodeErr := config.Decode(file)
	closeErr := file.Close()
	if decodeErr != nil {
		return fmt.Errorf("decode render catalog: %w", decodeErr)
	}
	if closeErr != nil {
		return errors.New("close render catalog")
	}
	if cfg.Version != 2 {
		return errors.New("render-config requires config version 2")
	}
	client := config.Client(opts.client)
	data, err := clientconfig.Render(clientconfig.Options{
		Client: client, ConfigPath: opts.configPath, GovernorPath: opts.governorPath, Services: cfg.Services,
	})
	if err != nil {
		return fmt.Errorf("render client config: %w", err)
	}
	if opts.outputPath == "-" {
		written, err := stdout.Write(data)
		if err != nil {
			return fmt.Errorf("write rendered config: %w", err)
		}
		if written != len(data) {
			return fmt.Errorf("write rendered config: %w", io.ErrShortWrite)
		}
		return nil
	}
	if err := validateRenderOutput(opts.outputPath); err != nil {
		return err
	}
	return writeAtomic(opts.outputPath, data)
}

func validateRenderOutput(path string) error {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			return errors.New("render output must be a private regular file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("inspect render output")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return errors.New("create render output directory")
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		return errors.New("render output directory must be private")
	}
	return nil
}

func writeAtomic(path string, data []byte) (resultErr error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}
	temp, err := createTempFile(dir, ".mcp-governor-*")
	if err != nil {
		return fmt.Errorf("create temporary snapshot: %w", err)
	}
	tempPath := temp.Name()
	defer func() {
		if resultErr != nil {
			temp.Close()
			os.Remove(tempPath)
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		return fmt.Errorf("set temporary snapshot permissions: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		return fmt.Errorf("write temporary snapshot: %w", err)
	}
	if err := syncFile(temp); err != nil {
		return fmt.Errorf("sync temporary snapshot: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary snapshot: %w", err)
	}
	if err := os.Chmod(tempPath, 0o600); err != nil {
		return fmt.Errorf("set snapshot permissions: %w", err)
	}
	if err := renameFile(tempPath, path); err != nil {
		return fmt.Errorf("replace snapshot: %w", err)
	}
	if err := syncDirectory(dir); err != nil {
		return err
	}
	return nil
}

func syncDirectoryOS(dir string) error {
	directory, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open output directory: %w", err)
	}
	if err := directory.Sync(); err != nil {
		directory.Close()
		return fmt.Errorf("sync output directory: %w", err)
	}
	if err := directory.Close(); err != nil {
		return fmt.Errorf("close output directory: %w", err)
	}
	return nil
}
