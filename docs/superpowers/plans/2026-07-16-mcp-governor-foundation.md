# MCP Governor Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a read-only WSL2 process governor that measures MCP process PSS/USS, identifies configured service families and registered orphans, and publishes deterministic JSON snapshots without killing or restarting anything.

**Architecture:** A standalone Go module under `tools/mcp-governor` reads Linux procfs through a narrow `ProcFS` interface, classifies processes using explicit configuration rules, and emits versioned snapshots. A systemd user timer runs observation mode periodically; later singleton plans consume the same process identity and snapshot contracts.

**Tech Stack:** Go 1.25 standard library, Linux `/proc`, JSON, systemd user units, Go unit and integration tests.

---

## Delivery Boundary

This plan is the first of four independent deliveries derived from the approved design:

1. Governance foundation and observation mode: this plan.
2. claude-mem and Chroma singleton backends: a follow-up plan.
3. Obsidian multi-session gateway: a follow-up plan.
4. Per-repository CodeGraph gateway: a follow-up plan.

This delivery never sends signals, invokes `systemctl`, rewrites MCP configuration, or exposes a network listener. Its output is evidence used to set later limits safely.

## File Map

- `tools/mcp-governor/go.mod`: isolated module declaration.
- `tools/mcp-governor/cmd/mcp-governor/main.go`: `snapshot` CLI and exit handling.
- `tools/mcp-governor/internal/config/config.go`: strict JSON configuration and defaults.
- `tools/mcp-governor/internal/process/model.go`: stable process identity and memory model.
- `tools/mcp-governor/internal/process/procfs.go`: Linux procfs reader.
- `tools/mcp-governor/internal/process/classifier.go`: explicit service-family classification.
- `tools/mcp-governor/internal/process/snapshot.go`: aggregation and orphan detection.
- `tools/mcp-governor/config.example.json`: reviewed process-family rules.
- `tools/mcp-governor/systemd/mcp-governor-observe.service`: one-shot observer.
- `tools/mcp-governor/systemd/mcp-governor-observe.timer`: periodic user timer.
- `tools/mcp-governor/scripts/install-user-units.sh`: idempotent unit installer.
- `tools/mcp-governor/README.md`: build, configure, run, inspect, uninstall.

### Task 1: Establish The Standalone Module And Snapshot Contract

**Files:**

- Create: `tools/mcp-governor/go.mod`
- Create: `tools/mcp-governor/internal/process/model.go`
- Create: `tools/mcp-governor/internal/process/model_test.go`

- [ ] **Step 1: Write the failing JSON contract test**

```go
package process

import (
 "encoding/json"
 "strings"
 "testing"
 "time"
)

func TestSnapshotJSONContract(t *testing.T) {
 s := Snapshot{Version: 1, CapturedAt: time.Unix(0, 0).UTC(), Mode: "observe", Processes: []Process{{Identity: Identity{PID: 42, StartTicks: 99}, Service: "chroma", PSSBytes: 4096}}}
 b, err := json.Marshal(s)
 if err != nil { t.Fatal(err) }
 got := string(b)
 for _, want := range []string{`"version":1`, `"mode":"observe"`, `"pid":42`, `"start_ticks":99`, `"pss_bytes":4096`} {
  if !strings.Contains(got, want) { t.Fatalf("snapshot %s does not contain %s", got, want) }
 }
}
```

- [ ] **Step 2: Run the test and verify the contract is missing**

Run: `cd tools/mcp-governor && go test ./internal/process -run TestSnapshotJSONContract -v`

Expected: FAIL because `Snapshot`, `Process`, and `Identity` are undefined.

- [ ] **Step 3: Add the module and minimal immutable data model**

```go
// tools/mcp-governor/go.mod
module github.com/byteBuilderX/stratum/tools/mcp-governor

go 1.25.0
```

```go
// tools/mcp-governor/internal/process/model.go
package process

import "time"

type Identity struct {
 PID        int    `json:"pid"`
 StartTicks uint64 `json:"start_ticks"`
}

type Process struct {
 Identity
 PPID       int      `json:"ppid"`
 Command    string   `json:"command"`
 Args       []string `json:"-"` // classification only; version 1 snapshots omit argv
 Service    string   `json:"service,omitempty"`
 RSSBytes   uint64   `json:"rss_bytes"`
 PSSBytes   uint64   `json:"pss_bytes"`
 USSBytes   uint64   `json:"uss_bytes"`
 Registered bool     `json:"registered"`
 Orphan     bool     `json:"orphan"`
}

type ServiceSummary struct {
 Service   string `json:"service"`
 Processes int    `json:"processes"`
 RSSBytes  uint64 `json:"rss_bytes"`
 PSSBytes  uint64 `json:"pss_bytes"`
 USSBytes  uint64 `json:"uss_bytes"`
 Orphans   int    `json:"orphans"`
}

type Snapshot struct {
 Version    int              `json:"version"`
 CapturedAt time.Time        `json:"captured_at"`
 Mode       string           `json:"mode"`
 Processes  []Process        `json:"processes"`
 Services   []ServiceSummary `json:"services"`
 Warnings   []string         `json:"warnings"`
}
```

- [ ] **Step 4: Run and format the model tests**

Run: `cd tools/mcp-governor && gofmt -w internal/process/model.go internal/process/model_test.go && go test ./internal/process -run TestSnapshotJSONContract -v`

Expected: PASS.

- [ ] **Step 5: Commit the contract**

```bash
git add tools/mcp-governor/go.mod tools/mcp-governor/internal/process/model.go tools/mcp-governor/internal/process/model_test.go
git commit -m "feat: define MCP governor snapshot contract"
```

### Task 2: Read Stable Process Identity And PSS/USS From Procfs

**Files:**

- Create: `tools/mcp-governor/internal/process/procfs.go`
- Create: `tools/mcp-governor/internal/process/procfs_test.go`
- Create: `tools/mcp-governor/internal/process/testdata/proc/42/stat`
- Create: `tools/mcp-governor/internal/process/testdata/proc/42/status`
- Create: `tools/mcp-governor/internal/process/testdata/proc/42/cmdline`
- Create: `tools/mcp-governor/internal/process/testdata/proc/42/smaps_rollup`

- [ ] **Step 1: Create procfs fixtures including a command name with spaces**

`stat` must contain:

```text
42 (chrome devtools) S 7 1 1 0 -1 0 0 0 0 0 0 0 0 0 20 0 3 0 98765 0 0
```

`status` must contain:

```text
Name: chrome devtools
PPid: 7
VmRSS: 1200 kB
```

Write `cmdline` as NUL-delimited bytes representing `node`, `/opt/chrome-devtools-mcp`, and `--headless`. `smaps_rollup` must contain:

```text
Rss:                1200 kB
Pss:                 700 kB
Private_Clean:       100 kB
Private_Dirty:       300 kB
```

- [ ] **Step 2: Write failing parsing tests**

```go
func TestProcFSReadProcess(t *testing.T) {
 p := NewProcFS("testdata/proc")
 got, err := p.ReadProcess(42)
 if err != nil { t.Fatal(err) }
 if got.Identity != (Identity{PID: 42, StartTicks: 98765}) { t.Fatalf("identity = %+v", got.Identity) }
 if got.PPID != 7 || got.Command != "chrome devtools" { t.Fatalf("process = %+v", got) }
 if got.PSSBytes != 700*1024 || got.USSBytes != 400*1024 { t.Fatalf("memory = %+v", got) }
 if len(got.Args) != 3 || got.Args[1] != "/opt/chrome-devtools-mcp" { t.Fatalf("args = %q", got.Args) }
}

func TestProcFSReadProcessRejectsReusedPID(t *testing.T) {
 p := NewProcFS("testdata/proc")
 first, err := p.ReadIdentity(42)
 if err != nil { t.Fatal(err) }
 if first.StartTicks != 98765 { t.Fatalf("start ticks = %d", first.StartTicks) }
}
```

- [ ] **Step 3: Run tests and verify failure**

Run: `cd tools/mcp-governor && go test ./internal/process -run ProcFS -v`

Expected: FAIL because `NewProcFS`, `ReadProcess`, and `ReadIdentity` do not exist.

- [ ] **Step 4: Implement the procfs reader**

Implement `ProcFS` with `root string`, `ListPIDs() ([]int, error)`, `ReadIdentity(pid int) (Identity, error)`, and `ReadProcess(pid int) (Process, error)`. Parse `/proc/<pid>/stat` by locating the final `)` before splitting fields; start time is stat field 22, which is split index 19 after the command. Parse `PPid` and `VmRSS` from `status`, split `cmdline` on NUL bytes, and compute USS as `Private_Clean + Private_Dirty + Private_Hugetlb` from `smaps_rollup`.

Return a typed `ProcessGoneError` when any identity-critical file disappears. Treat a missing or permission-denied `smaps_rollup` as a warning-capable zero PSS/USS result, not a fabricated RSS substitute.

- [ ] **Step 5: Verify parser and race tests**

Run: `cd tools/mcp-governor && gofmt -w internal/process && go test -race ./internal/process -run ProcFS -v`

Expected: PASS with exact fixture values.

- [ ] **Step 6: Commit procfs measurement**

```bash
git add tools/mcp-governor/internal/process
git commit -m "feat: measure MCP processes through procfs"
```

### Task 3: Add Strict Classification Configuration

**Files:**

- Create: `tools/mcp-governor/internal/config/config.go`
- Create: `tools/mcp-governor/internal/config/config_test.go`
- Create: `tools/mcp-governor/config.example.json`
- Create: `tools/mcp-governor/internal/process/classifier.go`
- Create: `tools/mcp-governor/internal/process/classifier_test.go`

- [ ] **Step 1: Write failing strict-config and classification tests**

```go
func TestDecodeRejectsUnknownFields(t *testing.T) {
 _, err := Decode(strings.NewReader(`{"version":1,"unknown":true,"services":[]}`))
 if err == nil || !strings.Contains(err.Error(), "unknown") { t.Fatalf("error = %v", err) }
}
```

```go
func TestClassifierRequiresAllConfiguredFragments(t *testing.T) {
 c := NewClassifier([]Rule{{Name: "chroma", AllArgsContain: []string{"chroma-mcp", "--data-dir"}}})
 if got := c.Classify(Process{Args: []string{"python", "chroma-mcp", "--data-dir", "/data"}}); got != "chroma" { t.Fatalf("service = %q", got) }
 if got := c.Classify(Process{Args: []string{"python", "other-mcp"}}); got != "" { t.Fatalf("service = %q", got) }
}
```

- [ ] **Step 2: Run tests and verify missing implementations**

Run: `cd tools/mcp-governor && go test ./internal/config ./internal/process -run 'Decode|Classifier' -v`

Expected: FAIL with undefined configuration and classifier symbols.

- [ ] **Step 3: Implement versioned strict JSON configuration**

Define this schema in `config.go` and reject trailing JSON values, unknown fields, duplicate or empty service names, empty fragment lists, and versions other than `1`:

```go
type Config struct {
 Version       int           `json:"version"`
 OutputPath    string        `json:"output_path"`
 RegistryPath  string        `json:"registry_path"`
 Services      []ServiceRule `json:"services"`
}

type ServiceRule struct {
 Name           string   `json:"name"`
 AllArgsContain []string `json:"all_args_contain"`
}
```

The example config must classify only reviewed command fragments for `chroma`, `codegraph`, `obsidian`, `claude-mem`, `headroom`, `playwright`, and `chrome-devtools`. Set output to `%h/.local/state/mcp-governor/snapshot.json` and registry to `%h/.local/state/mcp-governor/registry.json`; path expansion occurs in the CLI, not the config package.

- [ ] **Step 4: Implement first-match classification**

Define `process.Rule` and `Classifier`. Require every configured fragment to occur in at least one argument. Reject ambiguous configuration during classifier construction if two rules are structurally identical. Do not classify by regex or bare process name.

- [ ] **Step 5: Run all configuration tests**

Run: `cd tools/mcp-governor && gofmt -w internal/config internal/process && go test -race ./internal/config ./internal/process -v`

Expected: PASS.

- [ ] **Step 6: Commit classification**

```bash
git add tools/mcp-governor/internal/config tools/mcp-governor/internal/process tools/mcp-governor/config.example.json
git commit -m "feat: classify configured MCP process families"
```

### Task 4: Build Deterministic Observation Snapshots

**Files:**

- Create: `tools/mcp-governor/internal/process/snapshot.go`
- Create: `tools/mcp-governor/internal/process/snapshot_test.go`
- Create: `tools/mcp-governor/internal/process/registry.go`
- Create: `tools/mcp-governor/internal/process/registry_test.go`

- [ ] **Step 1: Write failing orphan and aggregation tests**

```go
func TestBuildSnapshotMarksOnlyRegisteredDeadParentAsOrphan(t *testing.T) {
 processes := []Process{
  {Identity: Identity{PID: 10, StartTicks: 100}, PPID: 1, Service: "obsidian", PSSBytes: 10},
  {Identity: Identity{PID: 11, StartTicks: 110}, PPID: 1, Service: "obsidian", PSSBytes: 20},
 }
 registry := []Registration{{Identity: Identity{PID: 10, StartTicks: 100}, Client: Identity{PID: 99, StartTicks: 999}, Service: "obsidian"}}
 s := BuildSnapshot(time.Unix(0, 0).UTC(), processes, registry, map[Identity]bool{})
 if !s.Processes[0].Orphan || s.Processes[1].Orphan { t.Fatalf("processes = %+v", s.Processes) }
 if len(s.Services) != 1 || s.Services[0].PSSBytes != 30 || s.Services[0].Orphans != 1 { t.Fatalf("services = %+v", s.Services) }
}
```

- [ ] **Step 2: Run the test and verify it fails**

Run: `cd tools/mcp-governor && go test ./internal/process -run BuildSnapshot -v`

Expected: FAIL because registration and snapshot builders are undefined.

- [ ] **Step 3: Implement registry and snapshot rules**

Use this registry contract:

```go
type Registration struct {
 Identity  Identity  `json:"identity"`
 Client    Identity  `json:"client"`
 Service   string    `json:"service"`
 Repository string   `json:"repository,omitempty"`
 ConnectedAt time.Time `json:"connected_at"`
}
```

Mark a process orphaned only when all conditions hold: its exact `(pid,start_ticks)` is registered, its registered client identity is absent, and the process identity still matches procfs. An unregistered PPID-1 process is reported but never marked reclaimable. Sort processes by service then PID and summaries by service so identical input yields stable JSON.

- [ ] **Step 4: Add tests for PID reuse and deterministic order**

Add cases where the client PID exists with different start ticks, where a child PID is reused, and where input order is reversed. Reused identities must not match; reversed input must marshal to the same process and service ordering.

- [ ] **Step 5: Run snapshot tests**

Run: `cd tools/mcp-governor && gofmt -w internal/process && go test -race ./internal/process -v`

Expected: PASS.

- [ ] **Step 6: Commit observation logic**

```bash
git add tools/mcp-governor/internal/process
git commit -m "feat: report MCP service usage and registered orphans"
```

### Task 5: Add The Read-Only CLI And Atomic Output

**Files:**

- Create: `tools/mcp-governor/cmd/mcp-governor/main.go`
- Create: `tools/mcp-governor/cmd/mcp-governor/main_test.go`

- [ ] **Step 1: Write failing CLI integration tests**

Test a `run(args []string, stdout, stderr io.Writer) int` function with a fixture proc root. Assert that `snapshot --config <path> --proc-root <fixture> --output -` returns `0` and valid version-1 JSON. Assert missing config returns `2`, unreadable procfs returns `1`, and extra positional arguments return `2`.

- [ ] **Step 2: Run and verify failure**

Run: `cd tools/mcp-governor && go test ./cmd/mcp-governor -v`

Expected: FAIL because the CLI does not exist.

- [ ] **Step 3: Implement the CLI**

Support only:

```text
mcp-governor snapshot --config PATH [--proc-root /proc] [--output PATH|-]
```

Expand a leading `%h/` using `os.UserHomeDir`. Scan numeric proc directories, skip `ProcessGoneError`, attach warnings for unreadable PSS/USS, classify processes, load the optional registry as an empty list when it does not exist, and write JSON with two-space indentation.

For file output, create the parent with mode `0700`, write and fsync a same-directory temporary file with mode `0600`, rename it atomically, then fsync the directory. Never leave a partially written snapshot.

- [ ] **Step 4: Verify CLI, race, and static analysis**

Run: `cd tools/mcp-governor && gofmt -w cmd internal && go test -race ./... && go vet ./...`

Expected: all packages PASS and `go vet` exits 0.

- [ ] **Step 5: Build and smoke-test against live procfs**

Run:

```bash
cd tools/mcp-governor
go build -o /tmp/mcp-governor ./cmd/mcp-governor
/tmp/mcp-governor snapshot --config config.example.json --output /tmp/mcp-snapshot.json
jq '{version,mode,services,warnings}' /tmp/mcp-snapshot.json
```

Expected: JSON reports `version: 1`, `mode: "observe"`, classified live services, and no malformed numeric values. Missing `smaps_rollup` access appears in `warnings` rather than failing the scan.

- [ ] **Step 6: Commit the CLI**

```bash
git add tools/mcp-governor/cmd
git commit -m "feat: add read-only MCP governor snapshot CLI"
```

### Task 6: Install Observation Mode As A Systemd User Timer

**Files:**

- Create: `tools/mcp-governor/systemd/mcp-governor-observe.service`
- Create: `tools/mcp-governor/systemd/mcp-governor-observe.timer`
- Create: `tools/mcp-governor/scripts/install-user-units.sh`
- Create: `tools/mcp-governor/scripts/install_user_units_test.sh`
- Create: `tools/mcp-governor/README.md`

- [ ] **Step 1: Write a failing isolated installer test**

The shell test must set temporary `HOME`, `XDG_CONFIG_HOME`, and `XDG_STATE_HOME`, invoke the installer with `SYSTEMCTL=true`, and assert executable installation at `$HOME/.local/bin/mcp-governor`, config mode `0600`, unit files under `$HOME/.config/systemd/user`, and preservation of an existing config on a second run.

- [ ] **Step 2: Run and verify failure**

Run: `bash tools/mcp-governor/scripts/install_user_units_test.sh`

Expected: FAIL because the installer and units do not exist.

- [ ] **Step 3: Add hardened observation units**

The service must be `Type=oneshot`, execute `%h/.local/bin/mcp-governor snapshot --config %h/.config/mcp-governor/config.json`, and include:

```ini
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ProtectHome=read-only
ReadWritePaths=%h/.local/state/mcp-governor
RestrictAddressFamilies=AF_UNIX
LockPersonality=yes
```

The timer must use `OnBootSec=2min`, `OnUnitActiveSec=1min`, `RandomizedDelaySec=10s`, `Persistent=false`, and `WantedBy=timers.target`.

- [ ] **Step 4: Implement the idempotent installer**

Use `set -euo pipefail`; build from the tool module; install the binary atomically; create configuration only when absent; install both units; run `${SYSTEMCTL:-systemctl} --user daemon-reload`; and enable only the timer with `--now`. Do not request sudo and do not enable cleanup behavior.

- [ ] **Step 5: Document operations and rollback**

README commands must cover build, one-shot snapshot, timer installation, `systemctl --user status`, `journalctl --user-unit`, snapshot inspection with `jq`, timer disablement, and complete uninstall. State explicitly that observation mode never sends signals or rewrites MCP configurations.

- [ ] **Step 6: Verify installer and unit syntax**

Run:

```bash
bash tools/mcp-governor/scripts/install_user_units_test.sh
systemd-analyze --user verify tools/mcp-governor/systemd/mcp-governor-observe.service tools/mcp-governor/systemd/mcp-governor-observe.timer
cd tools/mcp-governor && go test -race ./... && go vet ./...
```

Expected: installer test PASS, both units verify without errors, Go tests PASS, and `go vet` exits 0.

- [ ] **Step 7: Commit observation deployment**

```bash
git add tools/mcp-governor/systemd tools/mcp-governor/scripts tools/mcp-governor/README.md
git commit -m "feat: install MCP governor observation timer"
```

### Task 7: Capture And Review The Governance Baseline

**Files:**

- Create: `docs/operations/mcp-governor-baseline.md`

- [ ] **Step 1: Install observation mode for the current user**

Run: `bash tools/mcp-governor/scripts/install-user-units.sh`

Expected: timer is active and no existing MCP client configuration changes.

- [ ] **Step 2: Capture idle and active snapshots**

Run an idle snapshot, then run representative concurrent Claude, Codex, and VS Code MCP activity and capture a second snapshot. Record PSS/USS, process count, orphan candidates, scan warnings, and snapshot duration per service.

- [ ] **Step 3: Verify ownership manually before enabling future cleanup**

For every reported orphan candidate, compare `(pid,start_ticks)`, client identity, `/proc/<pid>/status`, and the relevant process tree. Expected: no unregistered process is described as reclaimable and no PID-reuse mismatch is accepted.

- [ ] **Step 4: Write the baseline report**

Document the two snapshot timestamps, exact measurement commands, per-service tables, warnings, ownership review, and explicit recommendations for memory and task limits. Do not set a limit below the observed active PSS plus a documented safety margin.

- [ ] **Step 5: Run final verification**

Run:

```bash
cd tools/mcp-governor && go test -race ./... && go vet ./...
git diff --check
systemctl --user is-active mcp-governor-observe.timer
jq -e '.version == 1 and .mode == "observe"' ~/.local/state/mcp-governor/snapshot.json
```

Expected: tests and vet PASS, no whitespace errors, timer reports `active`, and `jq` returns true.

- [ ] **Step 6: Commit the measured baseline**

```bash
git add docs/operations/mcp-governor-baseline.md
git commit -m "docs: record MCP process governance baseline"
```

## Completion Gate

Do not begin singleton migration until all of these hold:

- Observation mode has run through at least one idle and one representative active period.
- Snapshot PSS/USS values have been spot-checked against `smaps_rollup`.
- No false reclaimable orphan has been found.
- The timer can be disabled and uninstalled without affecting Claude, Codex, VS Code, or their MCP configuration.
- The baseline report identifies concrete targets for claude-mem and Chroma migration.
