package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/byteBuilderX/stratum/tools/mcp-governor/internal/config"
	"github.com/byteBuilderX/stratum/tools/mcp-governor/internal/process"
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
)

type options struct {
	configPath string
	procRoot   string
	outputPath string
	outputSet  bool
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
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
