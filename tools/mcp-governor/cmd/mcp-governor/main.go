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
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

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
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
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
		if err := runProxy(opts, stdout, stderr); err != nil {
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
	default:
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		printUsage(stderr)
		return 2
	}
}

const proxyUsage = "usage: mcp-governor proxy --config PATH --client CLIENT --service SERVICE " +
	"[--session PID:START_TICKS] [--repository PATH] -- COMMAND [ARG...]"

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: mcp-governor snapshot|proxy|report ...")
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
	writer, err := observe.NewWriter(eventsDir, opts.client, sessionHash)
	if err != nil {
		return fmt.Errorf("create observation writer: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, writer.Close()) }()
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
	return proxy.Run(context.Background(), proxy.Options{
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
	if !opts.allowPartial && opts.end.Sub(opts.start) < 7*24*time.Hour {
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
	events, files, err := readEventFiles(eventsDir)
	if err != nil {
		return err
	}
	snapshots, err := readSnapshots(snapshotPath)
	if err != nil {
		return err
	}
	aggregate, err := report.Aggregate(opts.start, opts.end, events, snapshots)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(aggregate, "", "  ")
	if err != nil {
		return fmt.Errorf("encode report: %w", err)
	}
	data = append(data, '\n')
	if opts.outputSet && opts.outputPath == "-" {
		if _, err := stdout.Write(data); err != nil {
			return fmt.Errorf("write report: %w", err)
		}
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
		if err := writeAtomic(output, data); err != nil {
			return err
		}
	}
	boundary := opts.end.AddDate(0, 0, -cfg.Observation.RawRetentionDays)
	if err := pruneEventFiles(files, boundary); err != nil {
		return err
	}
	return nil
}

func safeReportTime(value time.Time) string { return value.UTC().Format("20060102T150405Z") }

type eventFile struct {
	path   string
	events []observe.Event
}

func readEventFiles(root string) ([]observe.Event, []eventFile, error) {
	var all []observe.Event
	var files []eventFile
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
			return nil
		}
		events, err := decodeEventJSONL(path)
		if err != nil {
			return err
		}
		files = append(files, eventFile{path: path, events: events})
		all = append(all, events...)
		return nil
	})
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, nil, fmt.Errorf("read events: %w", err)
	}
	return all, files, nil
}

func decodeEventJSONL(path string) ([]observe.Event, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open event file: %w", err)
	}
	defer file.Close()
	var events []observe.Event
	scanner := bufio.NewScanner(file)
	buffer := make([]byte, 64*1024)
	scanner.Buffer(buffer, 4*1024*1024)
	for line := 1; scanner.Scan(); line++ {
		data := bytes.TrimSpace(scanner.Bytes())
		if len(data) == 0 {
			return nil, fmt.Errorf("%s:%d: empty JSONL record", path, line)
		}
		var event observe.Event
		if err := decodeStrictJSON(data, &event); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, line, err)
		}
		if err := event.Validate(); err != nil {
			return nil, fmt.Errorf("%s:%d: validate event: %w", path, line, err)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan event file: %w", err)
	}
	return events, nil
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

func readSnapshots(path string) ([]process.Snapshot, error) {
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

func pruneEventFiles(files []eventFile, boundary time.Time) error {
	for _, file := range files {
		if len(file.events) == 0 {
			continue
		}
		old := true
		for _, event := range file.events {
			if !event.At.Before(boundary) {
				old = false
				break
			}
		}
		if !old {
			continue
		}
		info, err := os.Lstat(file.path)
		if err != nil {
			return fmt.Errorf("inspect expired event file: %w", err)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if err := os.Remove(file.path); err != nil {
			return fmt.Errorf("prune expired event file: %w", err)
		}
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
