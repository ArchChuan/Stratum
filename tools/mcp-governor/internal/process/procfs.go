package process

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type ProcessGoneError struct {
	PID int
	Err error
}

func (e *ProcessGoneError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("process %d disappeared: %v", e.PID, e.Err)
	}
	return fmt.Sprintf("process %d identity changed", e.PID)
}

func (e *ProcessGoneError) Unwrap() error { return e.Err }

type ProcFS struct {
	root     string
	readFile func(string) ([]byte, error)
}

func NewProcFS(root string) *ProcFS {
	return &ProcFS{root: root, readFile: os.ReadFile}
}

func (p *ProcFS) ListPIDs() ([]int, error) {
	entries, err := os.ReadDir(p.root)
	if err != nil {
		return nil, fmt.Errorf("read procfs root: %w", err)
	}
	pids := make([]int, 0, len(entries))
	for _, entry := range entries {
		if strings.Trim(entry.Name(), "0123456789") != "" {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err == nil && pid > 0 {
			pids = append(pids, pid)
		}
	}
	sort.Ints(pids)
	return pids, nil
}

func (p *ProcFS) ReadIdentity(pid int) (Identity, error) {
	data, err := p.readFile(p.path(pid, "stat"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Identity{}, &ProcessGoneError{PID: pid, Err: err}
		}
		return Identity{}, fmt.Errorf("read stat for process %d: %w", pid, err)
	}
	_, _, startTicks, err := parseStat(data)
	if err != nil {
		return Identity{}, fmt.Errorf("parse stat for process %d: %w", pid, err)
	}
	return Identity{PID: pid, StartTicks: startTicks}, nil
}

func (p *ProcFS) ReadProcess(pid int) (Process, []string, error) {
	identity, err := p.ReadIdentity(pid)
	if err != nil {
		return Process{}, nil, err
	}

	stat, err := p.readCoreFile(pid, "stat")
	if err != nil {
		return Process{}, nil, err
	}
	command, statPPID, _, err := parseStat(stat)
	if err != nil {
		return Process{}, nil, fmt.Errorf("parse stat for process %d: %w", pid, err)
	}
	status, err := p.readCoreFile(pid, "status")
	if err != nil {
		return Process{}, nil, err
	}
	_, rss, err := parseStatus(status)
	if err != nil {
		return Process{}, nil, fmt.Errorf("parse status for process %d: %w", pid, err)
	}
	cmdline, err := p.readCoreFile(pid, "cmdline")
	if err != nil {
		return Process{}, nil, err
	}

	result := Process{Identity: identity, PPID: statPPID, Command: command, Args: parseCmdline(cmdline), RSSBytes: rss}
	var warnings []string
	rollup, err := p.readFile(p.path(pid, "smaps_rollup"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
			warnings = append(warnings, fmt.Sprintf("process %d smaps_rollup unavailable: %v", pid, err))
		} else {
			return Process{}, nil, fmt.Errorf("read smaps_rollup for process %d: %w", pid, err)
		}
	} else {
		result.PSSBytes, result.USSBytes, err = parseSmapsRollup(rollup)
		if err != nil {
			return Process{}, nil, fmt.Errorf("parse smaps_rollup for process %d: %w", pid, err)
		}
	}

	finalIdentity, err := p.ReadIdentity(pid)
	if err != nil {
		return Process{}, nil, err
	}
	if finalIdentity != identity {
		return Process{}, nil, &ProcessGoneError{PID: pid}
	}
	return result, warnings, nil
}

func (p *ProcFS) readCoreFile(pid int, name string) ([]byte, error) {
	data, err := p.readFile(p.path(pid, name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, &ProcessGoneError{PID: pid, Err: err}
	}
	if err != nil {
		return nil, fmt.Errorf("read %s for process %d: %w", name, pid, err)
	}
	return data, nil
}

func (p *ProcFS) path(pid int, name string) string {
	return filepath.Join(p.root, strconv.Itoa(pid), name)
}

func parseStat(data []byte) (string, int, uint64, error) {
	line := strings.TrimSpace(string(data))
	open := strings.IndexByte(line, '(')
	close := strings.LastIndexByte(line, ')')
	if open < 0 || close <= open {
		return "", 0, 0, errors.New("missing command parentheses")
	}
	fields := strings.Fields(line[close+1:])
	// fields begin with proc stat field 3 (state); starttime is field 22.
	if len(fields) < 20 {
		return "", 0, 0, fmt.Errorf("got %d fields after command, need at least 20", len(fields))
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return "", 0, 0, fmt.Errorf("invalid PPID: %w", err)
	}
	startTicks, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return "", 0, 0, fmt.Errorf("invalid start ticks: %w", err)
	}
	return line[open+1 : close], ppid, startTicks, nil
}

func parseStatus(data []byte) (int, uint64, error) {
	values := map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), ":")
		if ok {
			values[key] = strings.TrimSpace(value)
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, 0, err
	}
	if values["Name"] == "" {
		return 0, 0, errors.New("missing Name")
	}
	ppid, err := strconv.Atoi(values["PPid"])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid PPid: %w", err)
	}
	rss, err := parseKB(values["VmRSS"])
	if err != nil {
		return 0, 0, fmt.Errorf("invalid VmRSS: %w", err)
	}
	return ppid, rss, nil
}

func parseSmapsRollup(data []byte) (uint64, uint64, error) {
	values := map[string]uint64{}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), ":")
		if !ok || (key != "Rss" && key != "Pss" && key != "Private_Clean" && key != "Private_Dirty" && key != "Private_Hugetlb") {
			continue
		}
		bytes, err := parseKB(strings.TrimSpace(value))
		if err != nil {
			return 0, 0, fmt.Errorf("invalid %s: %w", key, err)
		}
		values[key] = bytes
	}
	if err := scanner.Err(); err != nil {
		return 0, 0, err
	}
	for _, required := range []string{"Rss", "Pss", "Private_Clean", "Private_Dirty"} {
		if _, ok := values[required]; !ok {
			return 0, 0, fmt.Errorf("missing %s", required)
		}
	}
	return values["Pss"], values["Private_Clean"] + values["Private_Dirty"] + values["Private_Hugetlb"], nil
}

func parseKB(value string) (uint64, error) {
	fields := strings.Fields(value)
	if len(fields) != 2 || fields[1] != "kB" {
		return 0, fmt.Errorf("expected '<number> kB', got %q", value)
	}
	kb, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil {
		return 0, err
	}
	return kb * 1024, nil
}

func parseCmdline(data []byte) []string {
	parts := strings.Split(strings.TrimRight(string(data), "\x00"), "\x00")
	if len(parts) == 1 && parts[0] == "" {
		return nil
	}
	return parts
}
